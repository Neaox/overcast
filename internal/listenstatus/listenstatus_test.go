package listenstatus

import "testing"

func TestTracker_SnapshotIsNilUntilSomethingReports(t *testing.T) {
	// Given: a fresh tracker
	tr := NewTracker()

	// When: nothing has reported
	got := tr.Snapshot()

	// Then: the snapshot is nil, so a JSON field can omit it
	if got != nil {
		t.Fatalf("Snapshot() = %v, want nil", got)
	}
}

func TestTracker_SnapshotIsACopy(t *testing.T) {
	// Given: one listener reported
	tr := NewTracker()
	tr.Set(SMTP, Status{State: Listening, Addr: "127.0.0.1:1025"})

	// When: a caller mutates the snapshot it was handed
	snap := tr.Snapshot()
	snap[SMTP] = Status{State: Failed}

	// Then: the tracker is unaffected
	if got := tr.Snapshot()[SMTP].State; got != Listening {
		t.Fatalf("state after mutating a snapshot = %q, want %q", got, Listening)
	}
}

func TestTracker_SetReplacesAnEarlierReport(t *testing.T) {
	// Given: a listener first failed, then (after a re-probe) bound
	tr := NewTracker()
	tr.Set(LambdaRuntimeAPI, Status{State: Failed, Error: "address in use"})
	tr.Set(LambdaRuntimeAPI, Status{State: Listening, Addr: "172.18.0.1:9001"})

	// When/Then: the latest report wins and nothing lingers from the first
	got := tr.Snapshot()[LambdaRuntimeAPI]
	if got.State != Listening || got.Error != "" {
		t.Fatalf("status = %+v, want listening with no error", got)
	}
}

func TestDegraded(t *testing.T) {
	// Given: a mix of outcomes
	cases := map[string]struct {
		in   map[string]Status
		want bool
	}{
		"nothing reported":    {nil, false},
		"all listening":       {map[string]Status{SMTP: {State: Listening}}, false},
		"fell back but bound": {map[string]Status{SMTP: {State: Listening, FellBack: true}}, false},
		"one failed":          {map[string]Status{SMTP: {State: Listening}, LambdaRuntimeAPI: {State: Failed}}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// When/Then: only a Failed state counts as degraded
			if got := Degraded(tc.in); got != tc.want {
				t.Fatalf("Degraded() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTracker_NilIsSafe(t *testing.T) {
	// Given: no tracker was wired (a test server, say)
	var tr *Tracker

	// When/Then: reporting and reading are no-ops rather than panics
	tr.Set(SMTP, Status{State: Listening})
	if got := tr.Snapshot(); got != nil {
		t.Fatalf("Snapshot() on nil = %v, want nil", got)
	}
}
