package trace

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

// addAt registers a trace at a given time, failing or not.
func addAt(buf *Buffer, id string, at time.Time, status int) {
	rec := NewRecorder(id, at, http.MethodPost, "/", "localhost", "", http.Header{})
	rec.SetServiceInfo("sqs", "CreateQueue", "us-east-1")
	rec.SetResponse(http.Header{}, []byte(`{}`), status, 1<<20, false)
	buf.Add(rec)
}

func testPolicy() RetentionPolicy {
	return RetentionPolicy{Floor: 1000, Ceiling: 10000, Window: time.Hour, Pinned: 1000}
}

// This is the promise the whole plan exists to keep, and the criterion the
// design is answerable to:
//
//	You ran something big. Something in it failed. When you open the trace UI —
//	now, or after a meeting — the request that explains the failure is still
//	there, and you configured nothing in advance to make that true.
//
// Six thousand traces against a floor of a thousand, the failures early where
// recency eviction would discard them, and two hours on the clock.
func TestRetention_theFailuresSurviveTheDeployAndTheWait(t *testing.T) {
	// Given: a buffer with the shipped defaults and nothing configured
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(testPolicy(), clk)
	base := clk.Now()

	// When: a deploy pushes six thousand traces through it, three of which
	// fail in the first two hundred — the shape of a real failure, where the
	// error is early and the rollback noise is late
	failed := map[string]bool{"req-40": true, "req-120": true, "req-199": true}
	for i := 0; i < 6000; i++ {
		id := "req-" + strconv.Itoa(i)
		status := http.StatusOK
		if failed[id] {
			status = http.StatusBadRequest
		}
		addAt(buf, id, base.Add(time.Duration(i)*time.Millisecond), status)
	}

	// And: two hours pass before anyone looks
	clk.Add(2 * time.Hour)
	buf.Cull()

	// Then: every failure is still retrievable, in full
	for id := range failed {
		entry, ok := buf.Get(id)
		if !ok {
			t.Fatalf("%s was evicted — the trace that explains the failure is gone", id)
		}
		if entry.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", id, entry.StatusCode)
		}
		if len(entry.ResponseBody) == 0 {
			t.Errorf("%s: retained without its body", id)
		}
	}

	// And: the successful overflow above the floor has been reclaimed, so an
	// idle emulator does not sit on a whole deploy forever
	if got := buf.Len(); got > testPolicy().Floor+len(failed) {
		t.Errorf("retained %d traces two hours after the burst, want the floor plus the pinned failures", got)
	}
}

// Rule 2: the hour is sized for the gap between a deploy failing and a human
// looking, so the burst has to survive well past the deploy itself.
func TestRetention_burstSurvivesInsideTheWindow(t *testing.T) {
	// Given: a burst four times the floor
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(testPolicy(), clk)
	base := clk.Now()
	for i := 0; i < 4000; i++ {
		addAt(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond), http.StatusOK)
	}

	// When: someone looks a few minutes later
	clk.Add(5 * time.Minute)
	buf.Cull()

	// Then: the whole burst is still there — not just the newest thousand
	if got := buf.Len(); got != 4000 {
		t.Errorf("retained %d of 4000 inside the window", got)
	}
	if _, ok := buf.Get("req-0"); !ok {
		t.Error("the first trace of the burst was evicted while still inside the window")
	}
}

// Rule 2's other half: the overflow is not kept forever.
func TestRetention_overflowIsCulledBackToTheFloorAfterTheWindow(t *testing.T) {
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(testPolicy(), clk)
	base := clk.Now()
	for i := 0; i < 4000; i++ {
		addAt(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond), http.StatusOK)
	}

	clk.Add(90 * time.Minute)
	buf.Cull()

	if got := buf.Len(); got != testPolicy().Floor {
		t.Errorf("retained %d after the window, want the floor of %d", got, testPolicy().Floor)
	}
	// The floor keeps the newest, which is the one thing recency ordering is
	// right about.
	if _, ok := buf.Get("req-3999"); !ok {
		t.Error("the newest trace was culled")
	}
}

// The ceiling is what stops a burst being unbounded.
func TestRetention_theCeilingBounds(t *testing.T) {
	clk := clock.NewMock()
	policy := testPolicy()
	policy.Ceiling = 2000
	buf := NewBufferWithPolicy(policy, clk)
	base := clk.Now()
	for i := 0; i < 5000; i++ {
		addAt(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond), http.StatusOK)
	}

	if got := buf.Len(); got > policy.Ceiling {
		t.Errorf("retained %d, above the ceiling of %d", got, policy.Ceiling)
	}
	if _, ok := buf.Get("req-4999"); !ok {
		t.Error("the newest trace is missing")
	}
}

// A trace cannot be classified when it is admitted: the status is not known at
// request start, and a CloudFormation hop can fail minutes later on a goroutine
// outliving the request. Classification happens at eviction, which is the last
// possible moment and therefore the most informed one.
func TestRetention_pinsAFailureRecordedAfterAdmission(t *testing.T) {
	clk := clock.NewMock()
	policy := testPolicy()
	policy.Floor, policy.Ceiling = 10, 10
	buf := NewBufferWithPolicy(policy, clk)
	base := clk.Now()

	// Given: a trace admitted with no status at all
	rec := NewRecorder("late", base, http.MethodPost, "/", "localhost", "", http.Header{})
	buf.Add(rec)

	// When: it fails afterwards — as CloudFormation provisioning does — and
	// enough traffic arrives to evict it
	rec.AddHop(Hop{Service: "s3", Operation: "CreateBucket", RequestID: "child", ResponseStatus: 500, Error: "boom"})
	for i := 0; i < 50; i++ {
		addAt(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i+1)*time.Second), http.StatusOK)
	}

	// Then: it was pinned on the way out rather than dropped
	if _, ok := buf.Get("late"); !ok {
		t.Error("a trace that failed after admission was evicted; classification must happen at eviction")
	}
}

// Pinning is not unbounded either.
func TestRetention_pinnedRingIsCapped(t *testing.T) {
	clk := clock.NewMock()
	policy := testPolicy()
	policy.Floor, policy.Ceiling, policy.Pinned = 10, 10, 5
	buf := NewBufferWithPolicy(policy, clk)
	base := clk.Now()

	for i := 0; i < 100; i++ {
		addAt(buf, "fail-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Second), http.StatusInternalServerError)
	}

	// The newest failures win: the pinned ring evicts oldest-first like any
	// other, so a long-running emulator keeps the most recent problems.
	if _, ok := buf.Get("fail-99"); !ok {
		t.Error("the newest failure is missing")
	}
	if _, ok := buf.Get("fail-0"); ok {
		t.Error("the oldest failure survived a capped pinned ring")
	}
}
