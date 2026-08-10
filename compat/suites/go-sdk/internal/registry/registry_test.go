package registry

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
)

// twoGroupsOneName models the shape that made a mis-binding possible: two
// unrelated groups declaring a test of the same name, plus a name owned by
// exactly one group.
func twoGroupsOneName() *Registry {
	return &Registry{Groups: []RegistryGroup{
		{Service: "iam", Name: "iam-users", Tests: []RegistryTest{
			{Name: "ListUsers"}, {Name: "CreateUser"},
		}},
		{Service: "cognito", Name: "cognito-userpools", Tests: []RegistryTest{
			{Name: "ListUsers"},
		}},
	}}
}

func marker(id string) harness.TestFn {
	return func(_ context.Context, t *harness.TestContext) error {
		t.Set("ran", id)
		return nil
	}
}

// findTest returns the built test case for a group/test pair.
func findTest(t *testing.T, groups []harness.TestGroup, group, test string) harness.TestCase {
	t.Helper()
	for _, g := range groups {
		if g.Name != group {
			continue
		}
		for _, tc := range g.Tests {
			if tc.Name == test {
				return tc
			}
		}
	}
	t.Fatalf("no test %s/%s in built groups", group, test)
	return harness.TestCase{}
}

// An impl key that resolves to nothing must abort the run, not warn. Before
// this was enforced, a key with the wrong separator was a stderr line nobody
// read while the test it meant to implement fell back to another group's
// implementation and reported a pass.
func TestValidateImplsRejectsUnresolvableKey(t *testing.T) {
	reg := twoGroupsOneName()

	for _, tc := range []struct {
		name    string
		key     string
		wantSub []string
	}{
		{
			name:    "wrong separator",
			key:     "iam-users/CreateUser",
			wantSub: []string{`"iam-users/CreateUser"`, "matches no registry entry", `"iam-users:CreateUser"`},
		},
		{
			name:    "unknown group",
			key:     "iam-usres:CreateUser",
			wantSub: []string{`"iam-usres:CreateUser"`, "matches no registry entry"},
		},
		{
			name:    "unknown test",
			key:     "CreateUsr",
			wantSub: []string{`"CreateUsr"`, "matches no registry entry"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateImpls(reg, ImplMap{tc.key: marker("x")}, "go-sdk")
			if err == nil {
				t.Fatalf("ValidateImpls(%q) = nil, want error", tc.key)
			}
			for _, sub := range tc.wantSub {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q does not name %q", err, sub)
				}
			}
		})
	}
}

// A bare key for a name several groups declare cannot say which group it
// implements, so it must be refused rather than bound to one of them.
func TestValidateImplsRejectsAmbiguousBareKey(t *testing.T) {
	err := ValidateImpls(twoGroupsOneName(), ImplMap{"ListUsers": marker("iam")}, "go-sdk")
	if err == nil {
		t.Fatal("ValidateImpls(bare ambiguous key) = nil, want error")
	}
	for _, sub := range []string{`"ListUsers"`, "ambiguous", "iam-users", "cognito-userpools"} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q does not name %q", err, sub)
		}
	}
}

// The unambiguous cases must keep working: a bare key for a name only one group
// declares, and group-qualified keys for a shared name.
func TestValidateImplsAcceptsResolvableKeys(t *testing.T) {
	impls := ImplMap{
		"CreateUser":                  marker("iam"), // bare, single owner
		"iam-users:ListUsers":         marker("iam"),
		"cognito-userpools:ListUsers": marker("cognito"),
	}
	if err := ValidateImpls(twoGroupsOneName(), impls, "go-sdk"); err != nil {
		t.Fatalf("ValidateImpls(valid keys) = %v, want nil", err)
	}
}

// Defence in depth: even when validation is bypassed, BuildGroups must never
// bind a group to another group's implementation via the bare fallback.
func TestBuildGroupsRefusesCrossGroupBareFallback(t *testing.T) {
	// Only IAM registers a bare ListUsers. cognito-userpools must not pick it up.
	groups := BuildGroups(twoGroupsOneName(), ImplMap{"ListUsers": marker("iam")},
		BuildGroupsOptions{Suite: "go-sdk"})

	cognito := findTest(t, groups, "cognito-userpools", "ListUsers")
	if cognito.Skip == "" {
		t.Fatal("cognito-userpools/ListUsers bound to an impl; want unimplemented skip")
	}
	if want := "not yet implemented in go-sdk test suite"; cognito.Skip != want {
		t.Errorf("skip = %q, want %q", cognito.Skip, want)
	}

	// The same refusal applies to the group that did register it: a bare key
	// cannot claim ownership of an ambiguous name.
	iam := findTest(t, groups, "iam-users", "ListUsers")
	if iam.Skip == "" {
		t.Error("iam-users/ListUsers bound to an ambiguous bare impl; want skip")
	}
}

