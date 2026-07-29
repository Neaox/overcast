//go:build windows

package trust

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// winStore manages the current user's Root certificate store via certutil,
// which ships with Windows. Using `-user` scopes everything to the invoking
// user, so no elevation is needed — Windows shows a one-time confirmation
// dialog when a root certificate is added.
type winStore struct {
	dir string
	log *zap.Logger
	run cmdRunner
}

func newStore(log *zap.Logger, caDir string) (Store, error) {
	return &winStore{dir: caDir, log: log, run: execRun}, nil
}

func (s *winStore) Install(ctx context.Context) error {
	ca, err := LoadOrCreateCA(s.dir)
	if err != nil {
		return err
	}
	if installed, err := s.installed(ctx, ca); err != nil {
		return err
	} else if installed {
		s.log.Info("overcast CA already installed", zap.String("store", "CurrentUser\\Root"))
		return nil
	}
	certPath := filepath.Join(s.dir, CACertFile)
	out, err := s.run(ctx, "certutil", "-user", "-addstore", "Root", certPath)
	if err != nil {
		return fmt.Errorf("trust: certutil -addstore failed (the confirmation dialog may have been declined): %w\n%s", err, out)
	}
	s.log.Info("overcast CA installed", zap.String("store", "CurrentUser\\Root"), zap.String("cert", certPath))
	return nil
}

func (s *winStore) Uninstall(ctx context.Context) error {
	ca, absent, err := loadInstalledCA(s.dir)
	if err != nil || absent {
		return err
	}
	if installed, err := s.installed(ctx, ca); err != nil {
		return err
	} else if !installed {
		return nil
	}
	out, err := s.run(ctx, "certutil", "-user", "-delstore", "Root", caFingerprint(ca))
	if err != nil {
		return fmt.Errorf("trust: certutil -delstore failed: %w\n%s", err, out)
	}
	s.log.Info("overcast CA removed", zap.String("store", "CurrentUser\\Root"))
	return nil
}

func (s *winStore) Installed(ctx context.Context) (bool, error) {
	ca, absent, err := loadInstalledCA(s.dir)
	if err != nil || absent {
		return false, err
	}
	return s.installed(ctx, ca)
}

// installed looks the CA up by SHA-1 thumbprint. certutil exits non-zero
// when the entry is absent, so a command error with the usual "not found"
// output means "not installed" rather than a real failure.
func (s *winStore) installed(ctx context.Context, ca *CA) (bool, error) {
	out, err := s.run(ctx, "certutil", "-user", "-store", "Root", caFingerprint(ca))
	if err == nil {
		return true, nil
	}
	if strings.Contains(strings.ToLower(string(out)), "not found") ||
		strings.Contains(string(out), "0x80092004") { // CRYPT_E_NOT_FOUND
		return false, nil
	}
	return false, fmt.Errorf("trust: certutil -store failed: %w\n%s", err, out)
}
