package trace

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
)

// Retention that silently drops things is indistinguishable from a bug. These
// counters are what lets the UI say "1,204 older traces dropped, aged out after
// 1h" instead of ending a list with nothing and letting the reader wonder
// whether their request was never traced.
func TestStats_countsWhatWasDroppedAndWhy(t *testing.T) {
	clk := clock.NewMock()
	// The ceiling must exceed the floor, or there is no overflow for the window
	// to reclaim — rule 1 keeps the floor whatever the clock says.
	buf := NewBufferWithPolicy(RetentionPolicy{Floor: 10, Ceiling: 20, Window: time.Hour, Pinned: 5}, clk)
	base := clk.Now()

	// Given: more traffic than the ceiling holds, so the oldest are evicted
	for i := 0; i < 50; i++ {
		addAt(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond), http.StatusOK)
	}

	stats := buf.Stats()
	if stats.Dropped.Capacity == 0 {
		t.Error("nothing counted as dropped for capacity, though 50 traces went into a ceiling of 20")
	}
	if stats.Dropped.Aged != 0 {
		t.Errorf("Dropped.Aged = %d before any time passed", stats.Dropped.Aged)
	}

	// When: the window passes and the overflow is culled
	beforeAged := stats.Dropped.Aged
	clk.Add(2 * time.Hour)
	for i := 50; i < 65; i++ {
		addAt(buf, "req-"+strconv.Itoa(i), clk.Now().Add(time.Duration(i)*time.Millisecond), http.StatusOK)
	}

	// Then: the age cull is counted separately, because "we ran out of room"
	// and "you left it too long" are different answers to the same question
	if got := buf.Stats().Dropped.Aged; got <= beforeAged {
		t.Errorf("Dropped.Aged = %d, want it to have grown once the window passed", got)
	}
}

// The oldest retained timestamp is the other half of the sentence: it tells the
// reader how far back what they are looking at actually goes.
func TestStats_reportsTheOldestRetainedTrace(t *testing.T) {
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{Floor: 5, Ceiling: 5, Window: time.Hour, Pinned: 5}, clk)
	base := clk.Now()
	for i := 0; i < 20; i++ {
		addAt(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Second), http.StatusOK)
	}

	stats := buf.Stats()
	// Five retained, so the oldest is req-15.
	want := base.Add(15 * time.Second)
	if !stats.OldestRetained.Equal(want) {
		t.Errorf("OldestRetained = %v, want %v", stats.OldestRetained, want)
	}
}

// The occupancy split is what makes "why is this still here?" answerable: a
// pinned failure outliving everything around it looks like a bug until the
// numbers say pinning is why.
func TestStats_reportsOccupancyAndBudgets(t *testing.T) {
	clk := clock.NewMock()
	policy := RetentionPolicy{Floor: 10, Ceiling: 10, Window: time.Hour, Pinned: 5, Bytes: 1 << 20}
	buf := NewBufferWithPolicy(policy, clk)
	base := clk.Now()

	addAt(buf, "failed", base, http.StatusInternalServerError)
	for i := 0; i < 20; i++ {
		addAt(buf, "ok-"+strconv.Itoa(i), base.Add(time.Duration(i+1)*time.Second), http.StatusOK)
	}
	addTrace(buf, traceSpec{RequestID: "poll", Path: "/_overcast/health", Timestamp: base})

	stats := buf.Stats()
	if stats.Pinned == 0 {
		t.Error("Pinned = 0 though a failure was evicted from the live ring")
	}
	if stats.Live == 0 {
		t.Error("Live = 0 though traces are retained")
	}
	if stats.Internal == 0 {
		t.Error("Internal = 0 though a health poll was recorded")
	}
	if stats.BytesBudget != policy.Bytes {
		t.Errorf("BytesBudget = %d, want %d", stats.BytesBudget, policy.Bytes)
	}
	if stats.Window != policy.Window {
		t.Errorf("Window = %v, want %v", stats.Window, policy.Window)
	}
}

// A pinned trace is listed like any other, so the row needs to say why it
// outlived its neighbours.
func TestSummary_marksAPinnedTrace(t *testing.T) {
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{Floor: 5, Ceiling: 5, Window: time.Hour, Pinned: 5}, clk)
	base := clk.Now()

	addAt(buf, "failed", base, http.StatusInternalServerError)
	for i := 0; i < 20; i++ {
		addAt(buf, "ok-"+strconv.Itoa(i), base.Add(time.Duration(i+1)*time.Second), http.StatusOK)
	}

	summaries, _ := buf.ListSummaries(ListFilter{Limit: 100})
	var found bool
	for _, s := range summaries {
		if s.RequestID == "failed" {
			found = true
			if !s.Pinned {
				t.Error("the retained failure is not marked pinned, so a reader cannot tell why it is still here")
			}
			continue
		}
		if s.Pinned {
			t.Errorf("%s is marked pinned but never failed", s.RequestID)
		}
	}
	if !found {
		t.Fatal("the pinned failure is missing from the list")
	}
}
