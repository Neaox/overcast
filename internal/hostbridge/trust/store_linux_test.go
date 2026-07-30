//go:build linux

package trust

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

// fakeRunner records refresh-command invocations instead of executing them.
type fakeRunner struct {
	calls [][]string
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return nil, nil
}

// newTestLinuxStore builds a linuxStore pointed at a temp CA dir and a temp
// anchor dir, with the refresh command faked out.
func newTestLinuxStore(t *testing.T) (*linuxStore, *fakeRunner, string) {
	t.Helper()
	anchorDir := t.TempDir()
	fake := &fakeRunner{}
	s := &linuxStore{
		dir: t.TempDir(),
		log: zap.NewNop(),
		run: fake.run,
		anchors: []anchor{
			{dir: anchorDir, ext: ".crt", refresh: []string{"update-ca-certificates"}},
		},
	}
	return s, fake, anchorDir
}

func TestLinuxStore_installRequiresExistingCA(t *testing.T) {
	// Given: a store whose CA directory is empty
	s, _, _ := newTestLinuxStore(t)
	ctx := context.Background()

	// Then: nothing is installed, Uninstall is a no-op, and Install refuses
	// rather than silently minting key material (creation is the CLI's job)
	if ok, err := s.Installed(ctx); err != nil || ok {
		t.Fatalf("Installed before any CA = (%v, %v), want (false, nil)", ok, err)
	}
	if err := s.Uninstall(ctx); err != nil {
		t.Fatalf("Uninstall with no CA: %v", err)
	}
	if err := s.Install(ctx); err == nil {
		t.Error("Install with no CA certificate should error, got nil")
	}
}

func TestLinuxStore_installUninstallRoundTrip(t *testing.T) {
	// Given: a locally-minted CA
	s, fake, anchorDir := newTestLinuxStore(t)
	ctx := context.Background()
	ca, err := LoadOrCreateCA(s.dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	dest := filepath.Join(anchorDir, s.anchors[0].anchorFile(ca.Cert))

	// When: the CA is installed
	if err := s.Install(ctx); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Then: the fingerprint-named anchor matches the CA and the bundle was
	// refreshed
	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("anchor file not written: %v", err)
	}
	if string(written) != string(ca.CertPEM) {
		t.Error("anchor file does not match the CA certificate")
	}
	if len(fake.calls) != 1 || fake.calls[0][0] != "update-ca-certificates" {
		t.Errorf("expected one update-ca-certificates call, got %v", fake.calls)
	}
	if ok, err := s.Installed(ctx); err != nil || !ok {
		t.Errorf("Installed after Install = (%v, %v), want (true, nil)", ok, err)
	}

	// And: a second Install is an idempotent no-op (no second refresh)
	if err := s.Install(ctx); err != nil {
		t.Fatalf("Install (repeat): %v", err)
	}
	if len(fake.calls) != 1 {
		t.Errorf("repeat Install re-ran the refresh command: %v", fake.calls)
	}

	// When: the CA is uninstalled
	if err := s.Uninstall(ctx); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// Then: the anchor file is gone, the bundle was refreshed again, and the
	// CA key material survives for a later re-install
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("anchor file still present after Uninstall: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Errorf("expected a refresh after Uninstall, got %v", fake.calls)
	}
	if _, err := os.Stat(filepath.Join(s.dir, caKeyFile)); err != nil {
		t.Errorf("CA key material was deleted by Uninstall: %v", err)
	}
	if ok, _ := s.Installed(ctx); ok {
		t.Error("Installed still true after Uninstall")
	}
}

// TestLinuxStore_remoteCAWithoutKey pins the Docker flow: a CA cached from a
// remote daemon has only the certificate, and install/uninstall must work
// off that alone.
func TestLinuxStore_remoteCAWithoutKey(t *testing.T) {
	// Given: a remote daemon's CA certificate cached locally (no key)
	s, _, anchorDir := newTestLinuxStore(t)
	ctx := context.Background()
	daemonCA, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateCA (daemon): %v", err)
	}
	if err := SaveRemoteCA(s.dir, daemonCA.CertPEM); err != nil {
		t.Fatalf("SaveRemoteCA: %v", err)
	}

	// When: the cached CA is installed and later uninstalled
	if err := s.Install(ctx); err != nil {
		t.Fatalf("Install (remote CA): %v", err)
	}
	dest := filepath.Join(anchorDir, s.anchors[0].anchorFile(daemonCA.Cert))
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("anchor file not written for remote CA: %v", err)
	}
	if ok, err := s.Installed(ctx); err != nil || !ok {
		t.Errorf("Installed(remote CA) = (%v, %v), want (true, nil)", ok, err)
	}
	if err := s.Uninstall(ctx); err != nil {
		t.Fatalf("Uninstall (remote CA): %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("anchor file still present after Uninstall: %v", err)
	}
}

