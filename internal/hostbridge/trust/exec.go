package trust

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 is the thumbprint format certutil and security use, not a security decision.
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// cmdRunner runs one external command and returns its combined output. The
// platform backends take it as a field so tests can substitute a fake.
type cmdRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// execRun is the production cmdRunner.
func execRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// caFingerprint returns the SHA-1 thumbprint (lower-case hex) of the CA
// certificate — the identifier certutil and security address store entries
// by. Not a cryptographic choice: it is simply the lookup key format those
// tools speak.
//
//nolint:unused // used by the windows and darwin trust-store backends; lint runs on linux.
func caFingerprint(ca *CA) string {
	sum := sha1.Sum(ca.Cert.Raw) //nolint:gosec // thumbprint lookup key, see above.
	return hex.EncodeToString(sum[:])
}

// loadInstalledCA loads the CA from dir for Installed/Uninstall checks.
// Reports absent=true (and no error) when no CA has ever been created —
// nothing can be installed in that case.
func loadInstalledCA(dir string) (ca *CA, absent bool, err error) {
	ca, err = loadCA(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("trust: load CA from %s: %w", dir, err)
	}
	return ca, false, nil
}
