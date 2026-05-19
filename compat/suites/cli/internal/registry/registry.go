// Package registry loads the shared registry.json and builds TestGroup lists.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// RegistryGroup is one group entry from registry.json.
type RegistryGroup struct {
	Service string         `json:"service"`
	Name    string         `json:"name"`
	Tests   []RegistryTest `json:"tests"`
}

// RegistryTest is one test entry within a group.
type RegistryTest struct {
	Name     string   `json:"name"`
	Op       *string  `json:"op"`
	Skip     string   `json:"skip"`
	Requires []string `json:"requires"`
	Depends  []string `json:"depends"`
}

// Registry is the root of registry.json.
type Registry struct {
	Groups []RegistryGroup `json:"groups"`
}

// ImplMap maps test names to TestFn implementations.
type ImplMap map[string]harness.TestFn

var _noop harness.TestFn = func(_ context.Context, _ *harness.TestContext) error { return nil }

// registryPath returns the absolute path to registry.json.
// The suite is run with `go run ./cmd/runner` from compat/suites/cli/,
// so registry.json is one level up at compat/suites/registry.json.
func registryPath() string {
	if p := os.Getenv("OVERCAST_REGISTRY_PATH"); p != "" {
		return p
	}
	return filepath.Join("..", "registry.json")
}

// Load reads and parses registry.json.
func Load() (*Registry, error) {
	p := registryPath()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("registry: read %s: %w", p, err)
	}
	var reg Registry
	if err := json.Unmarshal(b, &reg); err != nil {
		return nil, fmt.Errorf("registry: parse: %w", err)
	}
	return &reg, nil
}

// BuildGroupsOptions controls how groups are assembled from the registry.
type BuildGroupsOptions struct {
	Suite        string
	Capabilities map[string]bool
	Setup        map[string]func(context.Context, *harness.TestContext) error
	Teardown     map[string]func(context.Context, *harness.TestContext) error
}

// topoSort topologically sorts tests within a group using their declared dependencies.
// Tests with no dependencies come first; tests whose deps are all resolved come next.
// Falls back to declaration order for tests at the same dependency depth.
func topoSort(tests []RegistryTest) []RegistryTest {
	byName := make(map[string]*RegistryTest, len(tests))
	for i := range tests {
		byName[tests[i].Name] = &tests[i]
	}

	sorted := make([]RegistryTest, 0, len(tests))
	visited := make(map[string]bool)
	visiting := make(map[string]bool) // cycle detection

	var visit func(t *RegistryTest)
	visit = func(t *RegistryTest) {
		if visited[t.Name] || visiting[t.Name] {
			return
		}
		visiting[t.Name] = true
		for _, dep := range t.Depends {
			if dt, ok := byName[dep]; ok {
				visit(dt)
			}
		}
		delete(visiting, t.Name)
		visited[t.Name] = true
		sorted = append(sorted, *t)
	}

	for i := range tests {
		visit(&tests[i])
	}
	return sorted
}

