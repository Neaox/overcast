package lambda

// The in-container init the acquire path embeds (initbin) is gitignored build
// output — `make lambda-init` — so a fresh checkout has none, and every unit
// test that walks the acquire path as far as resolveInitDelivery would fail
// with initbin's "run `make lambda-init`" error before it reached the thing it
// tests. CI builds the artefacts in a step before `go test`; a bare checkout
// has no such step, and AGENTS.md's contract is that a bare checkout builds,
// vets AND tests.
//
// Building them here is necessary but not sufficient: the embed was baked when
// this test binary was compiled, which on a fresh checkout was before the
// artefacts existed, and nothing built mid-run can change an embed. That is
// what initbin.EnvDistDir exists for — point it at dist/ and this very run
// reads what was just built. tests/integration/lambda's requireLambdaInit does
// the same for the Docker-gated tests.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Neaox/overcast/internal/services/lambda/initbin"
)

func TestMain(m *testing.M) {
	if dist, err := buildMissingInitArtefacts(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not build the in-container Lambda init (tests that acquire containers will fail with initbin's error): %v\n", err)
	} else {
		os.Setenv(initbin.EnvDistDir, dist)
	}
	os.Exit(m.Run())
}

// buildMissingInitArtefacts builds any absent init artefact with the same
// flags as the Makefile's lambda-init target, and returns the dist directory.
func buildMissingInitArtefacts() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate this test's own source file")
	}
	root := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", fmt.Errorf("no go.mod above %s", filepath.Dir(file))
		}
		root = parent
	}
	dist := filepath.Join(root, "internal", "services", "lambda", "initbin", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return "", err
	}
	for _, goarch := range []string{"amd64", "arm64"} {
		out := filepath.Join(dist, "lambda-init-linux-"+goarch)
		if info, err := os.Stat(out); err == nil && info.Size() > 0 {
			continue
		}
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w", "-o", out, "./cmd/lambda-init")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch)
		if combined, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("go build ./cmd/lambda-init for linux/%s: %v: %s", goarch, err, combined)
		}
	}
	return dist, nil
}
