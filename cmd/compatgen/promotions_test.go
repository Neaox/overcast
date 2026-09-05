//go:build dev

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The soak ledger is the only input that can move a generated group's `state`,
// and `state` is the only thing it may move. Both halves are asserted here: a
// ledger that changed anything else would be a second author of a generated
// file, which is exactly what routing promotion through an input was meant to
// prevent.

func TestPromotions_changeTheStateAndNothingElse(t *testing.T) {
	_, gen := generateFixture(t)
	scenarios := []*scenario{gen.scenario}
	backends := []string{"cli", "python-sdk"}
	promoted := gen.scenario.Groups[0].Name

	before, err := encodeDocument(buildRegistry(scenarios, backends, nil))
	if err != nil {
		t.Fatal(err)
	}
	ledger := &promotionsFile{
		Version: promotionsVersion,
		Groups: map[string]promotionEntry{
			promoted: {State: generatedStateGated, FirstSeen: "2026-09-01", PromotedAt: "2026-09-05", Runs: []string{"run-1"}},
		},
	}
	after, err := encodeDocument(buildRegistry(scenarios, backends, ledger))
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(before, after) {
		t.Fatalf("gating %s in the ledger did not change the generated registry", promoted)
	}
	changed := changedLines(string(before), string(after))
	if len(changed) != 1 {
		t.Fatalf("a promotion changed %d line(s), want exactly one:\n%s", len(changed), strings.Join(changed, "\n"))
	}
	if !strings.Contains(changed[0], `"state"`) || !strings.Contains(changed[0], generatedStateGated) {
		t.Fatalf("the changed line is %q, want the group's state", changed[0])
	}

	// And the group that was promoted is the one named, not whichever came
	// first in the file.
	for _, g := range buildRegistry(scenarios, backends, ledger).Groups {
		want := generatedStateCandidate
		if g.Name == promoted {
			want = generatedStateGated
		}
		if g.State != want {
			t.Errorf("group %s state = %q, want %q", g.Name, g.State, want)
		}
	}
}

// changedLines returns the lines of after that differ from before at the same
// index, plus any surplus. The two documents differ only by a value here, so a
// positional diff is enough and says more than a byte count.
func changedLines(before, after string) []string {
	oldLines := strings.Split(before, "\n")
	newLines := strings.Split(after, "\n")
	var changed []string
	for i, line := range newLines {
		if i >= len(oldLines) || oldLines[i] != line {
			changed = append(changed, strings.TrimSpace(line))
		}
	}
	if len(oldLines) > len(newLines) {
		changed = append(changed, oldLines[len(newLines):]...)
	}
	return changed
}

// TestPromotions_defaultToCandidate — a group nothing has recorded gates
// nothing. This is the state machine's safe default and the reason a missing
// ledger file is not an error.
func TestPromotions_defaultToCandidate(t *testing.T) {
	_, gen := generateFixture(t)
	empty := &promotionsFile{Version: promotionsVersion, Groups: map[string]promotionEntry{}}
	for _, source := range []*promotionsFile{nil, empty} {
		for _, g := range buildRegistry([]*scenario{gen.scenario}, []string{"cli"}, source).Groups {
			if g.State != generatedStateCandidate {
				t.Errorf("group %s state = %q with ledger %v, want %q", g.Name, g.State, source, generatedStateCandidate)
			}
		}
	}
}

// TestPromotions_regenerationIsStillByteIdentical is compat/model/README.md's
// determinism promise, asserted across a promotion: the ledger is an input, so
// generating from it twice must produce the same bytes and `-check` must stay
// clean on a tree whose ledger has moved.
func TestPromotions_regenerationIsStillByteIdentical(t *testing.T) {
	// The committed backend table is still empty, so the registry the
	// generator writes is empty too and would prove nothing about `state`.
	// Standing a backend up for the duration of this test is what makes the
	// registry carry the promoted group.
	restore := scenarioBackends
	scenarioBackends = []string{"cli", "python-sdk"}
	t.Cleanup(func() { scenarioBackends = restore })

	root := copyCorpus(t)
	group := firstScenarioGroup(t, root)
	writeFile(t, filepath.Join(root, filepath.FromSlash(promotionsPath)), `{
  "version": 1,
  "groups": {
    "`+group+`": {
      "state": "gated",
      "firstSeen": "2026-09-01",
      "promotedAt": "2026-09-05",
      "runs": ["run-1", "run-2", "run-3"]
    }
  }
}
`)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("regenerate: code=%d stderr=%s", code, stderr.String())
	}
	registry := string(readFile(t, filepath.Join(root, filepath.FromSlash(registryPath))))
	if !strings.Contains(registry, `"`+generatedStateGated+`"`) {
		t.Fatalf("the regenerated registry does not carry the promotion:\n%s", registry)
	}
	// The promise compat/model/README.md makes: regeneration is a pure
	// function of committed inputs, and the ledger is now one of them.
	if code := run([]string{"-root", root, "-check"}, &stdout, &stderr); code != 0 {
		t.Fatalf("-check after a promotion: code=%d stderr=%s", code, stderr.String())
	}
}

// TestPromotions_refuseAnEntryForAnUnknownGroup. A promotion left behind by a
// renamed group is not inert: the next group to be given that name would be
// born gated without ever soaking.
func TestPromotions_refuseAnEntryForAnUnknownGroup(t *testing.T) {
	root := copyCorpus(t)
	writeFile(t, filepath.Join(root, filepath.FromSlash(promotionsPath)), `{
  "version": 1,
  "groups": {
    "sqs-gen-nothing": { "state": "gated", "firstSeen": "2026-09-01", "promotedAt": "2026-09-05", "runs": ["run-1"] }
  }
}
`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root, "-check"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "sqs-gen-nothing") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

// TestPromotions_rejectAMalformedLedger proves the schema is actually applied
// on load, not merely committed next to the file.
func TestPromotions_rejectAMalformedLedger(t *testing.T) {
	root := copyCorpus(t)
	writeFile(t, filepath.Join(root, filepath.FromSlash(promotionsPath)), `{
  "version": 1,
  "groups": { "sqs-gen-queue": { "state": "gated", "firstSeen": "2026-09-01" } }
}
`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root, "-check"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "promotions.schema.json") {
		t.Fatalf("a gated entry with no evidence was accepted: code=%d stderr=%s", code, stderr.String())
	}
}

// firstScenarioGroup names a group the committed corpus really produces.
func firstScenarioGroup(t *testing.T, root string) string {
	t.Helper()
	c, err := loadCorpus(root)
	if err != nil {
		t.Fatal(err)
	}
	generations, _, err := generateAll(root, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, gen := range generations {
		if len(gen.scenario.Groups) > 0 {
			return gen.scenario.Groups[0].Name
		}
	}
	t.Fatal("the committed corpus produces no groups")
	return ""
}
