package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureEvents runs fn with os.Stdout redirected to a file and returns the
// NDJSON events it emitted. emit writes to os.Stdout directly, which is what
// the Go runner reads, so a test that wants to assert on the run's report has
// to read it the same way.
func captureEvents(t *testing.T, fn func()) []map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.ndjson")
	f, err := os.Create(path) //nolint:gosec // a path this test just made
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = f
	func() {
		defer func() {
			os.Stdout = saved
			f.Close() //nolint:errcheck
		}()
		fn()
	}()

	raw, err := os.ReadFile(path) //nolint:gosec // as above
	if err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("emitted line is not JSON: %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

func statuses(events []map[string]any) map[string]string {
	out := map[string]string{}
	for _, ev := range events {
		if ev["event"] == "test_result" {
			out[fmt.Sprint(ev["test"])] = fmt.Sprint(ev["status"])
		}
	}
	return out
}

// TestSetupFailureStillRunsTeardown is the IR's rule (compat/model/README.md
// § The scenario file): a group whose setup failed reports every test as skip
// and still runs teardown. The failure that made this a bug is a setup that got
// halfway — the queue is created, the DLQ redrive is not — where skipping
// teardown leaks everything the successful steps made, with no test left to
// clean it up.
func TestSetupFailureStillRunsTeardown(t *testing.T) {
	tornDown := false
	ran := false

	events := captureEvents(t, func() {
		counts := RunGroup(context.Background(), TestGroup{
			Suite: "cli", Service: "widgets", Name: "widgets-gen-thing",
			Setup: func(context.Context, *TestContext) error {
				return errors.New("CreateDep: AccessDenied")
			},
			Tests: []TestCase{
				{Name: "CreateThing", Fn: func(context.Context, *TestContext) error { ran = true; return nil }},
				{Name: "DeleteThing", Fn: func(context.Context, *TestContext) error { ran = true; return nil }},
			},
			Teardown: func(context.Context, *TestContext) error { tornDown = true; return nil },
		})
		if counts.Skipped != 2 || counts.Passed != 0 {
			t.Errorf("counts = %+v, want two skips and nothing run", counts)
		}
	})

	if !tornDown {
		t.Error("teardown must run after a failed setup — that is the run that leaks")
	}
	if ran {
		t.Error("no test may run when setup failed")
	}
	got := statuses(events)
	if got["CreateThing"] != "skip" || got["DeleteThing"] != "skip" {
		t.Errorf("statuses = %v, want every test skipped", got)
	}
	var sawSetupError bool
	for _, ev := range events {
		if ev["event"] == "group_setup_error" {
			sawSetupError = true
		}
		if ev["event"] == "test_result" && !strings.Contains(fmt.Sprint(ev["error"]), "setup failed: CreateDep: AccessDenied") {
			t.Errorf("skip reason = %q, want it to name the setup failure", ev["error"])
		}
	}
	if !sawSetupError {
		t.Error("a failed setup must emit group_setup_error")
	}
}

// TestTeardownRunsAfterTheTests keeps the ordinary path honest alongside the
// one above: teardown is not something only a failed setup triggers.
func TestTeardownRunsAfterTheTests(t *testing.T) {
	var order []string
	captureEvents(t, func() {
		counts := RunGroup(context.Background(), TestGroup{
			Suite: "cli", Name: "widgets-gen-thing",
			Setup: func(context.Context, *TestContext) error { order = append(order, "setup"); return nil },
			Tests: []TestCase{{Name: "CreateThing", Fn: func(context.Context, *TestContext) error {
				order = append(order, "test")
				return nil
			}}},
			Teardown: func(context.Context, *TestContext) error { order = append(order, "teardown"); return nil },
		})
		if counts.Passed != 1 {
			t.Errorf("counts = %+v, want one pass", counts)
		}
	})
	if strings.Join(order, ",") != "setup,test,teardown" {
		t.Errorf("order = %v", order)
	}
}

// ─── unimplemented classification ─────────────────────────────────────────────

// composedFailure stands in for the scenario interpreter's failure type: a
// message assembled out of scenario data, which the substring heuristic must
// never be pointed at.
type composedFailure struct{ msg string }

func (c composedFailure) Error() string  { return c.msg }
func (composedFailure) ComposedFailure() {}

type wrappedUnimplemented struct{ err error }

func (w wrappedUnimplemented) Error() string   { return w.err.Error() }
func (w wrappedUnimplemented) Unwrap() []error { return []error{w.err, ErrUnimplemented} }

// TestIsUnimplementedReadsTheSentinelNotTheProse is the bug this classification
// existed to have: the heuristic matches a bare "501", and a composed failure
// message embeds the params JSON, so a run id or a port number in there was
// enough to report every failure of that test as unimplemented.
func TestIsUnimplementedReadsTheSentinelNotTheProse(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "a raw CLI 501",
			err:  errors.New(`aws widgets probe: exit status 254: An error occurred (501) when calling the Probe operation`),
			want: true,
		},
		{
			name: "a raw CLI error that is not a 501",
			err:  errors.New(`aws widgets get: exit status 254: An error occurred (AccessDenied) when calling the Get operation`),
			want: false,
		},
		{
			name: "a composed failure whose params happen to contain 501",
			err:  composedFailure{msg: `widgets-gen-thing/GetThing: GetThing params {"Id":"oc-501abcde-thing"}: responseField equals at $.Id: expected "x", actual "y" (compat/model/scenarios/widgets.json assert[0])`},
			want: false,
		},
		{
			name: "a composed failure whose port happens to contain 501",
			err:  composedFailure{msg: `widgets-gen-thing/GetThing: GetThing params {"Endpoint":"http://127.0.0.1:4501"}: readback nonEmpty at $.Id: expected a non-empty value, actual <missing> (f.json assert[0])`},
			want: false,
		},
		{
			name: "a composed failure carrying the sentinel",
			err:  wrappedUnimplemented{err: composedFailure{msg: `widgets-gen-probe/Probe: Probe params {}: call: expected the call to succeed, actual "An error occurred (501) …" (f.json call)`}},
			want: true,
		},
		{
			name: "the sentinel through fmt.Errorf's %w",
			err:  fmt.Errorf("eventually gave up after 3 attempt(s) 0ms apart; last failure: %w", wrappedUnimplemented{err: composedFailure{msg: "x"}}),
			want: true,
		},
		{
			name: "a composed failure through fmt.Errorf's %w",
			err:  fmt.Errorf("eventually gave up after 3 attempt(s) 0ms apart; last failure: %w", composedFailure{msg: `params {"Id":"oc-501abcde"}`}),
			want: false,
		},
		{name: "no error", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUnimplemented(tc.err); got != tc.want {
				t.Errorf("IsUnimplemented = %v, want %v for %v", got, tc.want, tc.err)
			}
		})
	}
}

