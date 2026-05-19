// Package registry loads the shared registry.json and builds TestGroup lists.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
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
	Op       *string  `json:"op"` // nil = absent, "" = null
	Skip     string   `json:"skip"`
	Requires []string `json:"requires"`
}

// Registry is the root of registry.json.
type Registry struct {
	Groups []RegistryGroup `json:"groups"`
}

// ImplMap maps test names to TestFn implementations.
type ImplMap map[string]harness.TestFn

var _noop harness.TestFn = func(_ context.Context, _ *harness.TestContext) error { return nil }

// registryPath returns the absolute path to registry.json.
//
// The suite is run with `go run ./cmd/runner` from compat/suites/go-sdk/,
// so registry.json is one level up at compat/suites/registry.json.
//
// When OVERCAST_REGISTRY_PATH is set, that value is used directly.
func registryPath() string {
	if p := os.Getenv("OVERCAST_REGISTRY_PATH"); p != "" {
		return p
	}
	// Default: one level up from CWD (compat/suites/go-sdk → compat/suites/).
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
	Capabilities map[string]bool // set of capability strings this runner supports
	Setup        map[string]func(context.Context, *harness.TestContext) error
	Teardown     map[string]func(context.Context, *harness.TestContext) error
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
		// CDK lifecycle tests belong to the cdk suite, not SDK suites.
		if rg.Service == "cdk" {
			continue
		}
		var tests []harness.TestCase

		for _, rt := range rg.Tests {
			op := ""
			if rt.Op != nil {
				op = *rt.Op
			}

			if rt.Skip != "" {
				tests = append(tests, harness.TestCase{Name: rt.Name, Fn: _noop, Op: op, Skip: rt.Skip})
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
				reason := fmt.Sprintf("requires %v (not available in this environment)", missing)
				tests = append(tests, harness.TestCase{Name: rt.Name, Fn: _noop, Op: op, Skip: reason})
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
					Skip: fmt.Sprintf("not yet implemented in %s test suite", opts.Suite),
				})
				continue
			}
			if fn == nil {
				// Explicitly registered as nil → the AWS SDK client does not
				// yet expose this operation.  Emit as N/A, not as a suite gap.
				tests = append(tests, harness.TestCase{
					Name: rt.Name, Fn: _noop, Op: op,
					NA: "not yet supported by the AWS Go SDK v2",
				})
				continue
			}

			tests = append(tests, harness.TestCase{Name: rt.Name, Fn: fn, Op: op})
		}

		g := harness.TestGroup{
			Suite:   opts.Suite,
			Service: rg.Service,
			Name:    rg.Name,
			Tests:   tests,
		}
		if opts.Setup != nil {
			groupName := rg.Name
			g.Setup = func(ctx context.Context, t *harness.TestContext) error {
				fn, ok := opts.Setup[groupName]
				if !ok {
					return nil
				}
				return fn(ctx, t)
			}
		}
		if opts.Teardown != nil {
			groupName := rg.Name
			g.Teardown = func(ctx context.Context, t *harness.TestContext) error {
				fn, ok := opts.Teardown[groupName]
				if !ok {
					return nil
				}
				return fn(ctx, t)
			}
		}
		groups = append(groups, g)
	}

	return groups
}

// ValidateImpls warns about impl keys not present in the registry.
func ValidateImpls(reg *Registry, impls ImplMap, suite string) {
	all := map[string]bool{}
	for _, rg := range reg.Groups {
		for _, rt := range rg.Tests {
			all[rt.Name] = true
			all[rg.Name+":"+rt.Name] = true
		}
	}
	for name := range impls {
		if !all[name] {
			fmt.Fprintf(os.Stderr, "registry: [%s] impl %q not found in registry (orphan)\n", suite, name)
		}
	}
}
