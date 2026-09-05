package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/compat"
)

// The soak's decision is the whole of --promote-generated's risk: it is what
// puts a mechanically generated test into an absolute gate on the strength of
// three runs nobody watched. Every rule it applies is asserted here, including
// the ones whose absence would look like it was working.

const promoteTestGroup = "sqs-gen-queue"

// candidateFixture is the registry the decision tests run against: one
// candidate group, two suites, two tests.
func candidateFixture() *generatedRegistry {
	return &generatedRegistry{
		Version: generatedRegistryVersion,
		Groups: []generatedGroup{{
			Service:   "sqs",
			Name:      promoteTestGroup,
			Generated: true,
			Scenario:  "compat/model/scenarios/sqs.json",
			State:     generatedStateCandidate,
			Suites:    []string{"cli", "python-sdk"},
			Tests:     []generatedTest{{Name: "CreateQueue"}, {Name: "SendMessage"}},
		}},
	}
}

// soakRuns builds n runs in which every (suite, test) of the fixture group
// answers `status`, then applies the overrides — each one replacing the status
// of a single (run, suite, test).
type statusOverride struct {
	run    int
	suite  string
	test   string
	status compat.Status
	// drop removes the result instead of changing it.
	drop bool
}

func soakRuns(n int, status compat.Status, overrides ...statusOverride) []promotionRun {
	runs := make([]promotionRun, 0, n)
	for i := range n {
		var specs []resultSpec
		for _, suite := range []string{"cli", "python-sdk"} {
			for _, test := range []string{"CreateQueue", "SendMessage"} {
				spec := resultSpec{suite: suite, service: "sqs", group: promoteTestGroup, test: test, status: status}
				skip := false
				for _, o := range overrides {
					if o.run != i || o.suite != suite || o.test != test {
						continue
					}
					if o.drop {
						skip = true
						break
					}
					spec.status = o.status
				}
				if !skip {
					specs = append(specs, spec)
				}
			}
		}
		runs = append(runs, promotionRun{
			ID:     "run-" + string(rune('1'+i)),
			Report: reportWithResults(specs...),
		})
	}
	return runs
}

func TestDecidePromotion(t *testing.T) {
	tests := []struct {
		name        string
		runs        []promotionRun
		minRuns     int
		wantPromote bool
		wantBlocker string
	}{
		{
			name:        "three identical passing runs promote",
			runs:        soakRuns(3, compat.StatusPass),
			minRuns:     3,
			wantPromote: true,
		},
		{
			name:        "a consistently unimplemented group still promotes",
			runs:        soakRuns(3, compat.StatusUnimplemented),
			minRuns:     3,
			wantPromote: true,
		},
		{
			name: "one failing run blocks",
			runs: soakRuns(3, compat.StatusPass,
				statusOverride{run: 1, suite: "cli", test: "SendMessage", status: compat.StatusFail}),
			minRuns:     3,
			wantBlocker: "cli/SendMessage answered pass, then fail across 3 run(s)",
		},
		{
			name:        "a consistent failure blocks — consistency is not the only rule",
			runs:        soakRuns(3, compat.StatusFail),
			minRuns:     3,
			wantBlocker: "cli/CreateQueue failed in every run",
		},
		{
			name: "one flip between two non-failing statuses blocks",
			runs: soakRuns(3, compat.StatusPass,
				statusOverride{run: 2, suite: "python-sdk", test: "CreateQueue", status: compat.StatusSkip}),
			minRuns:     3,
			wantBlocker: "python-sdk/CreateQueue answered pass, then skip across 3 run(s)",
		},
		{
			name:        "fewer runs than the soak needs blocks",
			runs:        soakRuns(2, compat.StatusPass),
			minRuns:     3,
			wantBlocker: "only 2 of the 3 run(s) the soak needs were available",
		},
		{
			name: "a suite missing from one run blocks",
			runs: soakRuns(3, compat.StatusPass,
				statusOverride{run: 1, suite: "python-sdk", test: "CreateQueue", drop: true},
				statusOverride{run: 1, suite: "python-sdk", test: "SendMessage", drop: true}),
			minRuns:     3,
			wantBlocker: "python-sdk/CreateQueue has no result in run run-2",
		},
		{
			name: "a single test missing from one run blocks",
			runs: soakRuns(3, compat.StatusPass,
				statusOverride{run: 0, suite: "cli", test: "SendMessage", drop: true}),
			minRuns:     3,
			wantBlocker: "cli/SendMessage has no result in run run-1",
		},
		{
			name:        "no runs at all blocks",
			runs:        nil,
			minRuns:     3,
			wantBlocker: "only 0 of the 3 run(s) the soak needs were available",
		},
		{
			// A caller asking for no evidence at all still gets none.
			name:        "no runs blocks even when the minimum is zero",
			runs:        nil,
			minRuns:     0,
			wantBlocker: "only 0 of the 1 run(s) the soak needs were available",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			verdict := decidePromotion(candidateFixture().Groups[0], tc.runs, tc.minRuns)
			if verdict.Promote != tc.wantPromote {
				t.Fatalf("Promote = %v, want %v (blockers: %v)", verdict.Promote, tc.wantPromote, verdict.Blockers)
			}
			if tc.wantBlocker == "" {
				if len(verdict.Blockers) != 0 {
					t.Fatalf("blockers = %v, want none", verdict.Blockers)
				}
				return
			}
			if !containsSubstring(verdict.Blockers, tc.wantBlocker) {
				t.Fatalf("blockers = %v, want one containing %q", verdict.Blockers, tc.wantBlocker)
			}
		})
	}
}

