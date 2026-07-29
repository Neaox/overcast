package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Neaox/overcast/compat"
)

const baselineVersion = 1

type compatBaseline struct {
	Version int             `json:"version"`
	Entries []baselineEntry `json:"entries"`
}

type baselineEntry struct {
	Suite   string        `json:"suite"`
	Service string        `json:"service"`
	Group   string        `json:"group"`
	Test    string        `json:"test"`
	Op      string        `json:"op,omitempty"`
	Status  compat.Status `json:"status"`
}

func compareBaselineFile(baselinePath, resultsPath string) error {
	baseline, err := readBaselineFile(baselinePath)
	if err != nil {
		return err
	}
	report, err := readRunReportFile(resultsPath)
	if err != nil {
		return err
	}
	flaky, err := readFlakyFile(*flakyFilePath)
	if err != nil {
		return err
	}
	regressions := compareBaselineWith(baseline, report, flaky)
	if len(regressions) > 0 {
		for _, regression := range regressions {
			fmt.Fprintln(os.Stderr, regression)
		}
		if *annotate {
			// stdout so a workflow step can tee it; GitHub picks the commands
			// up from either stream.
			fmt.Print(baselineAnnotations(regressions))
		}
		return fmt.Errorf("%d compat baseline regression(s)", len(regressions))
	}
	fmt.Printf("compat: baseline check passed (%d expected result(s))\n", len(baseline.Entries))
	return nil
}

func updateBaselineFile(baselinePath, resultsPath string) error {
	baseline, err := readBaselineFileIfExists(baselinePath)
	if err != nil {
		return err
	}
	report, err := readRunReportFile(resultsPath)
	if err != nil {
		return err
	}
	flaky, err := readFlakyFile(*flakyFilePath)
	if err != nil {
		return err
	}
	updated := updateBaselineWith(baseline, report, flaky)
	return writeBaselineFile(baselinePath, updated)
}

func lintBaselineChangeFiles(oldPath, newPath string) error {
	oldBaseline, err := readBaselineFile(oldPath)
	if err != nil {
		return err
	}
	newBaseline, err := readBaselineFile(newPath)
	if err != nil {
		return err
	}
	issues := lintBaselineChange(oldBaseline, newBaseline)
	if len(issues) > 0 {
		for _, issue := range issues {
			fmt.Fprintln(os.Stderr, issue)
		}
		if *annotate {
			fmt.Print(baselineAnnotations(issues))
		}
		return fmt.Errorf("%d compat baseline downgrade(s)", len(issues))
	}
	fmt.Printf("compat: baseline change lint passed (%d expected result(s))\n", len(newBaseline.Entries))
	return nil
}

// flakySet is the set of baseline keys quarantined as intermittent.
type flakySet map[string]bool

// flakyFile is compat/flaky.json — tests known to give different answers on
// identical input. They are bugs, not a resting state; the list only shrinks.
type flakyFile struct {
	Version int          `json:"version"`
	Comment string       `json:"comment,omitempty"`
	Flaky   []flakyEntry `json:"flaky"`
}

type flakyEntry struct {
	Suite  string `json:"suite"`
	Group  string `json:"group"`
	Test   string `json:"test"`
	Reason string `json:"reason"`
}

func readFlakyFile(path string) (flakySet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return flakySet{}, nil
		}
		return nil, fmt.Errorf("read flaky list %s: %w", path, err)
	}
	var file flakyFile
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("parse flaky list %s: %w", path, err)
	}
	set := make(flakySet, len(file.Flaky))
	for _, e := range file.Flaky {
		set[e.Suite+"/"+e.Group+"/"+e.Test] = true
	}
	return set, nil
}

func compareBaseline(baseline *compatBaseline, report *compat.RunReport) []string {
	return compareBaselineWith(baseline, report, flakySet{})
}

