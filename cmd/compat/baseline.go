package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

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
	regressions := compareBaseline(baseline, report)
	if len(regressions) > 0 {
		for _, regression := range regressions {
			fmt.Fprintln(os.Stderr, regression)
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
	updated := updateBaseline(baseline, report)
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
		return fmt.Errorf("%d compat baseline downgrade(s)", len(issues))
	}
	fmt.Printf("compat: baseline change lint passed (%d expected result(s))\n", len(newBaseline.Entries))
	return nil
}

func compareBaseline(baseline *compatBaseline, report *compat.RunReport) []string {
	current := baselineEntriesFromReport(report)
	currentByKey := baselineEntryMap(current.Entries)
	var regressions []string
	for _, expected := range baseline.Entries {
		actual, ok := currentByKey[baselineKey(expected)]
		if !ok {
			regressions = append(regressions, fmt.Sprintf("compat baseline missing result: %s expected %s", baselineKey(expected), expected.Status))
			continue
		}
		if statusRank(actual.Status) < statusRank(expected.Status) {
			regressions = append(regressions, fmt.Sprintf("compat baseline regression: %s %s -> %s", baselineKey(expected), expected.Status, actual.Status))
		}
	}
	sort.Strings(regressions)
	return regressions
}

func updateBaseline(baseline *compatBaseline, report *compat.RunReport) *compatBaseline {
	current := baselineEntriesFromReport(report)
	merged := baselineEntryMap(baseline.Entries)
	for _, entry := range current.Entries {
		key := baselineKey(entry)
		old, ok := merged[key]
		if !ok || statusRank(entry.Status) > statusRank(old.Status) {
			merged[key] = entry
		}
	}
	return baselineFromMap(merged)
}

func lintBaselineChange(oldBaseline, newBaseline *compatBaseline) []string {
	newByKey := baselineEntryMap(newBaseline.Entries)
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
	sort.Strings(issues)
	return issues
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