func containsSubstring(lines []string, want string) bool {
	for _, line := range lines {
		if strings.Contains(line, want) {
			return true
		}
	}
	return false
}

// TestApplyPromotions_recordsFirstSeenOnFirstObservation pins the other half of
// the ledger: a candidate that does not promote still has to be written down,
// or it can never age out and the overdue nag has nothing to measure against.
func TestApplyPromotions_recordsFirstSeenOnFirstObservation(t *testing.T) {
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	ledger := &promotionsFile{Version: promotionsVersion, Groups: map[string]promotionEntry{}}

	outcome := applyPromotions(candidateFixture(), ledger, soakRuns(2, compat.StatusPass), 3, now)

	entry, ok := outcome.File.Groups[promoteTestGroup]
	if !ok {
		t.Fatal("the candidate was not recorded")
	}
	if entry.State != generatedStateCandidate || entry.FirstSeen != "2026-09-05" {
		t.Fatalf("entry = %+v, want a candidate first seen 2026-09-05", entry)
	}
	if entry.PromotedAt != "" || len(entry.Runs) != 0 {
		t.Fatalf("entry = %+v, want no promotion evidence", entry)
	}
	if len(outcome.Promoted) != 0 || len(outcome.Blocked) != 1 {
		t.Fatalf("promoted = %v, blocked = %v", outcome.Promoted, outcome.Blocked)
	}
}

// TestApplyPromotions_keepsTheOriginalFirstSeen — the date is written once. If
// each run rewrote it, no candidate could ever reach thirty days old.
func TestApplyPromotions_keepsTheOriginalFirstSeen(t *testing.T) {
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	ledger := &promotionsFile{Version: promotionsVersion, Groups: map[string]promotionEntry{
		promoteTestGroup: {State: generatedStateCandidate, FirstSeen: "2026-08-01"},
	}}

	outcome := applyPromotions(candidateFixture(), ledger, soakRuns(3, compat.StatusFail), 3, now)

	if got := outcome.File.Groups[promoteTestGroup].FirstSeen; got != "2026-08-01" {
		t.Fatalf("firstSeen = %q, want the original 2026-08-01", got)
	}
}

func TestApplyPromotions_promotesWithEvidence(t *testing.T) {
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	ledger := &promotionsFile{Version: promotionsVersion, Groups: map[string]promotionEntry{
		promoteTestGroup: {State: generatedStateCandidate, FirstSeen: "2026-09-01"},
	}}

	outcome := applyPromotions(candidateFixture(), ledger, soakRuns(3, compat.StatusPass), 3, now)

	if len(outcome.Promoted) != 1 || outcome.Promoted[0] != promoteTestGroup {
		t.Fatalf("promoted = %v", outcome.Promoted)
	}
	entry := outcome.File.Groups[promoteTestGroup]
	if entry.State != generatedStateGated || entry.PromotedAt != "2026-09-05" || entry.FirstSeen != "2026-09-01" {
		t.Fatalf("entry = %+v", entry)
	}
	if strings.Join(entry.Runs, ",") != "run-1,run-2,run-3" {
		t.Fatalf("runs = %v, want the three run ids as evidence", entry.Runs)
	}
}

