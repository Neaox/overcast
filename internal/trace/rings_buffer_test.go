package trace

import (
	"strconv"
	"testing"
	"time"
)

// The point of giving internal polling its own ring: a UI left open, a health
// check and an SSE reconnect can no longer cost a user-facing trace. Under the
// single ring these shared a budget, so idle polling ate 20% of it.
func TestBuffer_internalTrafficDoesNotConsumeUserBudget(t *testing.T) {
	// Given: a buffer at its default-shaped floor
	const floor = 50
	buf := NewBuffer(floor)
	base := time.Unix(0, 0)

	// When: the floor's worth of user traces arrives, interleaved with far more
	// internal polling than the internal ring can hold
	for i := 0; i < floor; i++ {
		addTrace(buf, traceSpec{
			RequestID: "user-" + strconv.Itoa(i),
			Path:      "/",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
		for p := 0; p < 5; p++ {
			addTrace(buf, traceSpec{
				RequestID: "int-" + strconv.Itoa(i) + "-" + strconv.Itoa(p),
				Path:      "/_overcast/health",
				Timestamp: base.Add(time.Duration(i)*time.Second + time.Duration(p)*time.Millisecond),
			})
		}
	}

	// Then: every user trace survives — not merely most of them
	for i := 0; i < floor; i++ {
		if _, ok := buf.Get("user-" + strconv.Itoa(i)); !ok {
			t.Fatalf("user-%d was evicted by internal polling", i)
		}
	}
}

// The internal ring is capped absolutely as well as proportionally, so that
// raising the user floor later does not silently license thousands of retained
// health checks.
func TestBuffer_internalRingIsCappedBothWays(t *testing.T) {
	for _, tc := range []struct {
		name  string
		floor int
		want  int
	}{
		{"proportional below the absolute cap", 50, 10},
		{"default floor sits exactly on the cap", 1000, maxInternalRing},
		{"a raised floor does not raise it further", 10000, maxInternalRing},
		{"a tiny floor still keeps one", 3, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewBuffer(tc.floor).internal.cap(); got != tc.want {
				t.Errorf("internal ring cap = %d, want %d", got, tc.want)
			}
		})
	}
}

// Listing must not materialise the whole ring to return one page. This is the
// property that makes a deeper ceiling affordable on the read path, and the UI
// polls it once a second.
func TestBuffer_listSummariesStopsAtLimit(t *testing.T) {
	// Given: a full buffer of user traces
	buf := NewBuffer(500)
	base := time.Unix(0, 0)
	for i := 0; i < 500; i++ {
		addTrace(buf, traceSpec{
			RequestID: "req-" + strconv.Itoa(i),
			Path:      "/",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}

	// When: one page is requested
	got, cursor := buf.ListSummaries(ListFilter{Limit: 10})

	// Then: exactly the ten newest come back, newest first, with a cursor
	if len(got) != 10 {
		t.Fatalf("len = %d, want 10", len(got))
	}
	if got[0].RequestID != "req-499" {
		t.Errorf("first = %q, want req-499", got[0].RequestID)
	}
	if got[9].RequestID != "req-490" {
		t.Errorf("last = %q, want req-490", got[9].RequestID)
	}
	if cursor != "req-490" {
		t.Errorf("cursor = %q, want req-490", cursor)
	}
}

// Two rings, one ordering: the list is a merge, so an internal trace recorded
// between two user traces still appears between them.
func TestBuffer_listMergesBothRingsByTimestamp(t *testing.T) {
	// Given: user and internal traces interleaved in time
	buf := NewBuffer(50)
	base := time.Unix(0, 0)
	addTrace(buf, traceSpec{RequestID: "user-old", Path: "/", Timestamp: base})
	addTrace(buf, traceSpec{RequestID: "int-mid", Path: "/_overcast/health", Timestamp: base.Add(time.Second)})
	addTrace(buf, traceSpec{RequestID: "user-new", Path: "/", Timestamp: base.Add(2 * time.Second)})

	// When: they are listed
	got, _ := buf.ListSummaries(ListFilter{Limit: 10})

	// Then: strict newest-first order across both rings
	want := []string{"user-new", "int-mid", "user-old"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].RequestID != id {
			t.Errorf("[%d] = %q, want %q", i, got[i].RequestID, id)
		}
	}
}

// Eviction is oldest-first within a ring, with no slot inheritance to make it
// otherwise. The single-ring implementation needed a fairness dance here
// because a reclaimed internal slot could sit near the head; separate rings
// remove the situation rather than compensating for it.
func TestBuffer_evictionIsOldestFirst(t *testing.T) {
	buf := NewBuffer(3)
	base := time.Unix(0, 0)
	for i := 0; i < 5; i++ {
		addTrace(buf, traceSpec{
			RequestID: "req-" + strconv.Itoa(i),
			Path:      "/",
			Timestamp: base.Add(time.Duration(i) * time.Second),
		})
	}

	for _, gone := range []string{"req-0", "req-1"} {
		if _, ok := buf.Get(gone); ok {
			t.Errorf("%s should have been evicted", gone)
		}
	}
	for _, kept := range []string{"req-2", "req-3", "req-4"} {
		if _, ok := buf.Get(kept); !ok {
			t.Errorf("%s should have survived", kept)
		}
	}
}
