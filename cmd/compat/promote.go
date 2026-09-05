// cmd/compat/promote.go — the candidate → gated soak for generated groups.
//
// A generated group lands in state "candidate": it runs everywhere and reports
// everywhere but gates nothing (see generatedregistry.go). Something has to
// move it into the gate once it has proved it answers the same way every time,
// and that something must be mechanical — the reviewer decision already
// happened at recipe review, and a soak that needs a human to notice it passed
// is a soak nothing ever comes out of.
//
// --promote-generated is that step. It reads N consecutive nightly run reports,
// and promotes a candidate group when every (suite, test) in it answered
// identically across all N with no `fail` and no `skip`, every suite the group
// is scoped to reporting in every run. `unimplemented` is a promotable
// agreement — decidePromotion says why, and a Tier 0 probe group depends on it.
//
// It writes exactly one file: compat/model/promotions.json. It does **not**
// touch compat/suites/registry.generated.json, which cmd/compatgen owns and
// rewrites wholly from its inputs — the promotions ledger is one of those
// inputs, and the caller regenerates (`make generate-compat-model`) after this
// runs. Two tools writing one generated file would make every promotion
// indistinguishable from the hand edit `compatgen -check` exists to catch.
//
// See docs/plans/compat-coverage-modelgen.md § 3.6 and
// cmd/compatgen/promotions.go, which is the reader.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/overcast-sh/overcast/compat"
	compatmodel "github.com/overcast-sh/overcast/compat/model"
)

const (
	// promotionDateLayout matches flakyDateLayout: the two ledgers are read
	// side by side by anyone triaging a nightly, and a second date format
	// would be gratuitous.
	promotionDateLayout = "2006-01-02"
	// candidateOverdueDays is when a candidate that has not promoted starts
	// being reported. It matches flakyHardDeadlineDays deliberately: a month
	// is how long this repo already says a test may sit outside the gate
	// before someone has to re-argue it, and a candidate that cannot agree
	// with itself after thirty nightly runs is not soaking, it is stuck.
	candidateOverdueDays = 30
)

// ---------------------------------------------------------------------------
// compat/model/promotions.json
// ---------------------------------------------------------------------------

// The ledger's shape, its version, its strict reader and its canonical encoder
// live in compat/model (package compatmodel). cmd/compatgen reads the same
// file, and two hand-maintained copies of one schema in two `main` packages
// drift: the copy here had neither the version check nor the strict decode its
// sibling had, so a field a later schema added would have been dropped on read
// and erased on the write that follows it.

// ---------------------------------------------------------------------------
// The decision
// ---------------------------------------------------------------------------

// promotionRun is one nightly run: its identifier (used as evidence in the
// ledger) and the merged report of every suite in it.
type promotionRun struct {
	ID     string
	Report *compat.RunReport
}

// promotionVerdict is the decision for one candidate group.
type promotionVerdict struct {
	Group string
	// Promote is true when every rule held.
	Promote bool
	// Blockers name the offending (suite, test)s, one line each, so a stuck
	// candidate says which test could not make its mind up rather than only
	// that the group did not promote.
	Blockers []string
}

// runStatuses indexes one run's results by "suite/test" within one group.
func runStatuses(report *compat.RunReport, group string) map[string]compat.Status {
	out := make(map[string]compat.Status)
	if report == nil {
		return out
	}
	for _, suite := range report.Suites {
		if suite == nil {
			continue
		}
		for _, g := range suite.Groups {
			if g == nil || g.Name != group {
				continue
			}
			for _, test := range g.Tests {
				out[suite.Suite+"/"+test.Test] = test.Status
			}
		}
	}
	return out
}