// compareBaselineWith reports regressions, ignoring tests quarantined as flaky.
//
// A quarantined test is exempt in both directions: it may fail where the
// baseline says pass, and it is not promoted when it passes. Anything else
// makes the gate intermittent, and a gate that reds the build at random is one
// people learn to re-run rather than read.
func compareBaselineWith(baseline *compatBaseline, report *compat.RunReport, flaky flakySet) []string {
	current := baselineEntriesFromReport(report)
	currentByKey := baselineEntryMap(current.Entries)
	baselineByKey := baselineEntryMap(baseline.Entries)
	var regressions []string
	for _, expected := range baseline.Entries {
		key := baselineKey(expected)
		if flaky[key] {
			continue
		}
		actual, ok := currentByKey[key]
		if !ok {
			regressions = append(regressions, fmt.Sprintf("compat baseline missing result: %s expected %s", key, expected.Status))
			continue
		}
		if statusRank(actual.Status) < statusRank(expected.Status) {
			regressions = append(regressions, fmt.Sprintf("compat baseline regression: %s %s -> %s", key, expected.Status, actual.Status))
		}
	}
	// A test the baseline says nothing about is new. Adding tests is always
	// welcome — but a new test that fails is a new failure, and iterating only
	// over baseline entries above would let it land on a green check. Only
	// `fail` is blocked: unimplemented/skip/na are legitimate states for a new
	// test (a known SUT gap, an environmental skip, an API the SDK lacks).
	for _, actual := range current.Entries {
		if actual.Status != compat.StatusFail {
			continue
		}
		key := baselineKey(actual)
		if flaky[key] {
			continue
		}
		if _, grandfathered := baselineByKey[key]; grandfathered {
			continue
		}
		regressions = append(regressions, fmt.Sprintf("compat baseline new failure, not in baseline: %s", key))
	}
	sort.Strings(regressions)
	return regressions
}

func updateBaseline(baseline *compatBaseline, report *compat.RunReport) *compatBaseline {
	return updateBaselineWith(baseline, report, flakySet{})
}

// updateBaselineWith ratchets the baseline forward, holding quarantined tests
// at their recorded floor. Promoting a flaky test on the run where it happened
// to pass would make its next intermittent failure a red build; a real fix is
// promoted by deleting its entry from compat/flaky.json.
func updateBaselineWith(baseline *compatBaseline, report *compat.RunReport, flaky flakySet) *compatBaseline {
	current := baselineEntriesFromReport(report)
	merged := baselineEntryMap(baseline.Entries)
	for _, entry := range current.Entries {
		key := baselineKey(entry)
		old, ok := merged[key]
		if ok && flaky[key] {
			continue
		}
		if !ok || statusRank(entry.Status) > statusRank(old.Status) {
			merged[key] = entry
		}
	}
	return baselineFromMap(merged)
}

func lintBaselineChange(oldBaseline, newBaseline *compatBaseline) []string {
	newByKey := baselineEntryMap(newBaseline.Entries)
	oldByKey := baselineEntryMap(oldBaseline.Entries)
	var issues []string
	for _, oldEntry := range oldBaseline.Entries {
		newEntry, ok := newByKey[baselineKey(oldEntry)]
		if !ok {
			issues = append(issues, fmt.Sprintf("compat baseline removed expectation: %s was %s", baselineKey(oldEntry), oldEntry.Status))
			continue
		}
		if statusRank(newEntry.Status) < statusRank(oldEntry.Status) {
			issues = append(issues, fmt.Sprintf("compat baseline downgrade: %s %s -> %s", baselineKey(oldEntry), oldEntry.Status, newEntry.Status))
		}
	}
	// Iterating only the old baseline above would let a contributor grandfather
	// a fresh failure simply by adding an entry for it. New expectations are
	// fine at any other status — the burn-down shrinks the fail set, never
	// grows it.
	//
	// Seeding is the exception: an empty old baseline means there is nothing to
	// get worse than, and the first population necessarily records reality
	// including its known failures. This cannot be abused to launder failures
	// later — emptying a populated baseline trips the removed-expectation check
	// above for every entry it dropped.
	if len(oldBaseline.Entries) == 0 {
		sort.Strings(issues)
		return issues
	}
	for _, newEntry := range newBaseline.Entries {
		if newEntry.Status != compat.StatusFail {
			continue
		}
		if _, existed := oldByKey[baselineKey(newEntry)]; existed {
			continue
		}
		issues = append(issues, fmt.Sprintf("compat baseline new fail expectation: %s may not be added as fail", baselineKey(newEntry)))
	}
	sort.Strings(issues)
	return issues
}