// TestApplyPromotions_leavesAGatedGroupAlone. Re-promoting an already-gated
// group would rewrite its evidence with whatever ran last night, quietly
// replacing the runs that earned it — and on a night when it failed, it would
// look like it had just promoted on a failure.
func TestApplyPromotions_leavesAGatedGroupAlone(t *testing.T) {
	now := time.Date(2026, 10, 20, 3, 0, 0, 0, time.UTC)
	gated := promotionEntry{
		State:      generatedStateGated,
		FirstSeen:  "2026-09-01",
		PromotedAt: "2026-09-04",
		Runs:       []string{"old-1", "old-2", "old-3"},
	}
	ledger := &promotionsFile{Version: promotionsVersion, Groups: map[string]promotionEntry{promoteTestGroup: gated}}

	// The registry still says candidate: that is the window between a
	// promotion and the regeneration that applies it, and the ledger has to
	// win there or the group promotes twice.
	outcome := applyPromotions(candidateFixture(), ledger, soakRuns(3, compat.StatusFail), 3, now)

	if len(outcome.Promoted) != 0 || len(outcome.Blocked) != 0 || len(outcome.Overdue) != 0 {
		t.Fatalf("a gated group was reconsidered: %+v", outcome)
	}
	if got := outcome.File.Groups[promoteTestGroup]; got.PromotedAt != gated.PromotedAt ||
		strings.Join(got.Runs, ",") != strings.Join(gated.Runs, ",") {
		t.Fatalf("entry = %+v, want %+v", got, gated)
	}
}

// TestApplyPromotions_reportsOverdueAtThirtyDays uses an injected clock, so the
// deadline is asserted rather than waited for.
func TestApplyPromotions_reportsOverdueAtThirtyDays(t *testing.T) {
	firstSeen := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		now         time.Time
		wantOverdue bool
	}{
		{name: "exactly at the deadline is not yet overdue", now: firstSeen.AddDate(0, 0, candidateOverdueDays)},
		{name: "one day past the deadline is overdue", now: firstSeen.AddDate(0, 0, candidateOverdueDays+1), wantOverdue: true},
		{name: "well inside the deadline is not overdue", now: firstSeen.AddDate(0, 0, 3)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ledger := &promotionsFile{Version: promotionsVersion, Groups: map[string]promotionEntry{
				promoteTestGroup: {State: generatedStateCandidate, FirstSeen: firstSeen.Format(promotionDateLayout)},
			}}
			outcome := applyPromotions(candidateFixture(), ledger, soakRuns(3, compat.StatusPass,
				statusOverride{run: 1, suite: "cli", test: "SendMessage", status: compat.StatusFail}),
				3, tc.now)
			if got := len(outcome.Overdue) > 0; got != tc.wantOverdue {
				t.Fatalf("overdue = %v (%v), want %v", got, outcome.Overdue, tc.wantOverdue)
			}
			// An overdue report is only useful if it says which test cannot
			// make its mind up.
			if tc.wantOverdue && !containsSubstring(outcome.Overdue, "cli/SendMessage") {
				t.Fatalf("overdue = %v, want the offending (suite, test) named", outcome.Overdue)
			}
		})
	}
}

// TestApplyPromotions_prunesAGroupThatNoLongerExists. A promotion left behind
// by a renamed group is worse than untidy: the next group given that name would
// inherit a gated state it never earned.
func TestApplyPromotions_prunesAGroupThatNoLongerExists(t *testing.T) {
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	ledger := &promotionsFile{Version: promotionsVersion, Groups: map[string]promotionEntry{
		promoteTestGroup: {State: generatedStateCandidate, FirstSeen: "2026-09-01"},
		"sqs-gen-gone":   {State: generatedStateGated, FirstSeen: "2026-01-01", PromotedAt: "2026-01-04", Runs: []string{"x"}},
	}}

	outcome := applyPromotions(candidateFixture(), ledger, soakRuns(3, compat.StatusPass), 3, now)

	if _, still := outcome.File.Groups["sqs-gen-gone"]; still {
		t.Fatal("a promotion for a group no scenario produces survived")
	}
	if len(outcome.Pruned) != 1 || outcome.Pruned[0] != "sqs-gen-gone" {
		t.Fatalf("pruned = %v", outcome.Pruned)
	}
}

// ---------------------------------------------------------------------------
// The CLI entry point
// ---------------------------------------------------------------------------