// decidePromotion decides one group against the runs.
//
// The rule is deliberately the strictest reading of "it answered the same way
// every time", because the cost of getting it wrong is asymmetric: a group held
// back for another night costs nothing, while a group promoted on incomplete
// evidence puts a test that cannot decide into an absolute gate, and the next
// disagreement reds an unrelated pull request. So:
//
//   - Every suite the group is scoped to must have reported every test in the
//     group, in every run. A suite missing from one run is not "two runs of
//     evidence and one absence" — it is one run where the group was not
//     exercised at all, and the soak has not happened.
//   - Every (suite, test) must carry the same status in all N runs.
//   - No status may be `fail`. A consistent failure is consistent, and would
//     otherwise promote straight into a gate it immediately breaks.
//   - No status may be `skip` either. A skip is not an answer about the
//     operation — it is the suite saying it never asked. A group whose every
//     test skips in all N runs (a setup failing the same way three nights
//     running is the realistic case) is perfectly consistent and has been
//     exercised precisely zero times, so promoting it would gate on evidence
//     that nothing ran. It is reported like a flip, naming the (suite, test),
//     because the fix is the same: find out why, in the recipe or the emulator.
//
// `unimplemented` is the one agreement that is emphatically promotable, and
// nothing above should be read as doubting it. A Tier 0 probe group calls an
// operation the emulator does not implement, with identifiers that do not
// exist; a stable 501 from every suite in every run is that operation
// answering exactly as modelled, and gating on it is what makes the group
// catch the day it stops being true. A rule that held `unimplemented` back
// would leave the largest class of generated group outside the gate forever.
func decidePromotion(group generatedGroup, runs []promotionRun, minRuns int) promotionVerdict {
	verdict := promotionVerdict{Group: group.Name}
	// len(runs) == 0 is checked in its own right, not just against minRuns: a
	// caller that passed --promote-min-runs 0 would otherwise promote every
	// candidate on no evidence whatsoever, which is the one thing this
	// function exists to make impossible.
	if len(runs) == 0 || len(runs) < minRuns {
		verdict.Blockers = append(verdict.Blockers, fmt.Sprintf(
			"only %d of the %d run(s) the soak needs were available", len(runs), max(minRuns, 1)))
		return verdict
	}
	perRun := make([]map[string]compat.Status, len(runs))
	for i, run := range runs {
		perRun[i] = runStatuses(run.Report, group.Name)
	}
	for _, suite := range group.Suites {
		for _, test := range group.Tests {
			key := suite + "/" + test.Name
			var seen []compat.Status
			missing := false
			for i, statuses := range perRun {
				status, ok := statuses[key]
				if !ok {
					verdict.Blockers = append(verdict.Blockers, fmt.Sprintf(
						"%s has no result in run %s", key, runs[i].ID))
					missing = true
					continue
				}
				seen = append(seen, status)
			}
			if missing {
				continue
			}
			if distinct := distinctStatuses(seen); len(distinct) > 1 {
				verdict.Blockers = append(verdict.Blockers, fmt.Sprintf(
					"%s answered %s across %d run(s)", key, strings.Join(distinct, ", then "), len(runs)))
				continue
			}
			if seen[0] == compat.StatusFail {
				verdict.Blockers = append(verdict.Blockers, fmt.Sprintf(
					"%s failed in every run — a consistent failure is still a failure", key))
			}
			if seen[0] == compat.StatusSkip {
				verdict.Blockers = append(verdict.Blockers, fmt.Sprintf(
					"%s skipped in every run — nothing was exercised, so nothing soaked", key))
			}
		}
	}
	sort.Strings(verdict.Blockers)
	verdict.Promote = len(verdict.Blockers) == 0
	return verdict
}

// distinctStatuses lists the statuses seen, in first-seen order, collapsing
// repeats — so "pass, then fail" reads as the flip it is rather than as a
// transcript.
func distinctStatuses(statuses []compat.Status) []string {
	var out []string
	seen := make(map[compat.Status]bool, len(statuses))
	for _, status := range statuses {
		if seen[status] {
			continue
		}
		seen[status] = true
		out = append(out, string(status))
	}
	return out
}

// promotionOutcome is everything one --promote-generated run decided.
type promotionOutcome struct {
	// Promoted names the groups that entered the gate, sorted.
	Promoted []string
	// Blocked holds the verdict for every candidate that did not, sorted by
	// group.
	Blocked []promotionVerdict
	// Overdue names candidates first seen more than candidateOverdueDays ago,
	// with their age — the nag that stops a group that can never agree from
	// reporting forever and gating nothing.
	Overdue []string
	// Pruned names ledger entries dropped because no generated group answers
	// to them any more.
	Pruned []string
	// File is the ledger as it should now be written.
	File *compatmodel.Promotions
}

