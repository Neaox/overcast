package compat

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

// spamArgv returns a platform-appropriate command that prints 300 numbered
// lines to stderr and exits 7 — a suite whose failure output exceeds the
// error-message cap, like a failed docker build streaming buildkit output.
func spamArgv() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "(for /L %i in (1,1,300) do @echo stderr-spam-line-%i-padding-padding-padding 1>&2) & exit 7"}
	}
	return []string{"sh", "-c", `i=1; while [ $i -le 300 ]; do echo "stderr-spam-line-$i-padding-padding-padding" >&2; i=$((i+1)); done; exit 7`}
}

func TestTailForError(t *testing.T) {
	t.Run("short input passes through unchanged", func(t *testing.T) {
		in := "line one\nline two"
		if got := tailForError(in, 100); got != in {
			t.Fatalf("tailForError() = %q, want input unchanged", got)
		}
	})

	t.Run("long input is cut on a line boundary and labeled", func(t *testing.T) {
		// Given: 100 numbered lines, far more than the cap admits.
		var b strings.Builder
		for i := 1; i <= 100; i++ {
			fmt.Fprintf(&b, "numbered-line-%03d some padding to give each line width\n", i)
		}
		in := strings.TrimSpace(b.String())

		got := tailForError(in, 500)

		// Then: the result is labeled, keeps the final line, and every kept
		// line is whole — the first kept line is a full "numbered-line-NNN…",
		// not a sheared fragment like the `n without silencing errors.` the
		// old byte-offset slice produced.
		label, body, ok := strings.Cut(got, "\n")
		if !ok || !strings.HasPrefix(label, "[suite output truncated — showing last ") {
			t.Fatalf("tailForError() = %q, want a truncation label on the first line", got)
		}
		if !strings.HasSuffix(body, "numbered-line-100 some padding to give each line width") {
			t.Errorf("tailForError() body = %q, want the final line preserved", body)
		}
		firstKept, _, _ := strings.Cut(body, "\n")
		if !strings.HasPrefix(firstKept, "numbered-line-") {
			t.Errorf("tailForError() first kept line = %q, want a whole line", firstKept)
		}
		if len(body) > 500 {
			t.Errorf("tailForError() body is %d bytes, want ≤ cap of 500", len(body))
		}
	})

	t.Run("single oversized line is cut on a rune boundary", func(t *testing.T) {
		// Given: one line of two-byte runes and a cap that lands mid-rune.
		in := strings.Repeat("é", 3000)

		got := tailForError(in, 4095)

		if !utf8.ValidString(got) {
			t.Fatalf("tailForError() produced invalid UTF-8: %q", got[:80])
		}
		if !strings.Contains(got, "[suite output truncated") {
			t.Errorf("tailForError() = %q…, want a truncation label", got[:80])
		}
	})
}

func TestRunSuite_longStderrIsTruncatedOnLineBoundary(t *testing.T) {
	// Given: a suite subprocess that floods stderr past the error cap and dies
	// without emitting results — the shape of a failed docker image build.
	r := &Runner{
		cfg: RunConfig{
			Endpoint: "http://localhost:4566",
			Region:   "us-east-1",
			RunID:    "oc-test",
		},
		logWriter: &bytes.Buffer{},
	}
	suite := SuiteConfig{
		Name: "spammy-suite",
		Argv: spamArgv(),
	}

	// When: the runner executes the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := r.runSuite(ctx, suite, 1)

	// Then: the error carries a labeled tail whose lines are whole.
	if err == nil {
		t.Fatal("runSuite() error = nil, want infrastructure error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[suite output truncated") {
		t.Fatalf("runSuite() error = %q…, want a truncation label", msg[:min(120, len(msg))])
	}
	if !strings.Contains(msg, "stderr-spam-line-300") {
		t.Errorf("runSuite() error lost the tail of stderr:\n%s", msg)
	}
	if strings.Contains(msg, "stderr-spam-line-1-padding") {
		t.Errorf("runSuite() error kept the head of stderr past the cap:\n%s", msg)
	}
	_, body, _ := strings.Cut(msg, "]\n")
	firstKept, _, _ := strings.Cut(body, "\n")
	if !strings.HasPrefix(firstKept, "stderr-spam-line-") {
		t.Errorf("runSuite() first kept line = %q, want a whole line after the label", firstKept)
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