// A group-qualified key binds only its own group.
func TestBuildGroupsBindsQualifiedKeyToItsGroup(t *testing.T) {
	groups := BuildGroups(twoGroupsOneName(),
		ImplMap{"iam-users:ListUsers": marker("iam")},
		BuildGroupsOptions{Suite: "go-sdk"})

	if iam := findTest(t, groups, "iam-users", "ListUsers"); iam.Skip != "" {
		t.Errorf("iam-users/ListUsers skipped (%q); want bound", iam.Skip)
	}
	if cognito := findTest(t, groups, "cognito-userpools", "ListUsers"); cognito.Skip == "" {
		t.Error("cognito-userpools/ListUsers bound to iam-users' impl")
	}
}

// The bare fallback must still work for a name only one group declares —
// most impls are registered that way.
func TestBuildGroupsAllowsUnambiguousBareFallback(t *testing.T) {
	groups := BuildGroups(twoGroupsOneName(), ImplMap{"CreateUser": marker("iam")},
		BuildGroupsOptions{Suite: "go-sdk"})

	if tc := findTest(t, groups, "iam-users", "CreateUser"); tc.Skip != "" {
		t.Errorf("iam-users/CreateUser skipped (%q); want bound via bare key", tc.Skip)
	}
}

// recorder returns an impl that records which source it came from, so a merge
// can be checked for having kept the right implementation rather than just a
// key of the right name.
func recorder(id string, into *string) harness.TestFn {
	return func(_ context.Context, _ *harness.TestContext) error {
		*into = id
		return nil
	}
}

// Two service files registering the same key must abort the run.
//
// This is the gap ValidateImpls cannot close. The merge that builds the suite's
// impl map is last-writer-wins, so one of the two implementations is discarded
// before validation ever sees the map — and the surviving key resolves
// perfectly well, so nothing is reported. The discarded test then runs the
// other file's implementation under its own name.
func TestMergeImplsRejectsDuplicateKey(t *testing.T) {
	var ran string
	_, err := MergeImpls([]ImplSource{
		{Name: "lambda", Impls: ImplMap{"lambda-crud:CreateFunction": recorder("lambda", &ran)}},
		{Name: "appsync", Impls: ImplMap{"lambda-crud:CreateFunction": recorder("appsync", &ran)}},
	}, "go-sdk")
	if err == nil {
		t.Fatal("MergeImpls(duplicate key) = nil error, want error")
	}
	// Both registering files must be named: the key alone does not say where to
	// look, and the whole point is that one of the two files is in the wrong.
	for _, sub := range []string{`"lambda-crud:CreateFunction"`, `"lambda"`, `"appsync"`, "duplicate impl registration"} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q does not name %q", err, sub)
		}
	}
}

// A bare key is just as clobberable as a qualified one, and the message must
// still name both sources.
func TestMergeImplsRejectsDuplicateBareKey(t *testing.T) {
	var ran string
	_, err := MergeImpls([]ImplSource{
		{Name: "iam", Impls: ImplMap{"CreateUser": recorder("iam", &ran)}},
		{Name: "cognito", Impls: ImplMap{"CreateUser": recorder("cognito", &ran)}},
	}, "go-sdk")
	if err == nil {
		t.Fatal("MergeImpls(duplicate bare key) = nil error, want error")
	}
	for _, sub := range []string{`"CreateUser"`, `"iam"`, `"cognito"`} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q does not name %q", err, sub)
		}
	}
}

// One source registering a key twice reads differently from two sources
// colliding — "both X and Y" would be nonsense when X and Y are the same file.
func TestMergeImplsReportsSingleSourceDuplicateAsSuch(t *testing.T) {
	var ran string
	_, err := MergeImpls([]ImplSource{
		{Name: "iam", Impls: ImplMap{"CreateUser": recorder("first", &ran)}},
		{Name: "iam", Impls: ImplMap{"CreateUser": recorder("second", &ran)}},
	}, "go-sdk")
	if err == nil {
		t.Fatal("MergeImpls(same source twice) = nil error, want error")
	}
	if !strings.Contains(err.Error(), `registered twice by "iam"`) {
		t.Errorf("error %q does not read as a single-source duplicate", err)
	}
}