// applyPromotions decides every candidate group and returns the ledger that
// results. It is pure — the clock is an argument — so the thirty-day deadline
// is testable without waiting a month.
func applyPromotions(gen *generatedRegistry, ledger *compatmodel.Promotions, runs []promotionRun, minRuns int, now time.Time) promotionOutcome {
	today := now.Format(promotionDateLayout)
	out := promotionOutcome{File: &compatmodel.Promotions{
		Schema:  ledger.Schema,
		Comment: ledger.Comment,
		Version: compatmodel.PromotionsVersion,
		Groups:  make(map[string]compatmodel.Promotion, len(ledger.Groups)),
	}}

	known := make(map[string]bool, len(gen.Groups))
	for _, g := range gen.Groups {
		known[g.Name] = true
	}
	for name, entry := range ledger.Groups {
		if !known[name] {
			out.Pruned = append(out.Pruned, name)
			continue
		}
		// Taking a group back out of the gate is a supported hand edit — set
		// its state to candidate — and it leaves promotedAt and the run ids
		// behind, describing a promotion that has been withdrawn. That is
		// worse than untidy: promotions.schema.json requires both only of a
		// gated entry, anyone auditing the ledger reads them as the evidence
		// of a live promotion, and the soak would copy them forward verbatim
		// every night. The state is the record; clear the evidence to match
		// it, and let the group earn a fresh set.
		if entry.State == generatedStateCandidate {
			entry.PromotedAt = ""
			entry.Runs = nil
		}
		out.File.Groups[name] = entry
	}
	sort.Strings(out.Pruned)

	for _, group := range gen.Groups {
		entry, recorded := ledger.Groups[group.Name]
		// The ledger outranks the registry's own `state` field: the registry
		// is regenerated *from* the ledger, so between a promotion and the
		// regeneration that follows it the registry is one step behind, and
		// reading it would re-promote a group that is already gated and
		// rewrite its evidence with today's runs.
		state := group.State
		if recorded && entry.State != "" {
			state = entry.State
		}
		if state != generatedStateCandidate {
			continue
		}
		if !recorded {
			// First observation. Recording it even when nothing promotes is
			// the whole basis of the overdue check — a candidate with no
			// first-seen date can never be too old.
			entry = compatmodel.Promotion{State: generatedStateCandidate, FirstSeen: today}
			out.File.Groups[group.Name] = entry
		}

		verdict := decidePromotion(group, runs, minRuns)
		if verdict.Promote {
			ids := make([]string, 0, len(runs))
			for _, run := range runs {
				ids = append(ids, run.ID)
			}
			entry.State = generatedStateGated
			entry.PromotedAt = today
			entry.Runs = ids
			out.File.Groups[group.Name] = entry
			out.Promoted = append(out.Promoted, group.Name)
			continue
		}
		out.Blocked = append(out.Blocked, verdict)
		if age, ok := candidateAge(entry.FirstSeen, now); ok && age > candidateOverdueDays {
			out.Overdue = append(out.Overdue, fmt.Sprintf(
				"%s — candidate for %d days (limit %d), blocked by: %s",
				group.Name, age, candidateOverdueDays, strings.Join(verdict.Blockers, "; ")))
		}
	}
	sort.Strings(out.Promoted)
	sort.Strings(out.Overdue)
	sort.Slice(out.Blocked, func(i, j int) bool { return out.Blocked[i].Group < out.Blocked[j].Group })
	return out
}

// candidateAge is how many days ago a candidate was first seen. An unparseable
// date reports no age rather than an error: the schema already rejects one, and
// a malformed date must not make a candidate look overdue on the strength of a
// typo.
func candidateAge(firstSeen string, now time.Time) (int, bool) {
	seen, err := time.Parse(promotionDateLayout, strings.TrimSpace(firstSeen))
	if err != nil {
		return 0, false
	}
	return int(now.Sub(seen).Hours() / 24), true
}

// ---------------------------------------------------------------------------
// The CLI entry point
// ---------------------------------------------------------------------------

// promoteGeneratedFile is the --promote-generated entry point.
func promoteGeneratedFile(promotionsPath, registryPath, generatedPath, runSpec string, minRuns int, annotate bool, stdout io.Writer) error {
	gen, err := readGeneratedRegistry(generatedPath)
	if err != nil {
		return err
	}
	// An empty generated registry means there is nothing to soak — and,
	// crucially, that pruning would empty the ledger on the strength of a file
	// that says nothing. Returning early keeps this a guaranteed no-op on a
	// tree whose first candidates have not landed yet, which is what stops the
	// nightly opening an empty pull request every morning.
	if len(gen.Groups) == 0 {
		fmt.Fprintf(stdout, "compat: no generated groups — nothing to promote\n")
		return nil
	}
	hand, err := readParityRegistry(registryPath)
	if err != nil {
		return err
	}
	// The same lint the gates run before they trust a candidate exemption. A
	// generated group colliding with a hand-written name would promote the
	// wrong thing into the gate.
	if issues := lintGeneratedRegistry(hand, gen); len(issues) > 0 {
		return generatedRegistryIssueError(generatedPath, issues)
	}

	runs, err := readPromotionRuns(runSpec)
	if err != nil {
		return err
	}
	ledger, err := compatmodel.ReadPromotions(promotionsPath)
	if err != nil {
		return err
	}

	outcome := applyPromotions(gen, ledger, runs, minRuns, time.Now())
	contents, err := compatmodel.EncodePromotions(outcome.File)
	if err != nil {
		return err
	}
	changed, err := writePromotionsIfChanged(promotionsPath, contents)
	if err != nil {
		return err
	}
	reportPromotions(stdout, outcome, runs, minRuns, changed, promotionsPath, annotate)
	return nil
}

