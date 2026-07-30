package trust

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// leafFromTLS parses the leaf certificate out of a tls.Certificate.
func leafFromTLS(t *testing.T, c tls.Certificate) *x509.Certificate {
	t.Helper()
	if len(c.Certificate) == 0 {
		t.Fatal("tls.Certificate carries no DER certificates")
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}
	return leaf
}

func TestLoadOrCreateCA_createsThenReuses(t *testing.T) {
	// Given: an empty CA directory
	dir := t.TempDir()

	// When: the CA is created
	ca1, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA (create): %v", err)
	}

	// Then: both key material files exist on disk
	for _, f := range []string{CACertFile, caKeyFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
	if !ca1.Cert.IsCA {
		t.Error("CA certificate does not have IsCA set")
	}

	// And: a second call loads the same CA rather than minting a new one
	ca2, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA (reuse): %v", err)
	}
	if ca1.Cert.SerialNumber.Cmp(ca2.Cert.SerialNumber) != 0 {
		t.Errorf("second LoadOrCreateCA minted a new CA: serial %v != %v",
			ca2.Cert.SerialNumber, ca1.Cert.SerialNumber)
	}
}

func TestServerCertificate_coversRequestedSANs(t *testing.T) {
	// Given: the SAN set the auto TLS mode requests
	dir := t.TempDir()
	sans := []string{
		"localhost", "127.0.0.1", "::1",
		"localhost.overcast.sh", "*.localhost.overcast.sh",
	}

	// When: a server certificate is minted
	cert, pool, err := ServerCertificate(dir, sans)
	if err != nil {
		t.Fatalf("ServerCertificate: %v", err)
	}
	leaf := leafFromTLS(t, cert)

	// Then: every DNS name is present
	for _, want := range []string{"localhost", "localhost.overcast.sh", "*.localhost.overcast.sh"} {
		if !slices.Contains(leaf.DNSNames, want) {
			t.Errorf("leaf DNSNames %v missing %q", leaf.DNSNames, want)
		}
	}
	// And: the IP literals became IP SANs, not DNS SANs
	for _, want := range []string{"127.0.0.1", "::1"} {
		found := slices.ContainsFunc(leaf.IPAddresses, func(ip net.IP) bool {
			return ip.Equal(net.ParseIP(want))
		})
		if !found {
			t.Errorf("leaf IPAddresses %v missing %s", leaf.IPAddresses, want)
		}
	}

	// And: the leaf chains to the returned CA pool for a wildcard name a
	// browser would actually dial
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		DNSName:   "app.localhost.overcast.sh",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("leaf does not verify against the CA pool: %v", err)
	}
}

func TestServerCertificate_reusesCachedLeaf(t *testing.T) {
	// Given: a leaf already minted for this SAN set
	dir := t.TempDir()
	sans := []string{"localhost", "127.0.0.1"}
	first, _, err := ServerCertificate(dir, sans)
	if err != nil {
		t.Fatalf("ServerCertificate (mint): %v", err)
	}

	// When: the same SAN set is requested again
	second, _, err := ServerCertificate(dir, sans)
	if err != nil {
		t.Fatalf("ServerCertificate (reuse): %v", err)
	}

	// Then: the cached leaf is reused, not re-minted
	l1, l2 := leafFromTLS(t, first), leafFromTLS(t, second)
	if l1.SerialNumber.Cmp(l2.SerialNumber) != 0 {
		t.Errorf("leaf was re-minted for an identical SAN set: serial %v != %v",
			l2.SerialNumber, l1.SerialNumber)
	}
}

func TestServerCertificate_remintsWhenSANsChange(t *testing.T) {
	// Given: a cached leaf that does not cover a newly-required SAN
	dir := t.TempDir()
	first, _, err := ServerCertificate(dir, []string{"localhost"})
	if err != nil {
		t.Fatalf("ServerCertificate (mint): %v", err)
	}

	// When: the SAN set grows (e.g. OVERCAST_HOSTNAME was set)
	second, _, err := ServerCertificate(dir, []string{"localhost", "overcast.internal"})
	if err != nil {
		t.Fatalf("ServerCertificate (grown SANs): %v", err)
	}

	// Then: a new leaf is minted and covers the new name
	l1, l2 := leafFromTLS(t, first), leafFromTLS(t, second)
	if l1.SerialNumber.Cmp(l2.SerialNumber) == 0 {
		t.Error("leaf was not re-minted after the SAN set changed")
	}
	if !slices.Contains(l2.DNSNames, "overcast.internal") {
		t.Errorf("re-minted leaf DNSNames %v missing overcast.internal", l2.DNSNames)
	}
}

func TestServerCertificate_remintsWhenExpiringSoon(t *testing.T) {
	// Given: a cached leaf that is inside the renewal window
	dir := t.TempDir()
	sans := []string{"localhost"}
	first, _, err := ServerCertificate(dir, sans)
	if err != nil {
		t.Fatalf("ServerCertificate (mint): %v", err)
	}
	l1 := leafFromTLS(t, first)

	// When: the clock has advanced to within the renewal margin of expiry
	restore := now
	now = func() time.Time { return l1.NotAfter.Add(-time.Hour) }
	t.Cleanup(func() { now = restore })

	second, _, err := ServerCertificate(dir, sans)
	if err != nil {
		t.Fatalf("ServerCertificate (near expiry): %v", err)
	}

	// Then: a fresh leaf replaces the expiring one
	if l2 := leafFromTLS(t, second); l1.SerialNumber.Cmp(l2.SerialNumber) == 0 {
		t.Error("leaf was not re-minted despite expiring within the renewal margin")
	}
}

func TestServerCertificate_remintsWhenCARotated(t *testing.T) {
	// Given: a cached leaf whose CA key material has since been replaced
	dir := t.TempDir()
	sans := []string{"localhost"}
	first, _, err := ServerCertificate(dir, sans)
	if err != nil {
		t.Fatalf("ServerCertificate (mint): %v", err)
	}
	for _, f := range []string{CACertFile, caKeyFile} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			t.Fatalf("remove %s: %v", f, err)
		}
	}

	// When: a certificate is requested against the recreated CA
	second, pool, err := ServerCertificate(dir, sans)
	if err != nil {
		t.Fatalf("ServerCertificate (rotated CA): %v", err)
	}

	// Then: the stale leaf is replaced with one that chains to the new CA
	l1, l2 := leafFromTLS(t, first), leafFromTLS(t, second)
	if l1.SerialNumber.Cmp(l2.SerialNumber) == 0 {
		t.Error("stale leaf survived a CA rotation")
	}
	if _, err := l2.Verify(x509.VerifyOptions{
		Roots:     pool,
		DNSName:   "localhost",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("re-minted leaf does not verify against the rotated CA: %v", err)
	}
}