// Every collision is reported, not just the first — otherwise fixing one
// duplicate only reveals the next.
func TestMergeImplsReportsEveryDuplicate(t *testing.T) {
	var ran string
	_, err := MergeImpls([]ImplSource{
		{Name: "iam", Impls: ImplMap{
			"iam-users:ListUsers":  recorder("iam", &ran),
			"iam-users:CreateUser": recorder("iam", &ran),
		}},
		{Name: "cognito", Impls: ImplMap{
			"iam-users:ListUsers":  recorder("cognito", &ran),
			"iam-users:CreateUser": recorder("cognito", &ran),
		}},
	}, "go-sdk")
	if err == nil {
		t.Fatal("MergeImpls(two duplicates) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "2 duplicate impl registration(s)") {
		t.Errorf("error %q does not report both duplicates", err)
	}
	// Sorted by key, so the message is the same on every run despite Go's
	// randomised map iteration order.
	createIdx := strings.Index(err.Error(), "iam-users:CreateUser")
	listIdx := strings.Index(err.Error(), "iam-users:ListUsers")
	if createIdx > listIdx {
		t.Errorf("problems not sorted by key; got:\n%v", err)
	}
}

// Negative control: distinct keys across sources merge cleanly, and each key
// keeps the implementation its own source registered.
func TestMergeImplsAcceptsDisjointSources(t *testing.T) {
	var ran string
	merged, err := MergeImpls([]ImplSource{
		{Name: "iam", Impls: ImplMap{
			"iam-users:ListUsers": recorder("iam", &ran),
			"CreateUser":          recorder("iam", &ran),
		}},
		{Name: "cognito", Impls: ImplMap{
			"cognito-userpools:ListUsers": recorder("cognito", &ran),
		}},
	}, "go-sdk")
	if err != nil {
		t.Fatalf("MergeImpls(disjoint sources) = %v, want nil", err)
	}
	if len(merged) != 3 {
		t.Fatalf("merged %d keys, want 3: %v", len(merged), merged)
	}
	for key, want := range map[string]string{
		"iam-users:ListUsers":         "iam",
		"CreateUser":                  "iam",
		"cognito-userpools:ListUsers": "cognito",
	} {
		fn, ok := merged[key]
		if !ok {
			t.Errorf("merged map is missing %q", key)
			continue
		}
		ran = ""
		if err := fn(context.Background(), nil); err != nil {
			t.Errorf("%s() = %v", key, err)
		}
		if ran != want {
			t.Errorf("%s bound to %q's impl, want %q's", key, ran, want)
		}
	}
}

// The real registry must not contain a group whose tests cannot be told apart
// from another group's by name alone without qualification — this is the data
// the rules above are enforced against.
func TestAmbiguousTestNamesMatchesOwners(t *testing.T) {
	reg := twoGroupsOneName()
	ambiguous := AmbiguousTestNames(reg)

	if !ambiguous["ListUsers"] {
		t.Error("ListUsers not reported ambiguous")
	}
	if ambiguous["CreateUser"] {
		t.Error("CreateUser reported ambiguous; only one group declares it")
	}
	owners := TestNameOwners(reg)
	if got := strings.Join(owners["ListUsers"], ","); got != "cognito-userpools,iam-users" {
		t.Errorf("owners[ListUsers] = %q, want sorted cognito-userpools,iam-users", got)
	}
}

// builtOrder returns the names of a built group's tests, in run order.
func builtOrder(t *testing.T, groups []harness.TestGroup, group string) []string {
	t.Helper()
	for _, g := range groups {
		if g.Name != group {
			continue
		}
		names := make([]string, 0, len(g.Tests))
		for _, tc := range g.Tests {
			names = append(names, tc.Name)
		}
		return names
	}
	t.Fatalf("group %q not built", group)
	return nil
}

// allImpls registers a passing implementation for every test in the registry so
// ordering is observed on real test cases rather than auto-skips.
func allImpls(reg *Registry) ImplMap {
	impls := make(ImplMap)
	for _, rg := range reg.Groups {
		for _, rt := range rg.Tests {
			impls[rg.Name+":"+rt.Name] = marker(rt.Name)
		}
	}
	return impls
}

// A group whose declaration order contradicts its own `depends` must run in
// dependency order, not file order. `cloudformation-stacks` listed DeleteStack
// before the UpdateStack it depends on, so this suite deleted the shared stack
// and then updated it while the cli and node-js suites — which already sort —
// did not. Ordering that differs by suite makes a group's outcome depend on
// which language ran it.
func TestBuildGroupsRunsDependenciesFirst(t *testing.T) {
	reg := &Registry{Groups: []RegistryGroup{
		{Service: "cloudformation", Name: "stacks", Tests: []RegistryTest{
			{Name: "CreateStack"},
			{Name: "DeleteStack", Depends: []string{"UpdateStack"}},
			{Name: "ValidateTemplate"},
			{Name: "UpdateStack", Depends: []string{"CreateStack"}},
		}},
	}}

	groups := BuildGroups(reg, allImpls(reg), BuildGroupsOptions{Suite: "go-sdk"})
	got := builtOrder(t, groups, "stacks")

	pos := map[string]int{}
	for i, name := range got {
		pos[name] = i
	}
	if pos["UpdateStack"] > pos["DeleteStack"] {
		t.Errorf("UpdateStack runs after the DeleteStack that depends on it: %v", got)
	}
	if pos["CreateStack"] > pos["UpdateStack"] {
		t.Errorf("CreateStack runs after UpdateStack: %v", got)
	}
}

// Tests at the same dependency depth keep their declaration order, so sorting
// reorders only what the edges require. A group is otherwise free to be
// rearranged run to run, which would make failures hard to reproduce.
func TestBuildGroupsKeepsDeclarationOrderWithinDepth(t *testing.T) {
	reg := &Registry{Groups: []RegistryGroup{
		{Service: "s3", Name: "s3-crud", Tests: []RegistryTest{
			{Name: "CreateBucket"},
			{Name: "PutObject", Depends: []string{"CreateBucket"}},
			{Name: "GetObject", Depends: []string{"PutObject"}},
			{Name: "ListObjects", Depends: []string{"PutObject"}},
		}},
	}}

	groups := BuildGroups(reg, allImpls(reg), BuildGroupsOptions{Suite: "go-sdk"})
	got := builtOrder(t, groups, "s3-crud")
	want := []string{"CreateBucket", "PutObject", "GetObject", "ListObjects"}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v (already in dependency order — nothing to reorder)", got, want)
	}
}

