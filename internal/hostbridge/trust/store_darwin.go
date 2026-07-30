//go:build darwin

package trust

import (
	"context"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// darwinStore manages the user's login keychain via the `security` tool,
// which ships with macOS. Targeting the login keychain (not the System
// keychain) means no sudo — macOS asks the user to authorise the trust
// change with its own dialog.
type darwinStore struct {
	dir      string
	keychain string
	log      *zap.Logger
	run      cmdRunner
}

func newStore(log *zap.Logger, caDir string) (Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("trust: resolve home directory: %w", err)
	}
	return &darwinStore{
		dir:      caDir,
		keychain: filepath.Join(home, "Library", "Keychains", "login.keychain-db"),
		log:      log,
		run:      execRun,
	}, nil
}

func (s *darwinStore) Install(ctx context.Context) error {
	cert, _, err := requireTrustAnchor(s.dir)
	if err != nil {
		return err
	}
	if installed, err := s.installed(ctx, cert); err != nil {
		return err
	} else if installed {
		s.log.Info("overcast CA already installed", zap.String("keychain", s.keychain))
		return nil
	}
	certPath := filepath.Join(s.dir, CACertFile)
	out, err := s.run(ctx, "security", "add-trusted-cert", "-r", "trustRoot", "-k", s.keychain, certPath)
	if err != nil {
		return fmt.Errorf("trust: security add-trusted-cert failed (the authorisation dialog may have been declined): %w\n%s", err, out)
	}
	s.log.Info("overcast CA installed", zap.String("keychain", s.keychain), zap.String("cert", certPath))
	return nil
}

func (s *darwinStore) Uninstall(ctx context.Context) error {
	cert, _, absent, err := loadTrustAnchor(s.dir)
	if err != nil || absent {
		return err
	}
	if installed, err := s.installed(ctx, cert); err != nil {
		return err
	} else if !installed {
		return nil
	}
	out, err := s.run(ctx, "security", "delete-certificate", "-Z", certFingerprint(cert), s.keychain)
	if err != nil {
		return fmt.Errorf("trust: security delete-certificate failed: %w\n%s", err, out)
	}
	s.log.Info("overcast CA removed", zap.String("keychain", s.keychain))
	return nil
}

func (s *darwinStore) Installed(ctx context.Context) (bool, error) {
	cert, _, absent, err := loadTrustAnchor(s.dir)
	if err != nil || absent {
		return false, err
	}
	return s.installed(ctx, cert)
}

// installed matches the CA by SHA-1 hash in the keychain listing. `security
// find-certificate` exits non-zero when nothing matches the common name,
// which simply means "not installed".
func (s *darwinStore) installed(ctx context.Context, cert *x509.Certificate) (bool, error) {
	out, err := s.run(ctx, "security", "find-certificate", "-a", "-c", cert.Subject.CommonName, "-Z", s.keychain)
	if err != nil {
		return false, nil //nolint:nilerr // non-zero exit = no matching certificate.
	}
	return strings.Contains(strings.ToLower(string(out)), certFingerprint(cert)), nil
}
