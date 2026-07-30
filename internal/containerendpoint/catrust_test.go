package containerendpoint

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/hostbridge/trust"
)

// catrust_test.go — how containers Overcast starts come to trust its TLS.
//
// Under OVERCAST_TLS=auto the API listener serves only TLS, so a function
// handed an http endpoint gets Go's plaintext rejection ("Client sent an HTTP
// request to an HTTPS server") surfaced as SDK deserialization garbage — every
// AWS call from every Lambda failed. The endpoint must switch scheme with the
// server, and the minting CA must be trusted inside the container: SDKs
// honour AWS_CA_BUNDLE (botocore, Go, CLI), NODE_EXTRA_CA_CERTS (Node),
// SSL_CERT_FILE/REQUESTS_CA_BUNDLE (OpenSSL/requests). The Java SDK trusts
// only its own keystore and is documented as the caveat.

func autoTLSConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	caDir := trust.DirFor(dir)
	if err := os.MkdirAll(caDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pem := "-----BEGIN CERTIFICATE-----\nMIIFAKE\n-----END CERTIFICATE-----\n"
	if err := os.WriteFile(filepath.Join(caDir, trust.CACertFile), []byte(pem), 0o644); err != nil {
		t.Fatal(err)
	}
	return &config.Config{Port: 4566, TLSMode: config.TLSModeAuto, DataDir: dir}
}

// BaseURL is the one place the endpoint scheme is decided, for both the
// Lambda and ECS runtimes. It must follow the server's listener.
func TestBaseURL_schemeFollowsTheListener(t *testing.T) {
	if got := BaseURL(&config.Config{Port: 4566}, "172.18.0.2"); got != "http://172.18.0.2:4566" {
		t.Errorf("plain: BaseURL = %q, want http", got)
	}
	if got := BaseURL(autoTLSConfig(t), "172.18.0.2"); got != "https://172.18.0.2:4566" {
		t.Errorf("TLS auto: BaseURL = %q, want https", got)
	}
	if got := BaseURL(&config.Config{Port: 4566, TLSCertFile: "c.pem", TLSKeyFile: "k.pem"}, "h"); got != "https://h:4566" {
		t.Errorf("explicit TLS: BaseURL = %q, want https", got)
	}
}

func TestCABundleEnv_setOnlyWhenTLSIsServed(t *testing.T) {
	plain := New(&config.Config{Port: 4566}, "http://172.18.0.2:4566")
	if env := plain.CABundleEnv(); len(env) != 0 {
		t.Errorf("plain HTTP: CABundleEnv = %v, want none", env)
	}

	m := New(autoTLSConfig(t), "https://172.18.0.2:4566")
	env := m.CABundleEnv()
	for _, k := range []string{"AWS_CA_BUNDLE", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "REQUESTS_CA_BUNDLE"} {
		if env[k] != ContainerCABundlePath {
			t.Errorf("CABundleEnv[%s] = %q, want %q", k, env[k], ContainerCABundlePath)
		}
	}
}

// The bundle travels by the same mechanism as function code — CopyToContainer
// — because when Overcast itself runs in Docker its data dir is not a host
// path, so a bind mount cannot carry the file to a sibling container.
func TestCABundleTar_carriesTheCACertificate(t *testing.T) {
	m := New(autoTLSConfig(t), "https://172.18.0.2:4566")

	data, err := m.CABundleTar()
	if err != nil {
		t.Fatalf("CABundleTar: %v", err)
	}
	tr := tar.NewReader(bytes.NewReader(data))
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if "/"+hdr.Name == ContainerCABundlePath || hdr.Name == strings.TrimPrefix(ContainerCABundlePath, "/") {
			body, _ := io.ReadAll(tr)
			if !strings.Contains(string(body), "BEGIN CERTIFICATE") {
				t.Errorf("bundle entry does not contain a PEM certificate: %q", body)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("tar does not contain %s", ContainerCABundlePath)
	}

	// And with TLS off there is nothing to inject.
	plain := New(&config.Config{Port: 4566}, "http://172.18.0.2:4566")
	if data, err := plain.CABundleTar(); err != nil || data != nil {
		t.Errorf("plain HTTP: CABundleTar = (%d bytes, %v), want (nil, nil)", len(data), err)
	}
}

// Under TLS, rewriting a baked URL to the raw endpoint address would trade a
// scheme error for a certificate error: the leaf's SANs cover Overcast's
// names, never a container-network IP. The rewrite target must be the named
// client endpoint instead.
func TestRewriteURLs_targetsANameUnderTLS(t *testing.T) {
	cfg := autoTLSConfig(t)
	m := New(cfg, "https://172.18.0.2:4566")

	got := m.RewriteURLs("https://localhost:4566/000000000000/orders")
	want := "https://localhost.overcast.sh:4566/000000000000/orders"
	if got != want {
		t.Errorf("TLS rewrite = %q, want the SAN-covered name %q", got, want)
	}
	// Plain HTTP keeps the address target — resolution-free, and pinned by the
	// existing rewrite tests.
	plain := New(&config.Config{Port: 4566}, "http://172.18.0.2:4566")
	if got := plain.RewriteURLs("http://localhost:4566/q"); got != "http://172.18.0.2:4566/q" {
		t.Errorf("plain rewrite = %q, want the address target", got)
	}
}