// TestUnimplementedStatusIsReportedForTheWrappedSentinel walks the whole path a
// generated probe test takes, since the classification only matters where
// RunGroup applies it.
func TestUnimplementedStatusIsReportedForTheWrappedSentinel(t *testing.T) {
	events := captureEvents(t, func() {
		counts := RunGroup(context.Background(), TestGroup{
			Suite: "cli", Name: "widgets-gen-probe",
			Tests: []TestCase{
				{Name: "Probe", Fn: func(context.Context, *TestContext) error {
					return wrappedUnimplemented{err: composedFailure{msg: `probe: expected the call to succeed, actual "501"`}}
				}},
				{Name: "Get", Fn: func(context.Context, *TestContext) error {
					return composedFailure{msg: `params {"Id":"oc-501abcde"}: expected "a", actual "b"`}
				}},
			},
		})
		if counts.Unimplemented != 1 || counts.Failed != 1 {
			t.Errorf("counts = %+v, want one unimplemented and one fail", counts)
		}
	})
	got := statuses(events)
	if got["Probe"] != "unimplemented" {
		t.Errorf("Probe = %q, want unimplemented", got["Probe"])
	}
	if got["Get"] != "fail" {
		t.Errorf("Get = %q, want fail — a 501 in the params says nothing about the status", got["Get"])
	}
}