// baselineAnnotations renders regression messages as GitHub workflow commands
// so each one becomes an annotation on the PR's checks tab, not just a line in
// a log nobody opens. Newlines and the other reserved characters are
// percent-encoded per the workflow-command format; an unescaped newline would
// silently truncate the annotation.
//
// https://docs.github.com/actions/reference/workflow-commands-for-github-actions
func baselineAnnotations(regressions []string) string {
	var b strings.Builder
	for _, regression := range regressions {
		b.WriteString("::error title=Compat baseline::")
		b.WriteString(escapeAnnotationData(regression))
		b.WriteString("\n")
	}
	return b.String()
}

// escapeAnnotationData percent-encodes the characters GitHub treats as
// structural inside a workflow command's message.
func escapeAnnotationData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

func baselineEntriesFromReport(report *compat.RunReport) *compatBaseline {
	var entries []baselineEntry
	for _, suite := range report.Suites {
		if suite == nil {
			continue
		}
		for _, group := range suite.Groups {
			if group == nil {
				continue
			}
			for _, test := range group.Tests {
				entries = append(entries, baselineEntry{
					Suite:   suite.Suite,
					Service: test.Service,
					Group:   test.Group,
					Test:    test.Test,
					Op:      test.Op,
					Status:  test.Status,
				})
			}
		}
	}
	return &compatBaseline{Version: baselineVersion, Entries: sortBaselineEntries(entries)}
}

func readBaselineFile(path string) (*compatBaseline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	var baseline compatBaseline
	if err := json.Unmarshal(b, &baseline); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	if baseline.Version == 0 {
		baseline.Version = baselineVersion
	}
	return &baseline, nil
}

func readBaselineFileIfExists(path string) (*compatBaseline, error) {
	baseline, err := readBaselineFile(path)
	if err == nil {
		return baseline, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return &compatBaseline{Version: baselineVersion}, nil
	}
	return nil, err
}

func writeBaselineFile(path string, baseline *compatBaseline) error {
	baseline.Version = baselineVersion
	baseline.Entries = sortBaselineEntries(baseline.Entries)
	b, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("write baseline: %w", err)
	}
	return nil
}

func baselineEntryMap(entries []baselineEntry) map[string]baselineEntry {
	out := make(map[string]baselineEntry, len(entries))
	for _, entry := range entries {
		out[baselineKey(entry)] = entry
	}
	return out
}

func baselineFromMap(entries map[string]baselineEntry) *compatBaseline {
	out := make([]baselineEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	return &compatBaseline{Version: baselineVersion, Entries: sortBaselineEntries(out)}
}

func sortBaselineEntries(entries []baselineEntry) []baselineEntry {
	sort.SliceStable(entries, func(i, j int) bool {
		return baselineKey(entries[i]) < baselineKey(entries[j])
	})
	return entries
}

func baselineKey(entry baselineEntry) string {
	return entry.Suite + "/" + entry.Group + "/" + entry.Test
}

func statusRank(status compat.Status) int {
	switch status {
	case compat.StatusPass:
		return 4
	case compat.StatusSkip, compat.StatusNA:
		return 3
	case compat.StatusUnimplemented:
		return 2
	case compat.StatusFail:
		return 1
	default:
		return 0
	}
}
