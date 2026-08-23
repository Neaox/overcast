package initbin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestGoarchForLambdaArchitecture(t *testing.T) {
	tests := []struct {
		arch string
		want string
		ok   bool
	}{
		{arch: "x86_64", want: "amd64", ok: true},
		{arch: "arm64", want: "arm64", ok: true},
		// Lambda's default when Architectures is unset is x86_64, and
		// dockerPlatformForLambdaArchitectures picks linux/amd64 for it.
		{arch: "", want: "amd64", ok: true},
		{arch: "amd64", ok: false},
		{arch: "X86_64", ok: false},
		{arch: "aarch64", ok: false},
	}
	for _, tc := range tests {
		got, ok := goarchFor(tc.arch)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("goarchFor(%q) = (%q, %v), want (%q, %v)", tc.arch, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLoadReturnsTheEmbeddedBinary(t *testing.T) {
	fsys := fstest.MapFS{
		"dist/lambda-init-linux-amd64": {Data: []byte("amd64-binary")},
		"dist/lambda-init-linux-arm64": {Data: []byte("arm64-binary")},
	}
	for arch, want := range map[string]string{"x86_64": "amd64-binary", "arm64": "arm64-binary", "": "amd64-binary"} {
		got, err := load(fsys, arch)
		if err != nil {
			t.Fatalf("load(%q): %v", arch, err)
		}
		if string(got) != want {
			t.Fatalf("load(%q) = %q, want %q", arch, got, want)
		}
	}
	for _, arch := range []string{"x86_64", "arm64"} {
		if !present(fsys, arch) {
			t.Errorf("present(%q) = false, want true", arch)
		}
	}
}

func TestLoadNamesTheMissingArtefactAndHowToBuildIt(t *testing.T) {
	// What a bare checkout looks like: the committed placeholder, no binaries.
	fsys := fstest.MapFS{"dist/.gitkeep": {Data: nil}}

	_, err := load(fsys, "arm64")
	if err == nil {
		t.Fatal("load succeeded with no embedded artefact")
	}
	msg := err.Error()
	for _, want := range []string{
		"arm64",
		"internal/services/lambda/initbin/dist/lambda-init-linux-arm64",
		"make lambda-init",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	if present(fsys, "arm64") {
		t.Error("present reported a missing artefact as present")
	}
}

func TestLoadRejectsAnUnsupportedArchitecture(t *testing.T) {
	fsys := fstest.MapFS{"dist/lambda-init-linux-amd64": {Data: []byte("x")}}

	_, err := load(fsys, "riscv64")
	if err == nil {
		t.Fatal("load accepted an architecture Lambda does not have")
	}
	msg := err.Error()
	for _, want := range []string{"riscv64", "x86_64", "arm64"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	if present(fsys, "riscv64") {
		t.Error("present reported an unsupported architecture as present")
	}
}

// maxInitBytes is the size budget for one architecture's init. Every function
// container pays for it twice over: once in the Overcast binary (both
// architectures are embedded) and once per cold start, in the provisioning
// archive that is copied in.
//
// Measured 2026-08-23 with `-trimpath -ldflags "-s -w"`, Go 1.26: 6.72 MB for
// amd64 and 6.23 MB for arm64. A bare `net/http` + `net/http/httputil` binary
// is 5.72 MB of that, so the floor belongs to the Runtime API proxy and is not
// something a smaller init could avoid. The budget is set above the measurement
// with room for ordinary growth: it exists to catch a dependency that drags in
// something large, not to police kilobytes.
const maxInitBytes = 8 << 20

func TestEmbeddedInitStaysWithinItsSizeBudget(t *testing.T) {
	for _, arch := range []string{"x86_64", "arm64"} {
		b, err := For(arch)
		if err != nil {
			t.Skipf("no init artefact embedded in this build: %v", err)
		}
		if len(b) > maxInitBytes {
			t.Errorf("the %s init is %d bytes, over the %d-byte budget — see the comment on maxInitBytes", arch, len(b), maxInitBytes)
		}
	}
}

// The embed pattern must resolve against the committed tree — with or without
// the (gitignored) build output. This is the test that fails if the .gitkeep
// placeholder is ever deleted.
func TestPackageEmbedResolves(t *testing.T) {
	entries, err := dist.ReadDir("dist")
	if err != nil {
		t.Fatalf("the embedded dist directory does not resolve: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the embedded dist directory is empty; the .gitkeep placeholder must be committed")
	}

	// Whatever this build embedded, the exported surface must agree with it.
	for _, arch := range []string{"x86_64", "arm64"} {
		_, err := For(arch)
		if got, want := err == nil, Present(arch); got != want {
			t.Errorf("For(%q) succeeded=%v but Present(%q)=%v", arch, got, arch, want)
		}
	}
}

// TestEnvDistDirOverridesTheEmbed pins the seam the repository's test
// bootstraps depend on: an embed is baked at compile time, so a test binary
// compiled on a bare checkout — before `make lambda-init` ever ran — can only
// use artefacts built mid-run through this override. It must win over the
// embed, and it must fail with its own actionable error when it points
// somewhere useless, rather than falling back silently.
func TestEnvDistDirOverridesTheEmbed(t *testing.T) {
	dir := t.TempDir()
	want := []byte("not-a-real-init-but-bytes-all-the-same")
	if err := os.WriteFile(filepath.Join(dir, "lambda-init-linux-amd64"), want, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDistDir, dir)

	got, err := For("x86_64")
	if err != nil {
		t.Fatalf("For with %s set: %v", EnvDistDir, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("For read %d bytes, want the %d written to the override directory", len(got), len(want))
	}

	// And: an override pointing at a directory without the artefact is its own
	// loud error naming the variable, never a silent fall-through to the embed.
	t.Setenv(EnvDistDir, t.TempDir())
	if _, err := For("x86_64"); err == nil || !strings.Contains(err.Error(), EnvDistDir) {
		t.Fatalf("For with a useless override = %v, want an error naming %s", err, EnvDistDir)
	}
}
