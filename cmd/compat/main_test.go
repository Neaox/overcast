package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
