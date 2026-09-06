//go:build dev

package main

import (
	"fmt"
	"go/types"
	"os"
	"slices"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

// Reading the vendored AWS SDK for Go v2's own field types.
//
// The go-sdk backend emits source, so it has to spell each input member the
// way the SDK declares it: `aws.String(...)` where smithy-go made a member a
// pointer, a bare literal where it made it a value, `types.<Enum>(...)` where
// it made it a named string. None of that is derivable from the pinned Smithy
// snapshot. The snapshot and the vendored SDK are generated from different
// revisions of the same AWS model and for the pilot service they already
// disagree: ReceiveMessage's MaxNumberOfMessages, VisibilityTimeout and
// WaitTimeSeconds target NullableInteger in models/aws/shapes/sqs.json — which
// says pointer — and are plain int32 fields in aws-sdk-go-v2/service/sqs.
//
// So the emitter asks the SDK. This file loads
// `github.com/aws/aws-sdk-go-v2/service/<pkg>` from the go-sdk suite module —
// a module of its own, with its own go.mod, which is why every load carries
// that directory — and hands the emitter `<Op>Input`'s fields as go/types
// values. The type-spelling table in emit_go_spell.go turns those into source.
//
// Two properties this is built for:
//
//   - **One load per service per run.** A load type-checks the package from
//     export data, which is fast but not free; a generation run emits many
//     calls per service and must not pay for it more than once.
//   - **Hermetic tests.** The loader takes the module directory as a
//     parameter, so cmd/compatgen's own tests point it at
//     testdata/awssdk — a checked-in stand-in for the SDK, complete with the
//     SDK's module path — and resolve real Go types without the module cache
//     or a network fetch. Only `make generate-compat-model` and
//     `make compat-model-check` read the real thing.

// goSDKModuleDir is the go-sdk suite's module root, repository-relative. Its
// go.mod pins the SDK versions the emitted source is compiled against, so it
// is the only correct place to resolve a field type from.
const goSDKModuleDir = "compat/suites/go-sdk"

// goSDKTypes loads and caches service packages from one module directory.
type goSDKTypes struct {
	dir string

	mu     sync.Mutex
	loaded map[string]*goSDKService
}

// newGoSDKTypes returns a loader that resolves against the module rooted at
// dir.
func newGoSDKTypes(dir string) *goSDKTypes {
	return &goSDKTypes{dir: dir, loaded: map[string]*goSDKService{}}
}

// goSDKService is one service package, type-checked.
type goSDKService struct {
	// Name is the package's own name, which is also how emitted source refers
	// to it: sqs, organizations, widgets.
	Name string
	// Path is its import path, and TypesPath its `types` subpackage — the only
	// two packages a spelled expression may name.
	Path      string
	TypesPath string

	scope *types.Scope
}

// prime loads several services in one go. A load spawns `go list` and makes it
// read the suite module's dependency graph, and that cost is per invocation
// rather than per package — so loading a generation run's services together is
// most of the difference between the emitter costing a fraction of a second
// and costing one per service.
//
// It is an optimisation, not a precondition: service loads anything prime was
// not told about.
func (l *goSDKTypes) prime(sdkIDs []string) error {
	var paths []string
	l.mu.Lock()
	for _, id := range sdkIDs {
		if path := goNameModule(id); l.loaded[path] == nil && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	l.mu.Unlock()
	if len(paths) == 0 {
		return nil
	}
	_, err := l.load(paths...)
	return err
}

// service loads the SDK package for a service, once per loader.
func (l *goSDKTypes) service(sdkID string) (*goSDKService, error) {
	path := goNameModule(sdkID)
	l.mu.Lock()
	svc, ok := l.loaded[path]
	l.mu.Unlock()
	if ok {
		return svc, nil
	}
	loaded, err := l.load(path)
	if err != nil {
		return nil, err
	}
	return loaded[path], nil
}

// load type-checks each named package and caches the result.
func (l *goSDKTypes) load(paths ...string) (map[string]*goSDKService, error) {
	cfg := &packages.Config{
		// NeedTypes type-checks the packages themselves from their
		// dependencies' export data; NeedDeps would additionally populate every
		// dependency's own types, which nothing here reads and which the AWS
		// SDK's dependency graph makes expensive.
		Mode: packages.NeedName | packages.NeedTypes,
		Dir:  l.dir,
		// GOWORK=off keeps a developer's workspace file — which cannot know
		// about a testdata fixture module — from redirecting the load.
		Env: append(os.Environ(), "GOWORK=off"),
	}
	loaded, err := packages.Load(cfg, paths...)
	if err != nil {
		return nil, fmt.Errorf("load %s from %s: %w", strings.Join(paths, ", "), l.dir, err)
	}
	if len(loaded) != len(paths) {
		return nil, fmt.Errorf("load %s from %s: %d packages matched, want %d", strings.Join(paths, ", "), l.dir, len(loaded), len(paths))
	}
	out := make(map[string]*goSDKService, len(loaded))
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, pkg := range loaded {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("load %s from %s: %v (is it required by %s/go.mod?)", pkg.PkgPath, l.dir, pkg.Errors[0], goSDKModuleDir)
		}
		if pkg.Types == nil || pkg.Types.Scope() == nil {
			return nil, fmt.Errorf("load %s from %s: no type information", pkg.PkgPath, l.dir)
		}
		svc := &goSDKService{
			Name:      pkg.Types.Name(),
			Path:      pkg.PkgPath,
			TypesPath: pkg.PkgPath + "/types",
			scope:     pkg.Types.Scope(),
		}
		l.loaded[svc.Path] = svc
		out[svc.Path] = svc
	}
	return out, nil
}

// Input returns the struct behind an operation's `<Op>Input`.
func (s *goSDKService) Input(op string) (*types.Struct, error) {
	name := op + "Input"
	obj := s.scope.Lookup(name)
	if obj == nil {
		return nil, fmt.Errorf("%s declares no %s; the vendored SDK is older than the pinned model", s.Path, name)
	}
	named, ok := types.Unalias(obj.Type()).(*types.Named)
	if !ok {
		return nil, fmt.Errorf("%s.%s is not a named type", s.Path, name)
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil, fmt.Errorf("%s.%s is not a struct", s.Path, name)
	}
	return structure, nil
}

// field finds the exported field a modeled member maps onto. smithy-go's rule
// is the member name with its first letter capitalized, plus reserved-word and
// collision handling this does not reproduce — so the member's own spelling is
// tried too, and a member with no field at all is reported rather than guessed
// at. That report is the point: under the reflective binder it was a run-time
// "has no settable member" and a red compat result; here it is a refusal.
func goSDKField(structure *types.Struct, member string) (*types.Var, bool) {
	want := []string{goNameField(member), member}
	for i := 0; i < structure.NumFields(); i++ {
		f := structure.Field(i)
		if !f.Exported() {
			continue
		}
		for _, name := range want {
			if f.Name() == name {
				return f, true
			}
		}
	}
	return nil, false
}
