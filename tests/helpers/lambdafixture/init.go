package lambdafixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/overcast-sh/overcast/internal/services/lambda/initbin"
)

// ─── the in-container init ───────────────────────────────────────────────────

var lambdaInitOnce struct {
	sync.Once
	err error
}

// EnsureInit makes sure this checkout has the in-container init built for both
// Linux architectures, building them if it does not, and points
// initbin.EnvDistDir at the result. The work happens once per process however
// many times this is called.
//
// The artefacts are gitignored build output (`make lambda-init`), so a fresh
// clone has none and every Docker-gated Lambda test would fail its cold start
// with initbin's "run `make lambda-init`" error. Building them here — once per
// test binary, with the same flags the Makefile uses — is what keeps `go test`
// working without a separate build step, including inside scripts/docker-go.sh.
//
// It lives here rather than in tests/integration/lambdadocker so that any test
// package needing a real init gets the same bootstrap: the embed is baked at
// compile time, so each test binary that starts containers must do this for
// itself, and one implementation is the only way that stays true.
//
// Call it from TestMain rather than from a test, so that no test depends on
// which file sorted first — see RequireInit.
func EnsureInit() error {
	lambdaInitOnce.Do(func() {
		dist, err := buildLambdaInit()
		if err == nil {
			// The embed in THIS test binary was baked at compile time, which
			// on a fresh checkout was before the artefacts existed; the env
			// override is what lets this very run use what was just built.
			// See initbin.EnvDistDir.
			os.Setenv(initbin.EnvDistDir, dist)
		}
		lambdaInitOnce.err = err
	})
	return lambdaInitOnce.err
}

// RequireInit is EnsureInit with the failure reported against the test that
// needs the init, which is where it is legible.
//
// A test binary that starts containers should also call EnsureInit from its
// TestMain: a test that never calls this one still needs the artefacts, and
// before tests/integration/lambda was split it got them only because
// lambda_init_test.go happened to sort first and left a dist behind. Running
// one of its siblings alone on a fresh checkout — `go test -run
// TestInvoke_nodeRuntime_success` — failed with an "Unhandled" function error
// naming nothing.
func RequireInit(t *testing.T) {
	t.Helper()
	if err := EnsureInit(); err != nil {
		t.Fatalf("the in-container Lambda init could not be built: %v", err)
	}
}

func buildLambdaInit() (string, error) {
	root, err := RepoRoot()
	if err != nil {
		return "", err
	}
	dist := filepath.Join(root, "internal", "services", "lambda", "initbin", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return "", err
	}
	for _, goarch := range []string{"amd64", "arm64"} {
		out := filepath.Join(dist, "lambda-init-linux-"+goarch)
		// Always rebuilt, never trusted from disk. The skip-if-present this
		// used to do meant a dist left by an earlier session silently tested a
		// stale init against current host code — a skew that shows up as
		// telemetry records quietly missing, not as an error naming its cause
		// (it cost the #1410 branch an hour of phantom debugging). Go's build
		// cache makes the honest rebuild cost about a second when nothing
		// changed.
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, "./cmd/lambda-init")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch)
		if combined, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("go build ./cmd/lambda-init for linux/%s: %v: %s", goarch, err, combined)
		}
	}
	return dist, nil
}

// RepoRoot walks up from this file to the module root.
func RepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate this test's own source file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", filepath.Dir(file))
		}
		dir = parent
	}
}
