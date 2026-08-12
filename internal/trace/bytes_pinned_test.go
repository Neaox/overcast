package trace

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

// The byte budget has to be a bound, or it is not a backstop.
//
// Pinned failures were exempt from it and capped only by count, so the worst
// case was PinnedLimit traces of up to ~2 MiB each — around 2 GB at the shipped
// defaults — however small OVERCAST_DEBUG_TRACE_BYTES_MB was set. A developer
// who lowers the budget because their machine is struggling would have found it
// did nothing about the memory actually being held.
func TestBytes_budgetBoundsPinnedTracesToo(t *testing.T) {
	const body = 64 << 10
	clk := clock.NewMock()
	// A budget of ten bodies, against a pinned ring that would otherwise hold
	// a hundred of them.
	buf := NewBufferWithPolicy(RetentionPolicy{
		Floor: 2, Ceiling: 2, Window: time.Hour, Pinned: 100, Bytes: 10 * body,
	}, clk)
	base := clk.Now()

	// Given/When: a deploy in which every request fails, each carrying a body
	for i := 0; i < 60; i++ {
		addSized(buf, "fail-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond),
			http.StatusInternalServerError, body)
	}

	// Then: retention obeys the budget rather than the pinned count
	if got := buf.RetainedBytes(); got > 10*body {
		t.Errorf("retained %d bytes against a %d budget; pinned traces are unbounded", got, 10*body)
	}
	// And the newest failures are the ones kept: a byte budget that had to
	// reclaim failures should still surrender the oldest first.
	if _, ok := buf.Get("fail-59"); !ok {
		t.Error("the newest failure was reclaimed before older ones")
	}
	if _, ok := buf.Get("fail-0"); ok {
		t.Error("the oldest failure survived while the budget was exceeded")
	}
}

// Unpinned overflow is still reclaimed first. A failure is only surrendered
// once there is nothing cheaper left to give up.
func TestBytes_reclaimsUnpinnedOverflowBeforeAnyFailure(t *testing.T) {
	const body = 64 << 10
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{
		Floor: 2, Ceiling: 100, Window: time.Hour, Pinned: 100, Bytes: 12 * body,
	}, clk)
	base := clk.Now()

	// Given: one early failure, then plenty of ordinary traffic on top of it
	addSized(buf, "failed", base, http.StatusInternalServerError, body)
	for i := 0; i < 40; i++ {
		addSized(buf, "ok-"+strconv.Itoa(i), base.Add(time.Duration(i+1)*time.Millisecond),
			http.StatusOK, body)
	}

	// Then: the budget was met by giving up ordinary traces, and the failure
	// is still there — it is what somebody would come back for
	if _, ok := buf.Get("failed"); !ok {
		t.Error("a failure was reclaimed while unpinned traces were still available to give up")
	}
	if got := buf.RetainedBytes(); got > 12*body {
		t.Errorf("retained %d bytes against a %d budget", got, 12*body)
	}
}

// The floor still wins. Rule 1 is a promise, and a budget smaller than the
// floor's worth of traces is the operator's own configuration.
func TestBytes_budgetStillNeverEvictsBelowTheFloor(t *testing.T) {
	const body = 64 << 10
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{
		Floor: 10, Ceiling: 10, Window: time.Hour, Pinned: 0, Bytes: body,
	}, clk)
	base := clk.Now()
	for i := 0; i < 10; i++ {
		addSized(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond),
			http.StatusOK, body)
	}
	if got := buf.Len(); got != 10 {
		t.Errorf("retained %d, want the floor of 10", got)
	}
}
