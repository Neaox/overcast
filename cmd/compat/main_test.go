package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/compat"
)

func TestWriteRunReportFile_persistsStructuredReport(t *testing.T) {
	// Given: a completed compatibility report.
	started := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	report := &compat.RunReport{
		Endpoint:   "http://localhost:4566",
		StartedAt:  started,
		FinishedAt: started.Add(2 * time.Second),
		Suites: []*compat.SuiteReport{
			{
				Suite:         "go-sdk",
				Passed:        2,
				Failed:        1,
				Skipped:       3,
				Unimplemented: 4,
			},
		},
	}
	path := filepath.Join(t.TempDir(), "compat-results.json")

	// When: the CLI writes the results file for a non-server run.
	if err := writeRunReportFile(path, report); err != nil {
		t.Fatalf("writeRunReportFile() error = %v", err)
	}

	// Then: the file contains a valid RunReport JSON payload.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read results: %v", err)
	}
	var got compat.RunReport
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal results: %v", err)
	}
	if got.Endpoint != report.Endpoint {
		t.Fatalf("Endpoint = %q, want %q", got.Endpoint, report.Endpoint)
	}
	if len(got.Suites) != 1 || got.Suites[0].Suite != "go-sdk" || got.Suites[0].Unimplemented != 4 {
		t.Fatalf("Suites = %#v", got.Suites)
	}
}

func TestMergeRunReportFiles_combinesSuiteReports(t *testing.T) {
	// Given: per-suite compatibility result files from separate CI jobs.
	dir := t.TempDir()
	firstStart := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	secondStart := firstStart.Add(3 * time.Second)
	writeTestReport(t, filepath.Join(dir, "compat-results-rust-sdk.json"), &compat.RunReport{
		Endpoint:   "http://localhost:4566",
		StartedAt:  secondStart,
		FinishedAt: secondStart.Add(time.Second),
		Suites: []*compat.SuiteReport{{
			Suite:         "rust-sdk",
			Passed:        3,
			Unimplemented: 1,
		}},
	})
	writeTestReport(t, filepath.Join(dir, "compat-results-node-js-sdk.json"), &compat.RunReport{
		Endpoint:   "http://localhost:4566",
		StartedAt:  firstStart,
		FinishedAt: firstStart.Add(2 * time.Second),
		Suites: []*compat.SuiteReport{{
			Suite:   "node-js-sdk",
			Passed:  5,
			Skipped: 2,
		}},
	})

	// When: the CLI merge helper reads the result glob.
	merged, err := mergeRunReportFiles([]string{filepath.Join(dir, "compat-results-*.json")})
	if err != nil {
		t.Fatalf("mergeRunReportFiles() error = %v", err)
	}

	// Then: the merged report preserves all suites in canonical runner order and
	// spans the earliest start through the latest finish.
	if merged.Endpoint != "http://localhost:4566" {
		t.Fatalf("Endpoint = %q", merged.Endpoint)
	}
	if !merged.StartedAt.Equal(firstStart) {
		t.Fatalf("StartedAt = %s, want %s", merged.StartedAt, firstStart)
	}
	if !merged.FinishedAt.Equal(secondStart.Add(time.Second)) {
		t.Fatalf("FinishedAt = %s", merged.FinishedAt)
	}
	if len(merged.Suites) != 2 {
		t.Fatalf("Suites len = %d, want 2", len(merged.Suites))
	}
	if merged.Suites[0].Suite != "node-js-sdk" || merged.Suites[1].Suite != "rust-sdk" {
		t.Fatalf("Suites order = %#v", []string{merged.Suites[0].Suite, merged.Suites[1].Suite})
	}
	if merged.Suites[1].Unimplemented != 1 {
		t.Fatalf("rust suite = %#v", merged.Suites[1])
	}
}