// writePromotionsIfChanged writes the ledger only when its bytes moved, and
// says which it did. A run that promotes nothing must leave the tree clean —
// that is how the nightly tells "nothing to promote" from "a promotion to
// publish" without inspecting the decision.
func writePromotionsIfChanged(path string, contents []byte) (bool, error) {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, contents) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read promotions %s: %w", path, err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return false, fmt.Errorf("write promotions %s: %w", path, err)
	}
	return true, nil
}

// readPromotionRuns loads the run reports named by --promote-runs: a
// comma-separated list of files, globs, or directories of *.json.
//
// A run's identifier is its file's base name without the extension, which is
// what lands in the ledger as evidence. The nightly names its merged reports
// after the workflow run that produced them, so the recorded identifier leads
// back to the artifacts.
func readPromotionRuns(spec string) ([]promotionRun, error) {
	paths, err := expandPromotionRunInputs(splitCSV(spec))
	if err != nil {
		return nil, err
	}
	runs := make([]promotionRun, 0, len(paths))
	for _, path := range paths {
		report, err := readRunReportFile(path)
		if err != nil {
			return nil, err
		}
		base := filepath.Base(path)
		runs = append(runs, promotionRun{
			ID:     strings.TrimSuffix(base, filepath.Ext(base)),
			Report: report,
		})
	}
	return runs, nil
}

// expandPromotionRunInputs resolves globs and directories to a sorted, deduped
// list of report files. Sorted, because the recorded evidence and the reported
// blockers must not depend on the order a shell happened to expand a glob in.
func expandPromotionRunInputs(inputs []string) ([]string, error) {
	seen := make(map[string]bool)
	var paths []string
	add := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, input := range inputs {
		matches, err := filepath.Glob(input)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", input, err)
		}
		if len(matches) == 0 {
			matches = []string{input}
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				return nil, fmt.Errorf("--promote-runs: %w", err)
			}
			if !info.IsDir() {
				add(match)
				continue
			}
			entries, err := filepath.Glob(filepath.Join(match, "*.json"))
			if err != nil {
				return nil, fmt.Errorf("glob %q: %w", match, err)
			}
			for _, entry := range entries {
				add(entry)
			}
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// reportPromotions prints what happened, in the order a reader cares about:
// what promoted, what did not and why, and what has been stuck too long.
func reportPromotions(stdout io.Writer, outcome promotionOutcome, runs []promotionRun, minRuns int, changed bool, path string, annotate bool) {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	fmt.Fprintf(stdout, "compat: soaked %d run(s) (%s), needing %d\n",
		len(runs), strings.Join(ids, ", "), minRuns)
	for _, name := range outcome.Promoted {
		fmt.Fprintf(stdout, "compat: promoted %s to %s\n", name, generatedStateGated)
	}
	for _, name := range outcome.Pruned {
		fmt.Fprintf(stdout, "compat: dropped the promotion record for %s — no generated group answers to that name\n", name)
	}
	for _, verdict := range outcome.Blocked {
		fmt.Fprintf(stdout, "compat: %s stays %s:\n", verdict.Group, generatedStateCandidate)
		for _, blocker := range verdict.Blockers {
			fmt.Fprintf(stdout, "    %s\n", blocker)
		}
	}
	if len(outcome.Overdue) > 0 {
		fmt.Fprintf(os.Stderr, "compat: %d candidate group(s) older than %d days have not promoted:\n",
			len(outcome.Overdue), candidateOverdueDays)
		for _, line := range outcome.Overdue {
			fmt.Fprintln(os.Stderr, "  "+line)
			if annotate {
				fmt.Fprintf(stdout, "::warning title=Compat candidate overdue::%s\n", escapeAnnotationData(line))
			}
		}
		fmt.Fprintf(os.Stderr,
			"compat: a candidate that cannot agree with itself is a bug in the recipe or the emulator — "+
				"fix it there, never by weakening an assertion or quarantining the test\n")
	}
	if changed {
		fmt.Fprintf(stdout, "compat: wrote %s — regenerate with `make generate-compat-model` to apply it\n", path)
		return
	}
	fmt.Fprintf(stdout, "compat: %s unchanged — nothing to promote\n", path)
}