// BuildGroups creates a []harness.TestGroup from the registry, auto-skipping
// tests whose impls are absent or whose requirements are unmet.
func BuildGroups(reg *Registry, impls ImplMap, opts BuildGroupsOptions) []harness.TestGroup {
	caps := opts.Capabilities
	if caps == nil {
		caps = map[string]bool{}
	}
	var groups []harness.TestGroup

	for _, rg := range reg.Groups {
		// CDK lifecycle tests belong to the cdk suite, not the CLI suite.
		if rg.Service == "cdk" {
			continue
		}
		var tests []harness.TestCase

		// Topologically sort tests by their declared dependencies.
		sortedTests := topoSort(rg.Tests)

		for _, rt := range sortedTests {
			op := ""
			if rt.Op != nil {
				op = *rt.Op
			}

			if rt.Skip != "" {
				tests = append(tests, harness.TestCase{Name: rt.Name, Fn: _noop, Op: op, Skip: rt.Skip, Depends: rt.Depends})
				continue
			}

			// Capability gate
			var missing []string
			for _, req := range rt.Requires {
				if !caps[req] {
					missing = append(missing, req)
				}
			}
			if len(missing) > 0 {
				tests = append(tests, harness.TestCase{
					Name: rt.Name, Fn: _noop, Op: op,
					Skip:    fmt.Sprintf("requires capabilities: %v", missing),
					Depends: rt.Depends,
				})
				continue
			}

			// Look up by group-qualified key ("groupName:testName") first, then
			// fall back to bare test name.  This avoids collisions when multiple
			// groups share the same test name (e.g. lambda-crud vs appsync-functions).
			qualifiedKey := rg.Name + ":" + rt.Name
			fn, ok := impls[qualifiedKey]
			if !ok {
				fn, ok = impls[rt.Name]
			}
			if !ok {
				tests = append(tests, harness.TestCase{
					Name: rt.Name, Fn: _noop, Op: op,
					Skip:    "not implemented in cli suite",
					Depends: rt.Depends,
				})
				continue
			}
			if fn == nil {
				// Explicitly registered as nil → the AWS CLI does not yet
				// expose this operation.  Emit as N/A, not as a suite gap.
				tests = append(tests, harness.TestCase{
					Name: rt.Name, Fn: _noop, Op: op,
					NA:      "not yet supported by the AWS CLI",
					Depends: rt.Depends,
				})
				continue
			}
			tests = append(tests, harness.TestCase{Name: rt.Name, Fn: fn, Op: op, Depends: rt.Depends})
		}

		groupName := rg.Name // capture for closures
		g := harness.TestGroup{
			Suite:   opts.Suite,
			Service: rg.Service,
			Name:    groupName,
			Tests:   tests,
		}

		if fn, ok := opts.Setup[groupName]; ok {
			g.Setup = func(ctx context.Context, t *harness.TestContext) error {
				return fn(ctx, t)
			}
		}
		if fn, ok := opts.Teardown[groupName]; ok {
			g.Teardown = func(ctx context.Context, t *harness.TestContext) error {
				return fn(ctx, t)
			}
		}

		groups = append(groups, g)
	}
	return groups
}

// ValidateImpls warns about impls that are not referenced by any registry group.
// Both bare names ("CreateFunction") and group-qualified names
// ("lambda-crud:CreateFunction") are accepted.
func ValidateImpls(reg *Registry, impls ImplMap, suite string) {
	known := make(map[string]bool)
	for _, rg := range reg.Groups {
		for _, rt := range rg.Tests {
			known[rt.Name] = true
			known[rg.Name+":"+rt.Name] = true
		}
	}
	for name := range impls {
		if !known[name] {
			fmt.Fprintf(os.Stderr, "[%s] WARNING: impl %q has no matching entry in registry.json. Check for a typo or add it to the registry.\n", suite, name)
		}
	}
}

// EmitAllNA writes NDJSON run_start + na result for every test in the registry +
// run_end to stdout. Used when a hard prerequisite (e.g. the aws CLI binary) is
// missing and no test can possibly run.
func EmitAllNA(reg *Registry, suiteName, runID, reason string) {
	emit := func(obj any) {
		b, _ := json.Marshal(obj)
		fmt.Println(string(b))
	}
	emit(map[string]any{"event": "run_start", "suite": suiteName, "run_id": runID})
	for _, rg := range reg.Groups {
		for _, rt := range rg.Tests {
			op := ""
			if rt.Op != nil {
				op = *rt.Op
			}
			emit(map[string]any{
				"event":       "test_result",
				"suite":       suiteName,
				"service":     rg.Service,
				"group":       rg.Name,
				"test":        rt.Name,
				"op":          op,
				"status":      "na",
				"na":          reason,
				"duration_ms": 0,
			})
		}
	}
	emit(map[string]any{"event": "run_end", "suite": suiteName, "run_id": runID})
}