func TestMergeRunReportFiles_noMatches(t *testing.T) {
	// Given: a result pattern that matches nothing.
	pattern := filepath.Join(t.TempDir(), "missing-*.json")

	// When: the merge helper reads the pattern.
	_, err := mergeRunReportFiles([]string{pattern})

	// Then: it returns an actionable error instead of writing an empty report.
	if err == nil {
		t.Fatal("mergeRunReportFiles() error = nil, want error")
	}
}

func writeTestReport(t *testing.T, path string, report *compat.RunReport) {
	t.Helper()
	if err := writeRunReportFile(path, report); err != nil {
		t.Fatalf("write test report %s: %v", path, err)
	}
}

func TestArtifactNamedDistinguishesARequestFromADefault(t *testing.T) {
	// Given: every way --overcast-image can arrive at a value
	// When: it is classified as a request or a default
	// Then: only a value the caller actually supplied — on the command line or
	// through the env var — counts. This is the distinction the whole fix for
	// issue #801 turns on: the flag is non-empty even when nobody asked for
	// anything, because it defaults to defaultOvercastImage, so the value
	// cannot answer the question on its own.
	cases := []struct {
		name      string
		value     string
		env       string
		flagGiven bool
		want      bool
	}{
		{"compiled-in default", defaultOvercastImage, "", false, false},
		{"named on the command line", "ghcr.io/neaox/overcast:rc", "", true, true},
		{"named through the environment", "ghcr.io/neaox/overcast:rc", "ghcr.io/neaox/overcast:rc", false, true},
		{"command line overriding the environment", "ghcr.io/neaox/overcast:rc", "ghcr.io/neaox/overcast:other", true, true},
		{"explicitly emptied", "", "", true, false},
		{"unset with an empty default", "", "", false, false},
	}
	for _, tc := range cases {
		if got := artifactNamed(tc.value, tc.env, tc.flagGiven); got != tc.want {
			t.Errorf("%s: artifactNamed(%q, %q, %v) = %v, want %v",
				tc.name, tc.value, tc.env, tc.flagGiven, got, tc.want)
		}
	}
}

func TestResolveSuiteSelection_acceptsKnownNamesAndTheEmptyDefault(t *testing.T) {
	// Given: the built-in suite names, as --suite would carry them.
	known := compat.KnownSuiteNames()
	if len(known) < 2 {
		t.Fatalf("KnownSuiteNames() = %v, want at least two built-in suites", known)
	}

	// When: the flag is empty, meaning "every suite".
	got, err := resolveSuiteSelection("")

	// Then: no selection is returned and nothing is rejected.
	if err != nil || len(got) != 0 {
		t.Fatalf("resolveSuiteSelection(\"\") = %v, %v; want nil, nil", got, err)
	}

	// When: real names are given, spacing and all.
	got, err = resolveSuiteSelection(known[0] + ", " + known[1])

	// Then: they are passed through trimmed.
	if err != nil {
		t.Fatalf("resolveSuiteSelection() error = %v, want nil", err)
	}
	if len(got) != 2 || got[0] != known[0] || got[1] != known[1] {
		t.Fatalf("resolveSuiteSelection() = %v, want [%s %s]", got, known[0], known[1])
	}
}

func TestResolveSuiteSelection_rejectsTypoEvenAlongsideARealSuite(t *testing.T) {
	// Given: a suite list where one name is a typo of a real suite.
	known := compat.KnownSuiteNames()

	// When: the flag is resolved.
	got, err := resolveSuiteSelection(known[0] + ",definitely-not-a-suite")

	// Then: the whole selection is refused — running the half that matched
	// would report green for coverage the caller never asked about.
	if err == nil {
		t.Fatalf("resolveSuiteSelection() = %v, nil; want an unknown-suite error", got)
	}
	if !strings.Contains(err.Error(), `"definitely-not-a-suite"`) {
		t.Errorf("error = %q, want it to name the unknown suite", err.Error())
	}
	if !strings.Contains(err.Error(), known[0]) {
		t.Errorf("error = %q, want it to list the valid suites", err.Error())
	}
}
