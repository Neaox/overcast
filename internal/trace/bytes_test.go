package trace

import (
	"bytes"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

// addSized registers a trace carrying a response body of n bytes and settles
// it, as the middleware does once the handler has returned.
func addSized(buf *Buffer, id string, at time.Time, status, n int) {
	rec := NewRecorder(id, at, http.MethodPut, "/bucket/key", "localhost", "", http.Header{})
	rec.SetServiceInfo("s3", "PutObject", "us-east-1")
	rec.SetRequestBody(bytes.Repeat([]byte("x"), n), false, int64(n))
	rec.SetResponse(http.Header{}, []byte(`{}`), status, 1<<20, false)
	buf.Add(rec)
	buf.Settle(rec)
}

func backstopPolicy(budget int64) RetentionPolicy {
	return RetentionPolicy{Floor: 10, Ceiling: 1000, Window: time.Hour, Pinned: 100, Bytes: budget}
}

// The ceiling is a count, and counts are a poor proxy for memory when one trace
// can be a thousand times the size of another. Ten thousand small traces are
// tens of megabytes; ten thousand 1 MiB uploads are ten gigabytes, and a
// seeding script does exactly that.
func TestBytes_budgetBoundsABurstOfLargeTraces(t *testing.T) {
	// Given: a budget of roughly twenty 64 KiB traces
	const body = 64 << 10
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(backstopPolicy(20*body), clk)
	base := clk.Now()

	// When: a hundred of them arrive, well inside both the window and the
	// ceiling, so only the budget can stop them
	for i := 0; i < 100; i++ {
		addSized(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond), http.StatusOK, body)
	}

	// Then: retention is bounded by bytes, not by the count that would have
	// allowed all hundred
	if got := buf.Len(); got >= 100 {
		t.Errorf("retained %d traces; the byte budget never bound", got)
	}
	if got := buf.RetainedBytes(); got > backstopPolicy(20*body).Bytes {
		t.Errorf("retained %d bytes, above the budget of %d", got, 20*body)
	}
	// And the newest survive, as they do under every other rule here.
	if _, ok := buf.Get("req-99"); !ok {
		t.Error("the newest trace was evicted by the byte budget")
	}
}

// The budget is a backstop on the burst, not an override of the promise. Rule 1
// says the newest Floor traces are always retained, and a floor the operator
// configured is their call to make.
func TestBytes_budgetNeverEvictsBelowTheFloor(t *testing.T) {
	// Given: a budget far smaller than the floor's worth of traces
	const body = 64 << 10
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(backstopPolicy(body), clk)
	base := clk.Now()

	// When: exactly the floor arrives, all of them large
	for i := 0; i < 10; i++ {
		addSized(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond), http.StatusOK, body)
	}

	// Then: the floor is intact even though it is over budget
	if got := buf.Len(); got != 10 {
		t.Errorf("retained %d, want the floor of 10 — the backstop must not override rule 1", got)
	}
}

// Pinned failures are what someone came back for; the backstop reclaims the
// ordinary overflow instead.
func TestBytes_budgetReclaimsUnpinnedBeforePinned(t *testing.T) {
	const body = 64 << 10
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(backstopPolicy(20*body), clk)
	base := clk.Now()

	// Given: an early failure, then a hundred large successes on top of it
	addSized(buf, "failed", base, http.StatusInternalServerError, body)
	for i := 0; i < 100; i++ {
		addSized(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i+1)*time.Millisecond), http.StatusOK, body)
	}

	// Then: the failure is still there, having been pinned on its way out
	if _, ok := buf.Get("failed"); !ok {
		t.Error("the byte budget reclaimed a pinned failure while unpinned traces remained")
	}
}

// Accounting has to come back down, or the budget ratchets shut: every trace
// evicted for any reason must return its bytes.
func TestBytes_accountingIsReleasedOnEviction(t *testing.T) {
	const body = 32 << 10
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(backstopPolicy(1<<30), clk) // budget never binds
	base := clk.Now()

	for i := 0; i < 200; i++ {
		addSized(buf, "req-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond), http.StatusOK, body)
	}

	// Retention is capped at the ceiling of 1000, so nothing was evicted yet;
	// the total should track what is actually held.
	held := buf.Len()
	if got, want := buf.RetainedBytes(), int64(held*body); got < want {
		t.Fatalf("RetainedBytes = %d for %d traces, want at least %d", got, held, want)
	}

	// Age everything out and the total must fall back to the floor's worth,
	// not stay at the burst's peak.
	clk.Add(2 * time.Hour)
	buf.Cull()
	if got, want := buf.RetainedBytes(), int64(buf.Len()*body*2); got > want {
		t.Errorf("RetainedBytes = %d after culling to %d traces; accounting did not come back down",
			got, buf.Len())
	}
}
