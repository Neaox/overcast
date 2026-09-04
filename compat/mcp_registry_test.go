package compat

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── registry.generated.json (#1393) ──────────────────────────────────────
//
// loadRegistry is the agent-facing test listing every compat_* MCP tool
// reads. Loading rules from the shared loader contract: a missing generated
// file is a no-op; the checked-in empty file is a no-op; a synthetic
// non-empty file concatenates after the hand-written groups; a malformed
// generated file (bad version, or a group missing generated/state/suites) is
// a load error; and a name collision between the two files is a load error.
// loadRegistry itself never runs a test, so the interim "fail" rule the suite
// loaders apply to an unbacked generated test has no analogue here — see
// TestToolListTestsHonoursSuitesScoping for the "suites" scoping it does
// apply.

// writeRegistryPair writes a hand-written registry.json and, if genBody is
// non-empty, a sibling registry.generated.json in the same temp dir. It
// returns the hand-written file's path, which is what a *compatMCPProvider
// resolves both files from.
func writeRegistryPair(t *testing.T, handBody, genBody string) string {
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
	return handPath
}

const handWrittenOnlyRegistry = `{"version":1,"groups":[{"service":"s3","name":"s3-crud","tests":[{"name":"CreateBucket"}]}]}`

// A missing registry.generated.json must be a no-op: loadRegistry() returns
// exactly the hand-written groups, unchanged.
func TestLoadRegistryMissingGeneratedFileIsNoOp(t *testing.T) {
	p := &compatMCPProvider{registryPath: writeRegistryPair(t, handWrittenOnlyRegistry, "")}

	reg, err := p.loadRegistry()
	if err != nil {
		t.Fatalf("loadRegistry() with no generated file = %v, want nil error", err)
	}
	if len(reg.Groups) != 1 || reg.Groups[0].Name != "s3-crud" {
		t.Fatalf("loadRegistry() groups = %+v, want only the hand-written s3-crud group", reg.Groups)
	}
}

// The checked-in empty file ({"version":1,"groups":[]}) must also be a no-op.
// Asserting only this invariant — not that the real on-disk file is currently
// empty — keeps the test from pinning a fact the G2 pilot is about to change.
func TestLoadRegistryEmptyGeneratedFileIsNoOp(t *testing.T) {
	p := &compatMCPProvider{registryPath: writeRegistryPair(t, handWrittenOnlyRegistry, `{"version":1,"groups":[]}`)}

	reg, err := p.loadRegistry()
	if err != nil {
		t.Fatalf("loadRegistry() with empty generated file = %v, want nil error", err)
	}
	if len(reg.Groups) != 1 || reg.Groups[0].Name != "s3-crud" {
		t.Fatalf("loadRegistry() groups = %+v, want only the hand-written s3-crud group", reg.Groups)
	}
}

// A synthetic non-empty generated file must be concatenated after the
// hand-written groups, in file order, with its generated/state/suites facet
// fields intact.
func TestLoadRegistryConcatenatesGeneratedGroupsAfterHandWritten(t *testing.T) {
	gen := `{"version":1,"groups":[{"service":"sqs","name":"sqs-generated","generated":true,"state":"candidate","suites":["go-sdk"],"tests":[{"name":"SendMessage"}]}]}`
	p := &compatMCPProvider{registryPath: writeRegistryPair(t, handWrittenOnlyRegistry, gen)}

	reg, err := p.loadRegistry()
	if err != nil {
		t.Fatalf("loadRegistry() = %v, want nil error", err)
	}
	if len(reg.Groups) != 2 {
		t.Fatalf("loadRegistry() groups = %+v, want 2", reg.Groups)
	}
	if reg.Groups[0].Name != "s3-crud" || reg.Groups[1].Name != "sqs-generated" {
		t.Errorf("loadRegistry() order = [%s, %s], want hand-written first: [s3-crud, sqs-generated]",
			reg.Groups[0].Name, reg.Groups[1].Name)
	}
	gg := reg.Groups[1]
	if !gg.Generated || gg.State != "candidate" || len(gg.Suites) != 1 || gg.Suites[0] != "go-sdk" {
		t.Errorf("generated group = %+v, want its generated/state/suites fields preserved", gg)
	}
}

// An unparsable generated file, a wrong version, and a group missing
// "generated", "state" or "suites" must each be a load error — the same
// posture as a malformed registry.json, not a silently-ignored file.
func TestLoadRegistryRejectsMalformedGeneratedFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		genBody string
		wantSub string
	}{
		{"unparsable", `{not json`, "invalid character"},
		{"wrong version", `{"version":2,"groups":[]}`, "version 2, want 1"},
		{"missing generated flag", `{"version":1,"groups":[{"name":"x","state":"candidate","suites":["go-sdk"],"tests":[{"name":"A"}]}]}`, `missing "generated": true`},
		{"missing state", `{"version":1,"groups":[{"name":"x","generated":true,"suites":["go-sdk"],"tests":[{"name":"A"}]}]}`, `missing "state"`},
		{"missing suites", `{"version":1,"groups":[{"name":"x","generated":true,"state":"candidate","tests":[{"name":"A"}]}]}`, `missing "suites"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &compatMCPProvider{registryPath: writeRegistryPair(t, handWrittenOnlyRegistry, tc.genBody)}
			_, err := p.loadRegistry()
			if err == nil {
				t.Fatalf("loadRegistry() = nil error, want error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("loadRegistry() error = %q, want it to contain %q", err, tc.wantSub)
			}
		})
	}
}

// A generated group name colliding with a hand-written one must be a load
// error naming the group: every caller of this listing resolves a group by
// name alone, so a collision would merge two different tests into one entry
// instead of conflicting.
func TestLoadRegistryRejectsGeneratedGroupCollidingWithHandWritten(t *testing.T) {
	gen := `{"version":1,"groups":[{"service":"s3","name":"s3-crud","generated":true,"state":"candidate","suites":["go-sdk"],"tests":[{"name":"CreateBucket"}]}]}`
	p := &compatMCPProvider{registryPath: writeRegistryPair(t, handWrittenOnlyRegistry, gen)}

	_, err := p.loadRegistry()
	if err == nil {
		t.Fatal("loadRegistry() = nil error, want error naming the colliding group")
	}
	if !strings.Contains(err.Error(), `"s3-crud"`) {
		t.Errorf("loadRegistry() error = %q, does not name the colliding group", err)
	}
}

// toolListTests must honour a generated group's "suites" scoping when a
// caller filters by suite: a group naming specific suites is out of scope for
// every other suite, so listing tests for "go-sdk" must not surface a group
// scoped to "python-sdk" only. Unscoped filtering (no suite given) still shows
// every group, generated included.
func TestToolListTestsHonoursSuitesScoping(t *testing.T) {
	hand := `{"version":1,"groups":[{"service":"s3","name":"s3-crud","tests":[{"name":"CreateBucket"}]}]}`
	gen := `{"version":1,"groups":[{"service":"sqs","name":"sqs-generated","generated":true,"state":"candidate","suites":["python-sdk"],"tests":[{"name":"SendMessage"}]}]}`
	regPath := writeRegistryPair(t, hand, gen)

	orch := NewOrchestrator(context.Background(), nil, func([]byte) {}, slog.New(slog.DiscardHandler))
	p := newCompatMCPProvider(orch, regPath)

	// Scoped to a suite the generated group does not name: the group must not
	// appear at all.
	entries := listTestsGroups(t, p, `{"suite":"go-sdk"}`)
	for _, g := range entries {
		if g == "sqs-generated" {
			t.Fatalf("toolListTests(suite=go-sdk) includes sqs-generated (scoped to python-sdk only): %v", entries)
		}
	}

	// No suite filter: every group is listed, generated included.
	entries = listTestsGroups(t, p, `{}`)
	found := false
	for _, g := range entries {
		if g == "sqs-generated" {
			found = true
		}
	}
	if !found {
		t.Errorf("toolListTests({}) omits sqs-generated; want every group listed with no suite filter: %v", entries)
	}
}

// listTestsGroups calls toolListTests and returns the "group" field of every
// entry. testEntry is declared inside toolListTests itself, so the result is
// read back through JSON rather than a direct type assertion.
func listTestsGroups(t *testing.T, p *compatMCPProvider, params string) []string {
	t.Helper()
	out, err := p.toolListTests(context.Background(), json.RawMessage(params))
	if err != nil {
		t.Fatalf("toolListTests(%s) = %v, want nil error", params, err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal toolListTests result: %v", err)
	}
	var entries []struct {
		Group string `json:"group"`
	}
	if err := json.Unmarshal(b, &entries); err != nil {
		t.Fatalf("unmarshal toolListTests result: %v", err)
	}
	groups := make([]string, len(entries))
	for i, e := range entries {
		groups[i] = e.Group
	}
	return groups
}
