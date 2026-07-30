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
			{dir: anchorDir, file: "overcast-local-ca.crt", refresh: []string{"update-ca-certificates"}},
		},
	}
	return s, fake, filepath.Join(anchorDir, "overcast-local-ca.crt")
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
			{dir: filepath.Join(anchorDir, "missing-debian"), file: "overcast-local-ca.crt", refresh: []string{"update-ca-certificates"}},
			{dir: anchorDir, file: "overcast-local-ca.pem", refresh: []string{"trust", "extract-compat"}},
		},
	}

	// When: the CA is installed
	if err := s.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Then: the anchor lands in the p11-kit dir and the p11-kit refresh ran
	if _, err := os.Stat(filepath.Join(anchorDir, "overcast-local-ca.pem")); err != nil {
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
			{dir: filepath.Join(existing, "nope"), file: "a.crt", refresh: []string{"sh"}},
			{dir: existing, file: "b.pem", refresh: []string{"definitely-not-a-real-command"}},
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

func TestLinuxStore_installUninstallRoundTrip(t *testing.T) {
	// Given: a fresh store with no CA yet
	s, fake, dest := newTestLinuxStore(t)
	ctx := context.Background()

	// Then: nothing is installed before any CA exists, and Uninstall is a no-op
	if ok, err := s.Installed(ctx); err != nil || ok {
		t.Fatalf("Installed before any CA = (%v, %v), want (false, nil)", ok, err)
	}
	if err := s.Uninstall(ctx); err != nil {
		t.Fatalf("Uninstall with no CA: %v", err)
	}

	// When: the CA is installed
	if err := s.Install(ctx); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Then: the anchor file matches the CA and the bundle was refreshed
	ca, err := loadCA(s.dir)
	if err != nil {
		t.Fatalf("loadCA after Install: %v", err)
	}
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
