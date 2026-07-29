package compat

import (
	"context"
	"log/slog"
	"testing"
)

// seedResults injects results as if suites had reported them over NDJSON.
func seedResults(o *Orchestrator, events ...TestResultEvent) {
	o.resultsMu.Lock()
	defer o.resultsMu.Unlock()
	for _, ev := range events {
		o.results[ev.Suite+":"+ev.Group+":"+ev.Test] = ev
	}
}

func newTestOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	o := NewOrchestrator(context.Background(), nil, func([]byte) {}, slog.New(slog.DiscardHandler))
	o.Endpoint = "http://localhost:4570"
	return o
}

func TestReportAggregatesResultsBySuiteAndGroup(t *testing.T) {
	// Given: results from two suites, one of which covers two groups
	o := newTestOrchestrator(t)
	seedResults(o,
		TestResultEvent{Suite: "go-sdk", Service: "s3", Group: "s3-crud", Test: "CreateBucket", Status: StatusPass, DurationMS: 4},
		TestResultEvent{Suite: "go-sdk", Service: "s3", Group: "s3-crud", Test: "DeleteBucket", Status: StatusFail, Error: "boom"},
		TestResultEvent{Suite: "go-sdk", Service: "sqs", Group: "sqs-queues", Test: "CreateQueue", Status: StatusPass},
		TestResultEvent{Suite: "cli", Service: "s3", Group: "s3-crud", Test: "CreateBucket", Status: StatusUnimplemented},
		TestResultEvent{Suite: "cli", Service: "s3", Group: "s3-crud", Test: "DeleteBucket", Status: StatusSkip},
	)

	// When: a report is built from the live results
	report := o.Report()

	// Then: it carries the endpoint and one entry per suite
	if report.Endpoint != "http://localhost:4570" {
		t.Errorf("Endpoint = %q, want the orchestrator's endpoint", report.Endpoint)
	}
	if len(report.Suites) != 2 {
		t.Fatalf("got %d suites, want 2", len(report.Suites))
	}

	suites := map[string]*SuiteReport{}
	for _, sr := range report.Suites {
		suites[sr.Suite] = sr
	}

	goSDK, ok := suites["go-sdk"]
	if !ok {
		t.Fatal("go-sdk missing from the report")
	}
	if goSDK.Passed != 2 || goSDK.Failed != 1 {
		t.Errorf("go-sdk counts: passed=%d failed=%d, want 2/1", goSDK.Passed, goSDK.Failed)
	}
	if len(goSDK.Groups) != 2 {
		t.Errorf("go-sdk has %d groups, want 2", len(goSDK.Groups))
	}

	cli, ok := suites["cli"]
	if !ok {
		t.Fatal("cli missing from the report")
	}
	if cli.Unimplemented != 1 || cli.Skipped != 1 {
		t.Errorf("cli counts: unimplemented=%d skipped=%d, want 1/1", cli.Unimplemented, cli.Skipped)
	}

	// And: group-level detail survives, so the report is usable by --report
	for _, g := range goSDK.Groups {
		if g.Name != "s3-crud" {
			continue
		}
		if g.Service != "s3" {
			t.Errorf("group service = %q, want s3", g.Service)
		}
		if len(g.Tests) != 2 {
			t.Errorf("s3-crud has %d tests, want 2", len(g.Tests))
		}
		if g.Passed != 1 || g.Failed != 1 {
			t.Errorf("s3-crud counts: passed=%d failed=%d, want 1/1", g.Passed, g.Failed)
		}
	}
}

func TestReportIsStableAcrossCalls(t *testing.T) {
	// Given: results that arrived in arbitrary map order
	o := newTestOrchestrator(t)
	seedResults(o,
		TestResultEvent{Suite: "go-sdk", Service: "sqs", Group: "sqs-queues", Test: "CreateQueue", Status: StatusPass},
		TestResultEvent{Suite: "go-sdk", Service: "s3", Group: "s3-crud", Test: "CreateBucket", Status: StatusPass},
		TestResultEvent{Suite: "cli", Service: "s3", Group: "s3-crud", Test: "CreateBucket", Status: StatusPass},
	)

	// When: the report is built repeatedly
	// Then: suites, groups, and tests come back in the same order every time,
	// so a saved results file does not churn between runs
	first := o.Report()
	for i := 0; i < 5; i++ {
		next := o.Report()
		if len(next.Suites) != len(first.Suites) {
			t.Fatalf("suite count changed between calls")
		}
		for j := range first.Suites {
			if next.Suites[j].Suite != first.Suites[j].Suite {
				t.Fatalf("suite order changed: %q vs %q", next.Suites[j].Suite, first.Suites[j].Suite)
			}
			for k := range first.Suites[j].Groups {
				if next.Suites[j].Groups[k].Name != first.Suites[j].Groups[k].Name {
					t.Fatalf("group order changed within %q", first.Suites[j].Suite)
				}
			}
		}
	}
}

func TestReportWithNoResultsIsEmptyNotNil(t *testing.T) {
	// Given: an orchestrator that has not run anything
	o := newTestOrchestrator(t)

	// When: a report is built
	// Then: it is a usable, empty report rather than nil
	report := o.Report()
	if report == nil {
		t.Fatal("Report() returned nil")
	}
	if len(report.Suites) != 0 {
		t.Errorf("got %d suites, want 0", len(report.Suites))
	}
}

func TestIdleReportsWhetherWorkIsOutstanding(t *testing.T) {
	// Given: an orchestrator with one suite process
	o := newTestOrchestrator(t)
	sp := &SuiteProcess{Name: "go-sdk", State: SuiteReady}
	o.processes = map[string]*SuiteProcess{"go-sdk": sp}

	// When: nothing is queued or running
	// Then: the orchestrator is idle, which is when a run report is final
	if !o.idle() {
		t.Error("idle() = false with an empty queue and no active batch")
	}

	// When: a batch is running
	sp.ActiveBatch = &QueuedBatch{ID: "b1"}
	if o.idle() {
		t.Error("idle() = true while a batch is active")
	}

	// When: the batch finishes but another is queued behind it
	sp.ActiveBatch = nil
	sp.Queue = []QueuedBatch{{ID: "b2"}}
	if o.idle() {
		t.Error("idle() = true with a batch still queued")
	}
}

func TestOnIdleFiresOnceWorkDrains(t *testing.T) {
	// Given: an orchestrator with an idle callback registered
	o := newTestOrchestrator(t)
	sp := &SuiteProcess{Name: "go-sdk", State: SuiteReady}
	o.processes = map[string]*SuiteProcess{"go-sdk": sp}
	seedResults(o, TestResultEvent{Suite: "go-sdk", Service: "s3", Group: "s3-crud", Test: "CreateBucket", Status: StatusPass})

	var got *RunReport
	o.OnIdle = func(r *RunReport) { got = r }

	// When: work is still outstanding, the callback must not fire
	sp.Queue = []QueuedBatch{{ID: "b2"}}
	o.notifyIfIdle()
	if got != nil {
		t.Fatal("OnIdle fired while a batch was still queued")
	}

	// When: the last batch drains
	sp.Queue = nil
	o.notifyIfIdle()

	// Then: the callback receives the accumulated report
	if got == nil {
		t.Fatal("OnIdle did not fire once the queue drained")
	}
	if len(got.Suites) != 1 || got.Suites[0].Passed != 1 {
		t.Errorf("OnIdle report did not carry the results: %+v", got.Suites)
	}
}
