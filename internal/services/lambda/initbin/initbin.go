// Package initbin carries the in-container Lambda init binary, built for each
// Linux architecture Lambda offers and embedded in the Overcast binary.
//
// Embedding rather than publishing an image: no registry artefact to pull, it
// works offline, there is no version skew between a host and its init, and
// native-mode Overcast on Windows or macOS needs no host-visible file to bind
// mount. The cost is roughly 5 MB on every Overcast binary — less than the SPA.
// See docs/plans/lambda-in-container-init.md § 3.2.
//
// The artefacts are build output and are NOT committed: dist/ holds a
// committed .gitkeep so the embed pattern always resolves and a bare `git
// clone` still builds, vets and tests, exactly as web/dist does for the SPA.
// Build them with `make lambda-init` (or `task lambda-init`). A build without
// them is a working Overcast that fails loudly — see [For] — the first time a
// function needs an execution environment.
package initbin

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// dist holds the built init binaries. `all:` so the committed .gitkeep is
// embedded too: without it the pattern matches nothing in a bare checkout and
// the package fails to compile.
//
//go:embed all:dist
var dist embed.FS

// distDirRel is where the artefacts live in the repository. It is in the error
// message, so it must stay in step with the Makefile's lambda-init target.
const distDirRel = "internal/services/lambda/initbin/dist"

// EnvDistDir names a directory to read the artefacts from INSTEAD of the
// embedded copies. Two audiences, neither of them production:
//
//   - The repository's own test binaries. An embed is baked at compile time,
//     so a test binary compiled from a bare checkout — before `make
//     lambda-init` has ever run — carries no init, and nothing it builds
//     mid-run can change that. The test bootstraps (this package's users have
//     one each: internal/services/lambda's TestMain and
//     tests/integration/lambda's requireLambdaInit) build the artefacts and
//     point this variable at dist/, so the FIRST `go test` on a fresh clone
//     passes rather than the second.
//   - A developer iterating on the init itself, who wants a container to run a
//     freshly built copy without relinking Overcast.
//
// When unset — every production deployment — the embedded copies are the only
// source, and a build without them fails loudly as documented on For.
const EnvDistDir = "OVERCAST_LAMBDA_INIT_DIST"

// goarchFor maps a Lambda Architectures value to the GOARCH the init was built
// for. It mirrors dockerPlatformForLambdaArchitectures in the lambda package —
// the function's architecture selects the image platform and the init together,
// so the two must never disagree. An empty value means "unset", which Lambda
// defaults to x86_64.
func goarchFor(arch string) (string, bool) {
	switch arch {
	case "", "x86_64":
		return "amd64", true
	case "arm64":
		return "arm64", true
	default:
		return "", false
	}
}

// artefactName is the file name `make lambda-init` writes into dist/.
func artefactName(goarch string) string { return "lambda-init-linux-" + goarch }

// For returns the init binary for a Lambda Architectures value ("x86_64" or
// "arm64"; empty means x86_64).
//
// When this build has no such artefact the error names the file that is
// missing and the command that produces it. It is deliberately loud: a silent
// fallback would mean an execution environment without an init, which is a
// container whose logs cannot be attributed to an invocation — the exact
// failure this whole mechanism exists to remove.
func For(arch string) ([]byte, error) { return load(dist, arch) }

// Present reports whether this build can serve an init for arch. It lets a
// caller fail at function create, where the user is looking, rather than at
// the first invoke.
func Present(arch string) bool { return present(dist, arch) }

func load(fsys fs.FS, arch string) ([]byte, error) {
	goarch, ok := goarchFor(arch)
	if !ok {
		return nil, fmt.Errorf("no Lambda init for architecture %q: Lambda functions are %q or %q", arch, "x86_64", "arm64")
	}
	name := artefactName(goarch)
	if dir := os.Getenv(EnvDistDir); dir != "" {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("%s points at %s, but %s is not readable there: %w", EnvDistDir, dir, name, err)
		}
		if len(b) == 0 {
			return nil, fmt.Errorf("%s points at %s, but %s is empty there", EnvDistDir, dir, name)
		}
		return b, nil
	}
	b, err := fs.ReadFile(fsys, "dist/"+name)
	if err != nil {
		return nil, fmt.Errorf("this overcast build has no Lambda init for linux/%s: %s/%s is missing — run `make lambda-init` (or `task lambda-init`) and rebuild", goarch, distDirRel, name)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("this overcast build's Lambda init for linux/%s is empty: %s/%s — run `make lambda-init` (or `task lambda-init`) and rebuild", goarch, distDirRel, name)
	}
	return b, nil
}

func present(fsys fs.FS, arch string) bool {
	_, err := load(fsys, arch)
	return err == nil
}
