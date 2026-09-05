package registry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
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
			err := ValidateImpls(reg, ImplMap{tc.key: marker("x")}, "cli")
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
	err := ValidateImpls(twoGroupsOneName(), ImplMap{"ListUsers": marker("iam")}, "cli")
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
	if err := ValidateImpls(twoGroupsOneName(), impls, "cli"); err != nil {
		t.Fatalf("ValidateImpls(valid keys) = %v, want nil", err)
	}
}

// Defence in depth: even when validation is bypassed, BuildGroups must never
// bind a group to another group's implementation via the bare fallback.
func TestBuildGroupsRefusesCrossGroupBareFallback(t *testing.T) {
	// Only IAM registers a bare ListUsers. cognito-userpools must not pick it up.
	groups := BuildGroups(twoGroupsOneName(), ImplMap{"ListUsers": marker("iam")},
		BuildGroupsOptions{Suite: "cli"})

	cognito := findTest(t, groups, "cognito-userpools", "ListUsers")
	if cognito.Skip == "" {
		t.Fatal("cognito-userpools/ListUsers bound to an impl; want unimplemented skip")
	}
	if want := "not yet implemented in cli test suite"; cognito.Skip != want {
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
		BuildGroupsOptions{Suite: "cli"})

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
		BuildGroupsOptions{Suite: "cli"})

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
	}, "cli")
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
	}, "cli")
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
	}, "cli")
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
	}, "cli")
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
	}, "cli")
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

// ─── registry.generated.json (#1393) ──────────────────────────────────────
//
// Loading rules from the shared loader contract: a missing generated file is
// a no-op; the checked-in empty file is a no-op; a synthetic non-empty file
// concatenates after the hand-written groups; a group whose `suites` excludes
// this suite is not loaded at all; a generated group with no impl and no
// scenario backend fails loudly with the exact interim-rule message; and a
// name collision between the two files is a load error.

// writeRegistryPair writes a hand-written registry.json and, if genBody is
// non-empty, a sibling registry.generated.json in the same temp dir, then
// points OVERCAST_REGISTRY_PATH at the hand-written file so Load() resolves
// both from there.
func writeRegistryPair(t *testing.T, handBody, genBody string) {
	t.Helper()
	dir := t.TempDir()
	handPath := filepath.Join(dir, "registry.json")
	if err := os.WriteFile(handPath, []byte(handBody), 0o644); err != nil {
		t.Fatalf("write registry.json: %v", err)
	}
	if genBody != "" {
		if err := os.WriteFile(filepath.Join(dir, "registry.generated.json"), []byte(genBody), 0o644); err != nil {
			t.Fatalf("write registry.generated.json: %v", err)
		}
	}
	t.Setenv("OVERCAST_REGISTRY_PATH", handPath)
}

const handWrittenOnly = `{"version":1,"groups":[{"service":"s3","name":"s3-crud","tests":[{"name":"CreateBucket"}]}]}`

// A missing registry.generated.json must be a no-op: Load() returns exactly
// the hand-written groups, unchanged. Suite images, CI artifacts and branches
// cut before the file existed have to keep working.
func TestLoadMissingGeneratedFileIsNoOp(t *testing.T) {
	writeRegistryPair(t, handWrittenOnly, "")

	reg, err := Load()
	if err != nil {
		t.Fatalf("Load() with no generated file = %v, want nil error", err)
	}
	if len(reg.Groups) != 1 || reg.Groups[0].Name != "s3-crud" {
		t.Fatalf("Load() groups = %+v, want only the hand-written s3-crud group", reg.Groups)
	}
}

// The checked-in empty file ({"version":1,"groups":[]}) must also be a no-op.
// Asserting only this invariant — not that the real on-disk file is currently
// empty — keeps the test from pinning a fact the G2 pilot is about to change.
func TestLoadEmptyGeneratedFileIsNoOp(t *testing.T) {
	writeRegistryPair(t, handWrittenOnly, `{"version":1,"groups":[]}`)

	reg, err := Load()
	if err != nil {
		t.Fatalf("Load() with empty generated file = %v, want nil error", err)
	}
	if len(reg.Groups) != 1 || reg.Groups[0].Name != "s3-crud" {
		t.Fatalf("Load() groups = %+v, want only the hand-written s3-crud group", reg.Groups)
	}
}

