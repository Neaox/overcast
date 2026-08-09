package compat

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

// crashArgv returns a platform-appropriate command that prints
// "missing dependency" to stderr and exits 7, standing in for a suite
// binary that dies before emitting any results.
func crashArgv() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo missing dependency 1>&2 & exit 7"}
	}
	return []string{"sh", "-c", "echo missing dependency >&2; exit 7"}
}

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
		Argv: crashArgv(),
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
				Argv: crashArgv(),
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

func TestRunnerRun_unknownSuiteNameIsRejected(t *testing.T) {
	// Given: a runner asked for a suite name no built-in suite answers to —
	// the shape of a typo on `--suite`.
	var log bytes.Buffer
	r := NewRunner(RunConfig{
		Endpoint: "http://127.0.0.1:1", // never contacted: the run must fail first
		Region:   "us-east-1",
		Suites:   []string{"go-skd"},
	}).WithLogWriter(&log)

	// When: the caller runs it, exactly as cmd/compat does.
	report, err := r.Run(context.Background())

	// Then: an error names the unknown suite and lists the real ones.
	if err == nil {
		t.Fatalf("Run() error = nil, want unknown-suite error (report = %#v)", report)
	}
	if !strings.Contains(err.Error(), `"go-skd"`) {
		t.Errorf("Run() error = %q, want it to name the unknown suite", err.Error())
	}
	if !strings.Contains(err.Error(), "go-sdk") {
		t.Errorf("Run() error = %q, want it to list the valid suites", err.Error())
	}
	// And: nothing was started — not even the pre-run sweep.
	if log.Len() != 0 {
		t.Errorf("Run() logged %q, want no work before the name check", log.String())
	}
}

func TestRunnerRun_oneUnknownSuiteFailsTheWholeSelection(t *testing.T) {
	// Given: a selection mixing a real suite with a typo. Running only the half
	// that matched would report green for a set nobody asked for.
	var log bytes.Buffer
	r := NewRunner(RunConfig{
		Endpoint: "http://127.0.0.1:1",
		Region:   "us-east-1",
		Suites:   []string{"go-sdk", "go-skd"},
	}).WithLogWriter(&log)

	// When: the caller runs it.
	report, err := r.Run(context.Background())

	// Then: the whole run is refused, and go-sdk never starts.
	if err == nil {
		t.Fatalf("Run() error = nil, want unknown-suite error (report = %#v)", report)
	}
	if !strings.Contains(err.Error(), `"go-skd"`) {
		t.Errorf("Run() error = %q, want it to name the unknown suite", err.Error())
	}
	if log.Len() != 0 {
		t.Errorf("Run() logged %q, want no suite work at all", log.String())
	}
}

func TestRunnerRun_noSuitesAtAllIsAnError(t *testing.T) {
	// Given: a runner holding no suites — the other way the parallelism budget
	// used to divide by zero.
	r := &Runner{
		cfg:       RunConfig{Endpoint: "http://127.0.0.1:1", Region: "us-east-1"},
		logWriter: &bytes.Buffer{},
	}

	// When: it runs.
	_, err := r.Run(context.Background())

	// Then: it says so instead of crashing.
	if err == nil {
		t.Fatal("Run() error = nil, want an empty-selection error")
	}
	if !strings.Contains(err.Error(), "no suites to run") {
		t.Fatalf("Run() error = %q, want it to say there is nothing to run", err.Error())
	}
}

func TestValidateSuiteNames(t *testing.T) {
	// Given: the built-in suite list.
	known := KnownSuiteNames()
	if len(known) == 0 {
		t.Fatal("KnownSuiteNames() is empty")
	}

	// When/Then: every built-in name is accepted.
	if err := ValidateSuiteNames(known); err != nil {
		t.Fatalf("ValidateSuiteNames(%v) error = %v, want nil", known, err)
	}
	if err := ValidateSuiteNames(nil); err != nil {
		t.Fatalf("ValidateSuiteNames(nil) error = %v, want nil (empty selection means all)", err)
	}

	// And: every unknown name is reported, not just the first.
	err := ValidateSuiteNames([]string{"nope", known[0], "also-nope"})
	if err == nil {
		t.Fatal("ValidateSuiteNames() error = nil, want unknown-suite error")
	}
	for _, want := range []string{`"nope"`, `"also-nope"`, known[len(known)-1]} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateSuiteNames() error = %q, want it to mention %s", err.Error(), want)
		}
	}
}
