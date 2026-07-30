package trust

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 is the thumbprint format certutil and security use, not a security decision.
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// cmdRunner runs one external command and returns its combined output. The
// platform backends take it as a field so tests can substitute a fake.
type cmdRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// execRun is the production cmdRunner.
func execRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// certFingerprint returns the SHA-1 thumbprint (lower-case hex) of a
// certificate — the identifier certutil and security address store entries
// by, also used to give each installed anchor a unique file name on Linux.
// Not a cryptographic choice: it is simply the lookup key those tools speak.
func certFingerprint(cert *x509.Certificate) string {
	sum := sha1.Sum(cert.Raw) //nolint:gosec // thumbprint lookup key, see above.
	return hex.EncodeToString(sum[:])
}

// loadTrustAnchor loads the CA *certificate* from dir for the trust-store
// backends. Deliberately certificate-only: a locally-minted CA directory
// also holds a private key, but a CA cached from a remote daemon
// (RemoteDirFor) never does, and installing/uninstalling/querying a trust
// store needs only the public half either way.
//
// Reports absent=true (and no error) when the directory holds no
// certificate — nothing to install, uninstall, or find in that case.
func loadTrustAnchor(dir string) (cert *x509.Certificate, certPEM []byte, absent bool, err error) {
	certPEM, err = os.ReadFile(filepath.Join(dir, CACertFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, true, nil
		}
		return nil, nil, false, fmt.Errorf("trust: read CA certificate from %s: %w", dir, err)
	}
	cert, err = parsePEMCertificate(certPEM)
	if err != nil {
		return nil, nil, false, fmt.Errorf("trust: parse CA certificate in %s: %w", dir, err)
	}
	return cert, certPEM, false, nil
}

// requireTrustAnchor is loadTrustAnchor for Install, where an absent
// certificate is an error the user can act on rather than a no-op.
func requireTrustAnchor(dir string) (*x509.Certificate, []byte, error) {
	cert, certPEM, absent, err := loadTrustAnchor(dir)
	if err != nil {
		return nil, nil, err
	}
	if absent {
		return nil, nil, fmt.Errorf("trust: no CA certificate at %s — run `overcast https enable` first (add --endpoint for a daemon running elsewhere, e.g. in Docker)", dir)
	}
	return cert, certPEM, nil
}