// A synthetic non-empty generated file must be concatenated after the
// hand-written groups, in file order.
func TestLoadConcatenatesGeneratedGroupsAfterHandWritten(t *testing.T) {
	gen := `{"version":1,"groups":[{"service":"sqs","name":"sqs-generated","generated":true,"state":"candidate","suites":["cli"],"tests":[{"name":"SendMessage"}]}]}`
	writeRegistryPair(t, handWrittenOnly, gen)

	reg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want nil error", err)
	}
	if len(reg.Groups) != 2 {
		t.Fatalf("Load() groups = %+v, want 2", reg.Groups)
	}
	if reg.Groups[0].Name != "s3-crud" || reg.Groups[1].Name != "sqs-generated" {
		t.Errorf("Load() order = [%s, %s], want hand-written first: [s3-crud, sqs-generated]",
			reg.Groups[0].Name, reg.Groups[1].Name)
	}
	gg := reg.Groups[1]
	if !gg.Generated || gg.State != "candidate" || len(gg.Suites) != 1 || gg.Suites[0] != "cli" {
		t.Errorf("generated group = %+v, want its generated/state/suites fields preserved", gg)
	}
}

// An unparsable generated file, a wrong version, and a group missing
// "generated", "state" or "suites" must each be a load error — the same
// posture as a malformed registry.json, not a silently-ignored file.
func TestLoadRejectsMalformedGeneratedFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		genBody string
		wantSub string
	}{
		{"unparsable", `{not json`, "parse"},
		{"wrong version", `{"version":2,"groups":[]}`, "version 2, want 1"},
		{"missing generated flag", `{"version":1,"groups":[{"name":"x","state":"candidate","suites":["cli"],"tests":[{"name":"A"}]}]}`, `missing "generated": true`},
		{"missing state", `{"version":1,"groups":[{"name":"x","generated":true,"suites":["cli"],"tests":[{"name":"A"}]}]}`, `missing "state"`},
		{"missing suites", `{"version":1,"groups":[{"name":"x","generated":true,"state":"candidate","tests":[{"name":"A"}]}]}`, `missing "suites"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeRegistryPair(t, handWrittenOnly, tc.genBody)
			_, err := Load()
			if err == nil {
				t.Fatalf("Load() = nil error, want error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("Load() error = %q, want it to contain %q", err, tc.wantSub)
			}
		})
	}
}

// A generated group name colliding with a hand-written one must be a load
// error naming the group: every gate file (baseline, flaky, parity debt) keys
// on suite/group/test with no notion of which file a group came from, so a
// collision would merge two different tests into one entry instead of
// conflicting.
func TestLoadRejectsGeneratedGroupCollidingWithHandWritten(t *testing.T) {
	gen := `{"version":1,"groups":[{"service":"s3","name":"s3-crud","generated":true,"state":"candidate","suites":["cli"],"tests":[{"name":"CreateBucket"}]}]}`
	writeRegistryPair(t, handWrittenOnly, gen)

	_, err := Load()
	if err == nil {
		t.Fatal("Load() = nil error, want error naming the colliding group")
	}
	if !strings.Contains(err.Error(), `"s3-crud"`) {
		t.Errorf("Load() error = %q, does not name the colliding group", err)
	}
}

// A generated group whose `suites` excludes this suite must not be loaded at
// all: no tests, no skips, no results. This is the same mechanism as the
// cdk-lifecycle special case, generalised to any group.
func TestBuildGroupsExcludesGeneratedGroupOutOfSuiteScope(t *testing.T) {
	reg := &Registry{Groups: []RegistryGroup{
		{Service: "sqs", Name: "sqs-generated", Generated: true, State: "candidate",
			Suites: []string{"python-sdk"}, Tests: []RegistryTest{{Name: "SendMessage"}}},
	}}

	groups := BuildGroups(reg, ImplMap{}, BuildGroupsOptions{Suite: "cli"})
	if len(groups) != 0 {
		t.Fatalf("BuildGroups() = %d groups, want 0 (sqs-generated is scoped to python-sdk only)", len(groups))
	}
}

// A generated group's `suites` including this suite must be loaded, and its
// tests keep running through the ordinary impl-resolution machinery.
func TestBuildGroupsIncludesGeneratedGroupInSuiteScope(t *testing.T) {
	reg := &Registry{Groups: []RegistryGroup{
		{Service: "sqs", Name: "sqs-generated", Generated: true, State: "candidate",
			Suites: []string{"cli"}, Tests: []RegistryTest{{Name: "SendMessage"}}},
	}}

	groups := BuildGroups(reg, ImplMap{}, BuildGroupsOptions{Suite: "cli"})
	if len(groups) != 1 {
		t.Fatalf("BuildGroups() = %d groups, want 1", len(groups))
	}
}

// A generated test with no static impl and no scenario backend must fail
// loudly with the exact interim-rule message — never skip, never na, never
// pass. This is the mechanism that keeps a coverage hole from reporting as
// success until the G2 interpreters land.
func TestBuildGroupsFailsGeneratedTestWithNoBackend(t *testing.T) {
	reg := &Registry{Groups: []RegistryGroup{
		{Service: "sqs", Name: "sqs-generated", Generated: true, State: "candidate",
			Suites: []string{"cli"}, Tests: []RegistryTest{{Name: "SendMessage"}}},
	}}

	groups := BuildGroups(reg, ImplMap{}, BuildGroupsOptions{Suite: "cli"})
	tc := findTest(t, groups, "sqs-generated", "SendMessage")

	if tc.Skip != "" {
		t.Errorf("SendMessage.Skip = %q, want empty — a generated test with no backend must fail, not skip", tc.Skip)
	}
	if tc.NA != "" {
		t.Errorf("SendMessage.NA = %q, want empty — a generated test with no backend must fail, not na", tc.NA)
	}
	if tc.Fn == nil {
		t.Fatal("SendMessage.Fn = nil, want a failing TestFn")
	}
	err := tc.Fn(context.Background(), nil)
	if err == nil {
		t.Fatal("SendMessage.Fn(...) = nil error, want the interim-rule failure")
	}
	want := `generated group "sqs-generated" is scoped to cli but cli has no scenario backend`
	if err.Error() != want {
		t.Errorf("SendMessage.Fn(...) error = %q, want %q", err, want)
	}
}

// A registered scenario backend takes priority over the interim fail rule —
// once a G2 interpreter is wired up, a generated test it resolves must run
// normally rather than fail.
func TestBuildGroupsUsesScenarioBackendWhenResolved(t *testing.T) {
	reg := &Registry{Groups: []RegistryGroup{
		{Service: "sqs", Name: "sqs-generated", Generated: true, State: "candidate",
			Suites: []string{"cli"}, Tests: []RegistryTest{{Name: "SendMessage"}}},
	}}
	backend := ScenarioBackend(func(group RegistryGroup, test RegistryTest) (harness.TestFn, bool) {
		if group.Name == "sqs-generated" && test.Name == "SendMessage" {
			return marker("resolved"), true
		}
		return nil, false
	})

	groups := BuildGroups(reg, ImplMap{}, BuildGroupsOptions{Suite: "cli", Scenario: backend})
	tc := findTest(t, groups, "sqs-generated", "SendMessage")
	if tc.Skip != "" || tc.NA != "" {
		t.Fatalf("SendMessage = %+v, want bound to the scenario backend, not skipped or na", tc)
	}
	tCtx := harness.NewTestContext("", "", "")
	if err := tc.Fn(context.Background(), tCtx); err != nil {
		t.Fatalf("SendMessage.Fn(...) = %v, want nil (bound to the backend's impl)", err)
	}
	if got := tCtx.GetString("ran"); got != "resolved" {
		t.Errorf("ran marker = %q, want %q — the scenario backend's impl did not run", got, "resolved")
	}
}

// A hand-written (non-generated) group's test with no static impl must also
// resolve through a registered scenario backend — not only a generated
// group's. This is the G6 port path (docs/plans/compat-coverage-modelgen.md
// § 3.11): a hand-written group ported to an authored scenario keeps its
// registry group/test names, so the loader must consult opts.Scenario for it
// exactly as it does for a generated group, with no special-casing on
// rg.Generated.
func TestBuildGroupsUsesScenarioBackendForHandWrittenGroup(t *testing.T) {
	reg := &Registry{Groups: []RegistryGroup{
		{Service: "sqs", Name: "sqs-queues", Tests: []RegistryTest{{Name: "SendMessage"}}},
	}}
	backend := ScenarioBackend(func(group RegistryGroup, test RegistryTest) (harness.TestFn, bool) {
		if group.Name == "sqs-queues" && test.Name == "SendMessage" {
			return marker("resolved"), true
		}
		return nil, false
	})

	groups := BuildGroups(reg, ImplMap{}, BuildGroupsOptions{Suite: "cli", Scenario: backend})
	tc := findTest(t, groups, "sqs-queues", "SendMessage")
	if tc.Skip != "" || tc.NA != "" {
		t.Fatalf("SendMessage = %+v, want bound to the scenario backend, not skipped or na", tc)
	}
	tCtx := harness.NewTestContext("", "", "")
	if err := tc.Fn(context.Background(), tCtx); err != nil {
		t.Fatalf("SendMessage.Fn(...) = %v, want nil (bound to the backend's impl)", err)
	}
	if got := tCtx.GetString("ran"); got != "resolved" {
		t.Errorf("ran marker = %q, want %q — the scenario backend's impl did not run", got, "resolved")
	}
}