// A `depends` cycle is a registry bug, but it must not hang or drop tests: the
// sort breaks the cycle and still emits every test exactly once.
func TestBuildGroupsSurvivesDependencyCycle(t *testing.T) {
	reg := &Registry{Groups: []RegistryGroup{
		{Service: "sqs", Name: "cyclic", Tests: []RegistryTest{
			{Name: "A", Depends: []string{"B"}},
			{Name: "B", Depends: []string{"A"}},
			{Name: "C"},
		}},
	}}

	groups := BuildGroups(reg, allImpls(reg), BuildGroupsOptions{Suite: "go-sdk"})
	got := builtOrder(t, groups, "cyclic")

	if len(got) != 3 {
		t.Fatalf("built %d tests from a 3-test group: %v", len(got), got)
	}
	seen := map[string]int{}
	for _, name := range got {
		seen[name]++
	}
	for _, name := range []string{"A", "B", "C"} {
		if seen[name] != 1 {
			t.Errorf("test %q emitted %d times, want exactly 1: %v", name, seen[name], got)
		}
	}
}

// A dependency naming a test the group does not declare must not drop it or the
// dependent. Only same-group edges order a run; the registry schema says as
// much, and a stale name is otherwise silently load-bearing.
func TestBuildGroupsIgnoresUnknownDependency(t *testing.T) {
	reg := &Registry{Groups: []RegistryGroup{
		{Service: "sqs", Name: "queues", Tests: []RegistryTest{
			{Name: "CreateQueue"},
			{Name: "SendMessage", Depends: []string{"CreateQueue", "NotInThisGroup"}},
		}},
	}}

	groups := BuildGroups(reg, allImpls(reg), BuildGroupsOptions{Suite: "go-sdk"})
	got := builtOrder(t, groups, "queues")

	if strings.Join(got, ",") != "CreateQueue,SendMessage" {
		t.Errorf("order = %v, want [CreateQueue SendMessage]", got)
	}
}

// The registry the suites actually run must sort the same way here as it does
// in the cli and node-js suites: every `depends` edge satisfied before the test
// that declares it.
func TestRealRegistryBuildsInDependencyOrder(t *testing.T) {
	// `go test` runs with the package directory as CWD, so the loader's default
	// "../registry.json" resolves inside internal/. Point it at the real file.
	t.Setenv("OVERCAST_REGISTRY_PATH", filepath.Join("..", "..", "..", "registry.json"))
	reg, err := Load()
	if err != nil {
		t.Fatalf("load registry.json: %v", err)
	}

	groups := BuildGroups(reg, allImpls(reg), BuildGroupsOptions{Suite: "go-sdk"})
	declared := map[string]map[string][]string{}
	for _, rg := range reg.Groups {
		deps := map[string][]string{}
		for _, rt := range rg.Tests {
			deps[rt.Name] = rt.Depends
		}
		declared[rg.Name] = deps
	}

	for _, g := range groups {
		ran := map[string]bool{}
		for _, tc := range g.Tests {
			for _, dep := range declared[g.Name][tc.Name] {
				if _, sameGroup := declared[g.Name][dep]; sameGroup && !ran[dep] {
					t.Errorf("%s: %s runs before its dependency %s", g.Name, tc.Name, dep)
				}
			}
			ran[tc.Name] = true
		}
	}
}
