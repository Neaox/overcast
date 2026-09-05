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
		ctx := NewTestContext("http://127.0.0.1:4566", "us-east-1", "run")
		res := RunGroup(context.Background(), TestGroup{
			Suite: "go-sdk", Service: "widgets", Name: "widgets-gen-thing",
			Setup: func(context.Context, *TestContext) error {
				return errors.New("CreateDep: AccessDenied")
			},
			Tests: []TestCase{
				{Name: "CreateThing", Fn: func(context.Context, *TestContext) error { ran = true; return nil }},
				{Name: "DeleteThing", Fn: func(context.Context, *TestContext) error { ran = true; return nil }},
			},
			Teardown: func(context.Context, *TestContext) error { tornDown = true; return nil },
		}, ctx)
		if res.Skipped != 2 || res.Passed != 0 {
			t.Errorf("res = %+v, want two skips and nothing run", res)
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
	for _, ev := range events {
		if ev["event"] == "test_result" && !strings.Contains(fmt.Sprint(ev["error"]), "setup failed: CreateDep: AccessDenied") {
			t.Errorf("skip reason = %q, want it to name the setup failure", ev["error"])
		}
	}
}

// TestTeardownRunsAfterTheTests keeps the ordinary path honest alongside the
// one above: teardown is not something only a failed setup triggers.
func TestTeardownRunsAfterTheTests(t *testing.T) {
	var order []string
	captureEvents(t, func() {
		ctx := NewTestContext("http://127.0.0.1:4566", "us-east-1", "run")
		res := RunGroup(context.Background(), TestGroup{
			Suite: "go-sdk", Name: "widgets-gen-thing",
			Setup: func(context.Context, *TestContext) error { order = append(order, "setup"); return nil },
			Tests: []TestCase{{Name: "CreateThing", Fn: func(context.Context, *TestContext) error {
				order = append(order, "test")
				return nil
			}}},
			Teardown: func(context.Context, *TestContext) error { order = append(order, "teardown"); return nil },
		}, ctx)
		if res.Passed != 1 {
			t.Errorf("res = %+v, want one pass", res)
		}
	})
	if strings.Join(order, ",") != "setup,test,teardown" {
		t.Errorf("order = %v", order)
	}
}