// TestPromoteGeneratedFile_isANoOpWithNoGeneratedGroups is the property the
// nightly rests on: nothing to promote must leave the tree byte-identical, or
// the bot opens an empty pull request every morning. It is also the guard that
// stops an empty registry — which is what the tree ships today — from being
// read as "every promotion is stale" and emptying the ledger.
func TestPromoteGeneratedFile_isANoOpWithNoGeneratedGroups(t *testing.T) {
	dir := t.TempDir()
	promotions := filepath.Join(dir, "promotions.json")
	seed := `{
  "version": 1,
  "groups": {
    "sqs-gen-queue": {
      "state": "gated",
      "firstSeen": "2026-09-01",
      "promotedAt": "2026-09-04",
      "runs": ["run-1"]
    }
  }
}
`
	writeTempFile(t, promotions, seed)
	registry := writeTempJSON(t, "registry.json", &parityRegistry{})
	generated := writeTempJSON(t, "registry.generated.json", &generatedRegistry{Version: generatedRegistryVersion})

	var out strings.Builder
	if err := promoteGeneratedFile(promotions, registry, generated, "", 3, false, &out); err != nil {
		t.Fatalf("promoteGeneratedFile() error = %v", err)
	}
	if got := readTempFile(t, promotions); got != seed {
		t.Fatalf("the ledger was rewritten:\n%s", got)
	}
	if !strings.Contains(out.String(), "nothing to promote") {
		t.Fatalf("output = %q", out.String())
	}
}

// TestPromoteGeneratedFile_writesOnlyTheLedger walks the whole path once, and
// asserts the design constraint the ledger exists for: --promote-generated
// never touches the generated registry, which cmd/compatgen owns.
func TestPromoteGeneratedFile_writesOnlyTheLedger(t *testing.T) {
	dir := t.TempDir()
	promotions := filepath.Join(dir, "promotions.json")
	registry := writeTempJSON(t, "registry.json", &parityRegistry{})
	generated := writeTempJSON(t, "registry.generated.json", candidateFixture())
	generatedBefore := readTempFile(t, generated)

	for i := range 3 {
		writeTempFile(t, filepath.Join(dir, "runs", "run-"+string(rune('1'+i))+".json"),
			marshalIndent(t, reportWithResults(
				resultSpec{suite: "cli", service: "sqs", group: promoteTestGroup, test: "CreateQueue", status: compat.StatusPass},
				resultSpec{suite: "cli", service: "sqs", group: promoteTestGroup, test: "SendMessage", status: compat.StatusPass},
				resultSpec{suite: "python-sdk", service: "sqs", group: promoteTestGroup, test: "CreateQueue", status: compat.StatusPass},
				resultSpec{suite: "python-sdk", service: "sqs", group: promoteTestGroup, test: "SendMessage", status: compat.StatusPass},
			)))
	}

	var out strings.Builder
	if err := promoteGeneratedFile(promotions, registry, generated, filepath.Join(dir, "runs"), 3, false, &out); err != nil {
		t.Fatalf("promoteGeneratedFile() error = %v", err)
	}

	if got := readTempFile(t, generated); got != generatedBefore {
		t.Fatal("--promote-generated rewrote registry.generated.json; cmd/compatgen owns that file")
	}
	var ledger promotionsFile
	if err := json.Unmarshal([]byte(readTempFile(t, promotions)), &ledger); err != nil {
		t.Fatalf("the ledger is not valid JSON: %v", err)
	}
	entry := ledger.Groups[promoteTestGroup]
	if entry.State != generatedStateGated {
		t.Fatalf("entry = %+v, want gated", entry)
	}
	if strings.Join(entry.Runs, ",") != "run-1,run-2,run-3" {
		t.Fatalf("runs = %v, want the directory's three reports in name order", entry.Runs)
	}

	// A second run over the same evidence must change nothing at all — the
	// nightly opens a pull request only when the bytes move.
	before := readTempFile(t, promotions)
	out.Reset()
	if err := promoteGeneratedFile(promotions, registry, generated, filepath.Join(dir, "runs"), 3, false, &out); err != nil {
		t.Fatalf("second promoteGeneratedFile() error = %v", err)
	}
	if readTempFile(t, promotions) != before {
		t.Fatal("a second identical soak rewrote the ledger")
	}
	if !strings.Contains(out.String(), "unchanged") {
		t.Fatalf("output = %q", out.String())
	}
}

func writeTempFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTempFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func marshalIndent(t *testing.T, value any) string {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
