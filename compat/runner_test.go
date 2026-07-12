package compat

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunSuite_crashBeforeResultsReturnsInfrastructureError(t *testing.T) {
	// Given: a suite subprocess that exits before emitting any NDJSON test results.
	r := &Runner{
		cfg: RunConfig{
			Endpoint: "http://localhost:4566",
			Region:   "us-east-1",
			RunID:    "oc-test",
		},
		logWriter: &bytes.Buffer{},
	}
	suite := SuiteConfig{
		Name: "broken-suite",
		Argv: []string{"sh", "-c", "echo missing dependency >&2; exit 7"},
	}

	// When: the runner executes the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sr, err := r.runSuite(ctx, suite, 1)

	// Then: the suite crash is reported as infrastructure failure, not a zero-test pass.
	if err == nil {
		t.Fatal("runSuite() error = nil, want infrastructure error")
	}
	if !strings.Contains(err.Error(), "missing dependency") {
		t.Fatalf("runSuite() error = %q, want stderr context", err.Error())
	}
	if sr == nil || sr.Total() != 0 {
		t.Fatalf("runSuite() SuiteReport total = %v, want zero-result report", sr)
	}
}

func TestRunnerRun_propagatesSuiteInfrastructureErrors(t *testing.T) {
	// Given: a compat runner with a suite that crashes before emitting results.
	emulator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	}))
	t.Cleanup(emulator.Close)

	r := &Runner{
		cfg: RunConfig{
			Endpoint: emulator.URL,
			Region:   "us-east-1",
			RunID:    "oc-test",
		},
		suites: []SuiteConfig{
			{
				Name: "broken-suite",
				Argv: []string{"sh", "-c", "echo missing dependency >&2; exit 7"},
			},
		},
		logWriter: &bytes.Buffer{},
	}

	// When: the full runner executes all suites.
	report, err := r.Run(context.Background())

	// Then: the infrastructure failure is returned to the CLI/workflow.
	if err == nil {
		t.Fatal("Run() error = nil, want infrastructure error")
	}
	if !strings.Contains(err.Error(), "missing dependency") {
		t.Fatalf("Run() error = %q, want stderr context", err.Error())
	}
	if report == nil || len(report.Suites) != 1 || report.Suites[0].Total() != 0 {
		t.Fatalf("Run() report = %#v, want one zero-result suite report", report)
	}
}
