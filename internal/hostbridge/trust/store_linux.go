//go:build linux

package trust

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// anchor describes one Linux system trust-store flavour: where CA
// certificates are dropped and which command rebuilds the bundle.
type anchor struct {
	// dir is the directory the distribution scans for extra CAs.
	dir string
	// file is the basename Overcast writes its CA under (the extension
	// matters: update-ca-certificates only picks up .crt).
	file string
	// refresh rebuilds the system bundle after dir changes.
	refresh []string
}

// anchors lists the supported trust-store flavours in preference order.
var anchors = []anchor{
	// Debian/Ubuntu/Alpine
	{dir: "/usr/local/share/ca-certificates", file: "overcast-local-ca.crt", refresh: []string{"update-ca-certificates"}},
	// Fedora/RHEL/CentOS
	{dir: "/etc/pki/ca-trust/source/anchors", file: "overcast-local-ca.pem", refresh: []string{"update-ca-trust", "extract"}},
	// Arch (p11-kit trust source; ca-certificates-utils creates the dir)
	{dir: "/etc/ca-certificates/trust-source/anchors", file: "overcast-local-ca.pem", refresh: []string{"trust", "extract-compat"}},
}

// linuxStore manages the system CA bundle. Writing under /usr/local or /etc
// needs root, so Install/Uninstall return a sudo hint on permission errors.
//
// Note: Firefox and Chromium on Linux read the NSS user database rather than
// the system bundle; installing there requires certutil from libnss3-tools
// and is documented in docs/https.md rather than automated here.
type linuxStore struct {
	dir     string
	log     *zap.Logger
	run     cmdRunner
	anchors []anchor
}

func newStore(log *zap.Logger, caDir string) (Store, error) {
	return &linuxStore{dir: caDir, log: log, run: execRun, anchors: anchors}, nil
}

// anchor picks the trust-store flavour for this system: first by anchor
// directory existence — the reliable distro signal — then, only if no known
// directory exists, by refresh-command presence. Two separate passes matter:
// Arch ships an `update-ca-trust` compat shim, so command-presence alone
// would match the Fedora/RHEL entry there and write to a directory Arch's
// p11-kit never scans.
func (s *linuxStore) anchor() (anchor, error) {
	for _, a := range s.anchors {
		if _, err := os.Stat(a.dir); err == nil {
			return a, nil
		}
	}
	for _, a := range s.anchors {
		if _, err := exec.LookPath(a.refresh[0]); err == nil {
			return a, nil
		}
	}
	dirs := make([]string, len(s.anchors))
	for i, a := range s.anchors {
		dirs[i] = a.dir
	}
	return anchor{}, fmt.Errorf("%w: no known system CA directory found (looked for %s)",
		ErrUnsupported, strings.Join(dirs, ", "))
}

func (s *linuxStore) Install(ctx context.Context) error {
	ca, err := LoadOrCreateCA(s.dir)
	if err != nil {
		return err
	}
	a, err := s.anchor()
	if err != nil {
		return err
	}
	dest := filepath.Join(a.dir, a.file)
	if existing, err := os.ReadFile(dest); err == nil && bytes.Equal(existing, ca.CertPEM) {
		s.log.Info("overcast CA already installed", zap.String("path", dest))
		return nil
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return sudoHint(err, "install")
	}
	if err := os.WriteFile(dest, ca.CertPEM, 0o644); err != nil {
		return sudoHint(err, "install")
	}
	if out, err := s.run(ctx, a.refresh[0], a.refresh[1:]...); err != nil {
		return fmt.Errorf("trust: %s failed: %w\n%s", a.refresh[0], err, out)
	}
	s.log.Info("overcast CA installed", zap.String("path", dest))
	return nil
}

func (s *linuxStore) Uninstall(ctx context.Context) error {
	_, absent, err := loadInstalledCA(s.dir)
	if err != nil || absent {
		return err
	}
	a, err := s.anchor()
	if err != nil {
		return err
	}
	dest := filepath.Join(a.dir, a.file)
	if err := os.Remove(dest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return sudoHint(err, "uninstall")
	}
	if out, err := s.run(ctx, a.refresh[0], a.refresh[1:]...); err != nil {
		return fmt.Errorf("trust: %s failed: %w\n%s", a.refresh[0], err, out)
	}
	s.log.Info("overcast CA removed", zap.String("path", dest))
	return nil
}

func (s *linuxStore) Installed(_ context.Context) (bool, error) {
	ca, absent, err := loadInstalledCA(s.dir)
	if err != nil || absent {
		return false, err
	}
	a, err := s.anchor()
	if err != nil {
		return false, err
	}
	existing, err := os.ReadFile(filepath.Join(a.dir, a.file))
	if err != nil {
		return false, nil //nolint:nilerr // unreadable/absent anchor = not installed.
	}
	return bytes.Equal(existing, ca.CertPEM), nil
}

// sudoHint wraps a permission error with the command the user actually needs.
func sudoHint(err error, verb string) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("trust: %w — the system CA bundle needs root: sudo overcast trust %s", err, verb)
	}
	return fmt.Errorf("trust: %w", err)
}
