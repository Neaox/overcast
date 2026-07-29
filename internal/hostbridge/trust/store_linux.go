//go:build linux

package trust

import (
	"bytes"
	"context"
	"crypto/x509"
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
	// ext is the file extension the flavour expects (it matters:
	// update-ca-certificates only picks up .crt).
	ext string
	// refresh rebuilds the system bundle after dir changes.
	refresh []string
}

// anchors lists the supported trust-store flavours in preference order.
var anchors = []anchor{
	// Debian/Ubuntu/Alpine
	{dir: "/usr/local/share/ca-certificates", ext: ".crt", refresh: []string{"update-ca-certificates"}},
	// Fedora/RHEL/CentOS
	{dir: "/etc/pki/ca-trust/source/anchors", ext: ".pem", refresh: []string{"update-ca-trust", "extract"}},
	// Arch (p11-kit trust source; ca-certificates-utils creates the dir)
	{dir: "/etc/ca-certificates/trust-source/anchors", ext: ".pem", refresh: []string{"trust", "extract-compat"}},
}

// anchorFile returns the basename an installed CA gets in a flavour's
// directory. The fingerprint suffix lets multiple overcast CAs coexist —
// one locally-minted, plus any fetched from remote daemons via --endpoint —
// where a fixed name would silently overwrite whichever was installed last.
func (a anchor) anchorFile(cert *x509.Certificate) string {
	return "overcast-ca-" + certFingerprint(cert)[:12] + a.ext
}

// legacyAnchorFiles are the fixed basenames earlier builds installed under
// (one per extension); Uninstall clears a matching one so an upgrade never
// strands a stale trust anchor.
var legacyAnchorFiles = []string{"overcast-local-ca.crt", "overcast-local-ca.pem"}

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
	cert, certPEM, err := requireTrustAnchor(s.dir)
	if err != nil {
		return err
	}
	a, err := s.anchor()
	if err != nil {
		return err
	}
	dest := filepath.Join(a.dir, a.anchorFile(cert))
	if existing, err := os.ReadFile(dest); err == nil && bytes.Equal(existing, certPEM) {
		s.log.Info("overcast CA already installed", zap.String("path", dest))
		return nil
	}
	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return sudoHint(err, "install")
	}
	if err := os.WriteFile(dest, certPEM, 0o644); err != nil {
		return sudoHint(err, "install")
	}
	if out, err := s.run(ctx, a.refresh[0], a.refresh[1:]...); err != nil {
		return fmt.Errorf("trust: %s failed: %w\n%s", a.refresh[0], err, out)
	}
	s.log.Info("overcast CA installed", zap.String("path", dest))
	return nil
}

func (s *linuxStore) Uninstall(ctx context.Context) error {
	cert, certPEM, absent, err := loadTrustAnchor(s.dir)
	if err != nil || absent {
		return err
	}
	a, err := s.anchor()
	if err != nil {
		return err
	}
	removed := false
	if ok, err := removeAnchorFile(filepath.Join(a.dir, a.anchorFile(cert)), nil); err != nil {
		return err
	} else if ok {
		removed = true
	}
	// Earlier builds used a fixed basename; clear it too when it holds this
	// same CA, so upgrades never strand a stale anchor.
	for _, legacy := range legacyAnchorFiles {
		if ok, err := removeAnchorFile(filepath.Join(a.dir, legacy), certPEM); err != nil {
			return err
		} else if ok {
			removed = true
		}
	}
	if !removed {
		return nil
	}
	if out, err := s.run(ctx, a.refresh[0], a.refresh[1:]...); err != nil {
		return fmt.Errorf("trust: %s failed: %w\n%s", a.refresh[0], err, out)
	}
	s.log.Info("overcast CA removed", zap.String("dir", a.dir))
	return nil
}

// removeAnchorFile removes path. When wantContent is non-nil the file is only
// removed if its content matches — the legacy fixed-name file may belong to a
// different overcast CA, which must be left alone. Reports whether a file was
// actually removed; a missing file is not an error.
func removeAnchorFile(path string, wantContent []byte) (bool, error) {
	if wantContent != nil {
		existing, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(existing, wantContent) {
			return false, nil
		}
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, sudoHint(err, "uninstall")
	}
	return true, nil
}

func (s *linuxStore) Installed(_ context.Context) (bool, error) {
	cert, certPEM, absent, err := loadTrustAnchor(s.dir)
	if err != nil || absent {
		return false, err
	}
	a, err := s.anchor()
	if err != nil {
		return false, err
	}
	existing, err := os.ReadFile(filepath.Join(a.dir, a.anchorFile(cert)))
	if err == nil && bytes.Equal(existing, certPEM) {
		return true, nil
	}
	// An anchor installed by an earlier build under the fixed name still
	// counts as installed — same CA, same trust effect.
	for _, legacy := range legacyAnchorFiles {
		if existing, err := os.ReadFile(filepath.Join(a.dir, legacy)); err == nil && bytes.Equal(existing, certPEM) {
			return true, nil
		}
	}
	return false, nil
}

// sudoHint wraps a permission error with the command the user actually needs.
func sudoHint(err error, verb string) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("trust: %w — the system CA bundle needs root: sudo overcast trust %s", err, verb)
	}
	return fmt.Errorf("trust: %w", err)
}
