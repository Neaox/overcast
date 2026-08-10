// Package registry loads the shared registry.json and builds TestGroup lists.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	// Depends lists tests in the SAME group that must run before this one.
	// BuildGroups sorts by these edges; they are not a cross-group reference.
	Depends []string `json:"depends"`
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

// topoSort topologically sorts tests within a group using their declared
// dependencies.  Tests with no dependencies come first; tests whose deps are all
// resolved come next.  Falls back to declaration order for tests at the same
// dependency depth, so the sort reorders only what the edges require.
//
// Registry file order is not authoritative on its own: `cloudformation-stacks`
// listed DeleteStack before the UpdateStack it depends on, and this suite ran it
// that way — deleting the shared stack and then updating it — while the cli and
// node-js suites, which already sorted, did not. Same registry, different order
// per language. Kept identical to the cli, node-js, Java, .NET and Rust sorts.
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
	ambiguous := AmbiguousTestNames(reg)
	var groups []harness.TestGroup

	for _, rg := range reg.Groups {
		// CDK lifecycle tests belong to the cdk suite, not SDK suites.
		if rg.Service == "cdk" {
			continue
		}
		var tests []harness.TestCase

		// Topologically sort tests by their declared dependencies so that
		// prerequisites always execute before the tests that need them.
		for _, rt := range topoSort(rg.Tests) {
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
			// fall back to the bare test name.  The bare fallback is refused for
			// a name claimed by more than one group: it would bind this group to
			// another group's implementation and report its result as ours.
			// ValidateImpls rejects such a registration outright; this is the
			// second line of defence, so a mis-bind cannot occur even if
			// validation is bypassed.
			qualifiedKey := rg.Name + ":" + rt.Name
			fn, ok := impls[qualifiedKey]
			if !ok && !ambiguous[rt.Name] {
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

// AmbiguousTestNames returns the test names that more than one registry group
// declares.
//
// A bare-name implementation cannot serve these. `ListUsers` belongs to both
// `iam-users` and `cognito-userpools`, so a bare `ListUsers` impl binds
// whichever group happens to resolve it — and the loser silently runs the
// other service's test and reports the result as its own. Suites must register
// the group-qualified key for an ambiguous name.
func AmbiguousTestNames(reg *Registry) map[string]bool {
	owners := TestNameOwners(reg)
	ambiguous := map[string]bool{}
	for name, groups := range owners {
		if len(groups) > 1 {
			ambiguous[name] = true
		}
	}
	return ambiguous
}

// TestNameOwners maps each registry test name to the sorted names of the groups
// that declare it.
func TestNameOwners(reg *Registry) map[string][]string {
	owners := map[string][]string{}
	for _, rg := range reg.Groups {
		for _, rt := range rg.Tests {
			if !slices.Contains(owners[rt.Name], rg.Name) {
				owners[rt.Name] = append(owners[rt.Name], rg.Name)
			}
		}
	}
	for name := range owners {
		slices.Sort(owners[name])
	}
	return owners
}

// ImplSource is one service group's contribution to the suite's impl map,
// labelled with the group it came from so a collision can name both sides.
type ImplSource struct {
	Name  string
	Impls ImplMap
}

// MergeImpls flattens the per-service impl maps into the single map the loader
// resolves against, refusing any key that two sources both register.
//
// The merge used to be `for k, v := range svc.Impls { allImpls[k] = v }` —
// last writer wins, and silently. Two service files both registering
// "lambda-crud:CreateFunction" left one implementation unreachable with nothing
// said about it, and the run reported a result for whichever one survived.
// ValidateImpls cannot catch this: by the time it sees the flattened map the
// discarded implementation is already gone, and the surviving key resolves
// perfectly well.
func MergeImpls(sources []ImplSource, suite string) (ImplMap, error) {
	merged := make(ImplMap)
	owner := make(map[string]string) // key → the source that registered it first

	var problems []string
	for _, src := range sources {
		for key, fn := range src.Impls {
			if first, dup := owner[key]; dup {
				problems = append(problems, duplicateProblem(key, first, src.Name))
				continue
			}
			owner[key] = src.Name
			merged[key] = fn
		}
	}
	if len(problems) == 0 {
		return merged, nil
	}
	// Map iteration order is random, so sort for a stable message. Every
	// problem starts with the key, which is what a reader scans for.
	slices.Sort(problems)
	return nil, fmt.Errorf("[%s] %d duplicate impl registration(s):\n  - %s",
		suite, len(problems), strings.Join(problems, "\n  - "))
}

// duplicateProblem describes one collision. The two sources are the same when a
// single service group registers the key twice.
func duplicateProblem(key, first, second string) string {
	if first == second {
		return fmt.Sprintf("impl %q is registered twice by %q"+
			" — one of the two would be silently discarded; remove or re-key one", key, first)
	}
	return fmt.Sprintf("impl %q is registered by both %q and %q"+
		" — one of the two would be silently discarded; remove or re-key one", key, first, second)
}

// ValidateImpls rejects impl keys that cannot be bound to exactly one registry
// test. It returns an error rather than warning: an unresolvable key used to be
// a stderr line nobody read, while the test it was meant to implement quietly
// fell back to another group's implementation and reported a pass.
//
// Two registrations are refused:
//
//   - a key matching no registry entry — a typo, a stale name, or the wrong
//     separator (every suite uses "group:test"; "group/test" is not accepted);
//   - a bare key for a test name that several groups declare, which cannot say
//     which group it implements.
func ValidateImpls(reg *Registry, impls ImplMap, suite string) error {
	owners := TestNameOwners(reg)
	known := map[string]bool{}
	for _, rg := range reg.Groups {
		for _, rt := range rg.Tests {
			known[rt.Name] = true
			known[rg.Name+":"+rt.Name] = true
		}
	}

	names := make([]string, 0, len(impls))
	for name := range impls {
		names = append(names, name)
	}
	slices.Sort(names)

	var problems []string
	for _, name := range names {
		switch {
		case !known[name]:
			msg := fmt.Sprintf("impl %q matches no registry entry", name)
			if strings.Contains(name, "/") {
				// The Java suite used "group/test" until the separator was
				// unified; a key copied from it resolves to nothing here.
				msg += fmt.Sprintf(` (group-qualified keys use ":", not "/" — did you mean %q?)`,
					strings.Replace(name, "/", ":", 1))
			}
			problems = append(problems, msg)
		case len(owners[name]) > 1:
			// Naming every candidate rather than guessing one: only the author
			// knows which group this implementation is for, and binding it to
			// the wrong one is the failure this check exists to prevent.
			qualified := make([]string, 0, len(owners[name]))
			for _, group := range owners[name] {
				qualified = append(qualified, fmt.Sprintf("%q", group+":"+name))
			}
			problems = append(problems, fmt.Sprintf(
				"impl %q is ambiguous: groups %v all declare a test named %q — qualify it with the group it implements, one of: %s",
				name, owners[name], name, strings.Join(qualified, ", ")))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("[%s] %d unusable impl registration(s):\n  - %s",
		suite, len(problems), strings.Join(problems, "\n  - "))
}
