package trace

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
)

// deployWithFailure registers a parent trace that dispatched `hops` internal
// calls, each of which is a trace in its own right, and fails the parent.
func deployWithFailure(buf *Buffer, at time.Time, hops int) *Recorder {
	parent := NewRecorder("deploy", at, http.MethodPost, "/", "localhost", "", http.Header{})
	parent.SetServiceInfo("cloudformation", "CreateStack", "us-east-1")
	buf.Add(parent)

	for i := 0; i < hops; i++ {
		id := "call-" + strconv.Itoa(i)
		child := NewRecorder(id, at.Add(time.Duration(i+1)*time.Millisecond),
			http.MethodPost, "/", "localhost", "", http.Header{})
		child.SetServiceInfo("ssm", "PutParameter", "us-east-1")
		child.SetRequestBody([]byte(`{"Name":"/p/`+strconv.Itoa(i)+`"}`), false, 16)
		child.SetResponse(http.Header{}, []byte(`{"Version":1}`), http.StatusOK, 1<<20, false)
		buf.Add(child)
		buf.Settle(child)
		parent.AddHop(Hop{Service: "ssm", Operation: "PutParameter", RequestID: id, ResponseStatus: 200})
	}

	parent.SetResponse(http.Header{}, []byte(`{"error":"rollback"}`), http.StatusBadRequest, 1<<20, false)
	buf.Settle(parent)
	return parent
}

// Pinning a failure that cannot show what it did is half a rescue. The calls a
// deploy made are traces of their own, and they are newer than the parent — so
// FIFO would evict the parent first and the children after it, leaving a pinned
// trace whose every hop body reports `evicted`.
func TestFamily_pinningKeepsTheCallsTheFailureMade(t *testing.T) {
	// Given: a failed deploy that dispatched ten calls
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{Floor: 20, Ceiling: 20, Window: time.Hour, Pinned: 100}, clk)
	base := clk.Now()
	deployWithFailure(buf, base, 10)

	// When: far more traffic arrives than the buffer can hold
	for i := 0; i < 200; i++ {
		addAt(buf, "later-"+strconv.Itoa(i), base.Add(time.Duration(i+100)*time.Millisecond), http.StatusOK)
	}

	// Then: the failure is still there
	entry, ok := buf.Get("deploy")
	if !ok {
		t.Fatal("the failed deploy was evicted")
	}
	// And so is what it did: every hop resolves its body rather than reporting
	// that the call it names is gone.
	for _, hop := range entry.Hops {
		if hop.RequestBodyOmitted == OmitEvicted {
			t.Errorf("hop %s (%s): the call it records was evicted out from under the pinned parent",
				hop.ID, hop.RequestID)
		}
		if len(hop.RequestBody) == 0 {
			t.Errorf("hop %s: no request body resolved", hop.ID)
		}
	}
}

// A family rides along in whatever room is left. It must never displace another
// failure, because two failures are two things somebody may come back for and a
// deploy's children are not more important than either.
func TestFamily_neverDisplacesAnotherFailure(t *testing.T) {
	// Given: a pinned ring with room for a handful, and an earlier failure.
	// The floor holds the deploy and its calls while it runs — classification
	// happens at eviction, so a trace evicted before it fails cannot be saved,
	// and that is a limit of the design rather than something to test around.
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{Floor: 30, Ceiling: 30, Window: time.Hour, Pinned: 4}, clk)
	base := clk.Now()
	addAt(buf, "earlier-failure", base, http.StatusInternalServerError)

	// When: a failed deploy with more children than the ring could hold
	// arrives, and everything is evicted from the live ring
	deployWithFailure(buf, base.Add(time.Second), 20)
	for i := 0; i < 50; i++ {
		addAt(buf, "later-"+strconv.Itoa(i), base.Add(time.Duration(i+10)*time.Second), http.StatusOK)
	}

	// Then: both failures survive — the family took the room that was left,
	// not the room another failure was using
	if _, ok := buf.Get("earlier-failure"); !ok {
		t.Error("an earlier failure was displaced by a later failure's family")
	}
	if _, ok := buf.Get("deploy"); !ok {
		t.Error("the failed deploy itself was not retained")
	}
}

// The list is a merge across three rings, and a trace pinned while still live
// is reachable from two of them. It must appear once.
func TestFamily_aTraceInTwoRingsIsListedOnce(t *testing.T) {
	clk := clock.NewMock()
	buf := NewBufferWithPolicy(RetentionPolicy{Floor: 50, Ceiling: 50, Window: time.Hour, Pinned: 50}, clk)
	base := clk.Now()
	deployWithFailure(buf, base, 5)

	// Evict the parent — and only the parent — so it is pinned while its
	// children are still in the live ring. That is the state where one trace is
	// reachable from two rings, and a merge that does not notice yields it
	// twice. Six entries so far against a floor of fifty, so forty-five more
	// pushes exactly one out.
	for i := 0; i < 45; i++ {
		addAt(buf, "later-"+strconv.Itoa(i), base.Add(time.Duration(i+1)*time.Second), http.StatusOK)
	}
	if _, ok := buf.Get("deploy"); !ok {
		t.Fatal("the parent was not retained; the test is not in the state it means to test")
	}
	dual := 0
	buf.mu.RLock()
	for _, s := range buf.index {
		if s.inLive && s.inPinned {
			dual++
		}
	}
	buf.mu.RUnlock()
	if dual == 0 {
		t.Fatal("no trace is in two rings, so this test proves nothing")
	}

	seen := map[string]int{}
	summaries, _ := buf.ListSummaries(ListFilter{Limit: 200})
	for _, s := range summaries {
		seen[s.RequestID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("%s appears %d times in one page", id, n)
		}
	}
}