// TestLinuxStore_distinctCAsCoexist verifies the fingerprint-named anchors:
// a local CA and a remote daemon's CA install side by side, and removing one
// leaves the other trusted.
func TestLinuxStore_distinctCAsCoexist(t *testing.T) {
	anchorDir := t.TempDir()
	fake := &fakeRunner{}
	flavour := anchor{dir: anchorDir, ext: ".crt", refresh: []string{"update-ca-certificates"}}
	newStoreFor := func(caDir string) *linuxStore {
		return &linuxStore{dir: caDir, log: zap.NewNop(), run: fake.run, anchors: []anchor{flavour}}
	}
	ctx := context.Background()

	// Given: a local CA and a (distinct) remote daemon CA
	local := newStoreFor(t.TempDir())
	if _, err := LoadOrCreateCA(local.dir); err != nil {
		t.Fatalf("LoadOrCreateCA (local): %v", err)
	}
	remoteCA, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateCA (remote): %v", err)
	}
	remote := newStoreFor(t.TempDir())
	if err := SaveRemoteCA(remote.dir, remoteCA.CertPEM); err != nil {
		t.Fatalf("SaveRemoteCA: %v", err)
	}

	// When: both are installed
	if err := local.Install(ctx); err != nil {
		t.Fatalf("Install (local): %v", err)
	}
	if err := remote.Install(ctx); err != nil {
		t.Fatalf("Install (remote): %v", err)
	}

	// Then: both report installed simultaneously
	if ok, _ := local.Installed(ctx); !ok {
		t.Error("local CA not installed after installing both")
	}
	if ok, _ := remote.Installed(ctx); !ok {
		t.Error("remote CA not installed after installing both")
	}

	// And: uninstalling one leaves the other alone
	if err := remote.Uninstall(ctx); err != nil {
		t.Fatalf("Uninstall (remote): %v", err)
	}
	if ok, _ := local.Installed(ctx); !ok {
		t.Error("uninstalling the remote CA removed the local CA's anchor")
	}
	if ok, _ := remote.Installed(ctx); ok {
		t.Error("remote CA still installed after Uninstall")
	}
}

// TestLinuxStore_uninstallClearsLegacyFixedName verifies an anchor installed
// by an earlier build under the fixed basename is recognised and removed.
func TestLinuxStore_uninstallClearsLegacyFixedName(t *testing.T) {
	// Given: this CA installed under the legacy fixed name only
	s, fake, anchorDir := newTestLinuxStore(t)
	ctx := context.Background()
	ca, err := LoadOrCreateCA(s.dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	legacy := filepath.Join(anchorDir, "overcast-local-ca.crt")
	if err := os.WriteFile(legacy, ca.CertPEM, 0o644); err != nil {
		t.Fatalf("write legacy anchor: %v", err)
	}

	// Then: it still counts as installed
	if ok, err := s.Installed(ctx); err != nil || !ok {
		t.Errorf("Installed(legacy anchor) = (%v, %v), want (true, nil)", ok, err)
	}

	// When: uninstalled
	if err := s.Uninstall(ctx); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	// Then: the legacy file is gone and the bundle refreshed
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy anchor still present after Uninstall: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Errorf("expected one refresh call, got %v", fake.calls)
	}
}

// TestLinuxStore_archTrustSourceFlavour covers the p11-kit flavour (Arch):
// install writes into the trust-source anchors dir and refreshes with
// `trust extract-compat`.
func TestLinuxStore_archTrustSourceFlavour(t *testing.T) {
	// Given: a system whose only known anchor directory is the p11-kit one
	anchorDir := t.TempDir()
	fake := &fakeRunner{}
	s := &linuxStore{
		dir: t.TempDir(),
		log: zap.NewNop(),
		run: fake.run,
		anchors: []anchor{
			{dir: filepath.Join(anchorDir, "missing-debian"), ext: ".crt", refresh: []string{"update-ca-certificates"}},
			{dir: anchorDir, ext: ".pem", refresh: []string{"trust", "extract-compat"}},
		},
	}
	ca, err := LoadOrCreateCA(s.dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}

	// When: the CA is installed
	if err := s.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Then: the anchor lands in the p11-kit dir (with its .pem extension)
	// and the p11-kit refresh ran
	if _, err := os.Stat(filepath.Join(anchorDir, s.anchors[1].anchorFile(ca.Cert))); err != nil {
		t.Errorf("anchor file not written to the trust-source dir: %v", err)
	}
	want := []string{"trust", "extract-compat"}
	if len(fake.calls) != 1 || fake.calls[0][0] != want[0] || fake.calls[0][1] != want[1] {
		t.Errorf("refresh calls = %v, want one %v", fake.calls, want)
	}
}

// TestLinuxStore_anchorPrefersExistingDirOverCommand pins the two-pass
// selection rule: an anchor whose directory exists wins over an earlier one
// whose directory is missing, regardless of which refresh binaries happen to
// be on PATH. (Arch ships an `update-ca-trust` compat shim, so a single
// dir-or-command pass would pick the Fedora entry there and write to a
// directory Arch's p11-kit never scans.)
func TestLinuxStore_anchorPrefersExistingDirOverCommand(t *testing.T) {
	// Given: the first flavour's dir is missing (its refresh command, `sh`,
	// certainly exists on PATH), the second flavour's dir exists
	existing := t.TempDir()
	s := &linuxStore{
		dir: t.TempDir(),
		log: zap.NewNop(),
		run: (&fakeRunner{}).run,
		anchors: []anchor{
			{dir: filepath.Join(existing, "nope"), ext: ".crt", refresh: []string{"sh"}},
			{dir: existing, ext: ".pem", refresh: []string{"definitely-not-a-real-command"}},
		},
	}

	// When: the flavour is selected
	a, err := s.anchor()
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}

	// Then: directory existence beats command presence
	if a.dir != existing {
		t.Errorf("anchor() picked %q, want the flavour whose dir exists (%q)", a.dir, existing)
	}
}
