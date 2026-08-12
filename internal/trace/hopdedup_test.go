package trace

import (
	"bytes"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// child builds a trace for an internal dispatch — the thing a hop is a record
// of — and registers it, returning its request ID.
func child(t *testing.T, buf *Buffer, id string, req, resp []byte, at time.Time) string {
	t.Helper()
	rec := NewRecorder(id, at, http.MethodPost, "/", "localhost", "", http.Header{})
	rec.SetServiceInfo("ssm", "PutParameter", "us-east-1")
	rec.SetRequestBody(req, false, int64(len(req)))
	rec.SetResponse(http.Header{}, resp, http.StatusOK, 1<<20, false)
	buf.Add(rec)
	return id
}

// A hop is a dispatched request with a trace of its own, so the bodies it
// reports are that trace's — held once, not twice.
func TestBuffer_hopBodiesResolveFromTheCalleeTrace(t *testing.T) {
	// Given: a child trace, and a parent whose hop points at it
	buf := NewBuffer(10)
	base := time.Unix(0, 0)
	childID := child(t, buf, "child-1", []byte(`{"Name":"/p"}`), []byte(`{"Version":1}`), base)

	parent := NewRecorder("parent-1", base.Add(-time.Second), http.MethodPost, "/", "localhost", "", http.Header{})
	parent.AddHop(Hop{Service: "ssm", Operation: "PutParameter", RequestID: childID, ResponseStatus: 200})
	buf.Add(parent)

	// When: the parent is read
	entry, ok := buf.Get("parent-1")
	if !ok {
		t.Fatal("parent trace missing")
	}

	// Then: the hop carries the callee's bodies, with nothing marked omitted
	hop := entry.Hops[0]
	if string(hop.RequestBody) != `{"Name":"/p"}` {
		t.Errorf("hop RequestBody = %q, want the callee's request", hop.RequestBody)
	}
	if string(hop.ResponseBody) != `{"Version":1}` {
		t.Errorf("hop ResponseBody = %q, want the callee's response", hop.ResponseBody)
	}
	if hop.RequestBodyOmitted != OmitNone || hop.ResponseBodyOmitted != OmitNone {
		t.Errorf("omission = (%q, %q), want neither", hop.RequestBodyOmitted, hop.ResponseBodyOmitted)
	}
}

// The parent can outlive the calls it made. Saying so beats an empty panel.
func TestBuffer_hopBodyReportsEvictedWhenCalleeIsGone(t *testing.T) {
	// Given: a parent whose hop points at a trace that was never retained
	buf := NewBuffer(10)
	parent := NewRecorder("parent-1", time.Unix(0, 0), http.MethodPost, "/", "localhost", "", http.Header{})
	parent.AddHop(Hop{Service: "ssm", Operation: "PutParameter", RequestID: "long-gone", ResponseStatus: 200})
	buf.Add(parent)

	// When: the parent is read
	entry, _ := buf.Get("parent-1")

	// Then: the hop says the body is no longer retained, rather than showing
	// nothing and letting a reader assume there was never a body
	hop := entry.Hops[0]
	if hop.RequestBodyOmitted != OmitEvicted || hop.ResponseBodyOmitted != OmitEvicted {
		t.Errorf("omission = (%q, %q), want both %q", hop.RequestBodyOmitted, hop.ResponseBodyOmitted, OmitEvicted)
	}
	// The hop itself survives in full: its timing, status and ordering are
	// what the reader is looking at.
	if hop.Operation != "PutParameter" || hop.ResponseStatus != 200 {
		t.Errorf("hop lost its metadata: %+v", hop)
	}
}

// The memory claim, asserted directly: a deploy's hop bodies are no longer
// pinned in the ring by the parent, because the parent never took a copy.
func TestAddHop_retainsNoBodies(t *testing.T) {
	// Given/When: a hop is recorded carrying a large body
	rec := newTestRecorder()
	rec.AddHop(Hop{
		Service:      "s3",
		Operation:    "PutObject",
		RequestID:    "child-1",
		RequestBody:  bytes.Repeat([]byte("x"), 1<<20),
		ResponseBody: bytes.Repeat([]byte("y"), 1<<20),
	})

	// Then: nothing of either body is held
	rec.mu.RLock()
	defer rec.mu.RUnlock()
	stored := rec.entry.Hops[0]
	if len(stored.RequestBody) != 0 || len(stored.ResponseBody) != 0 {
		t.Errorf("hop retained %d + %d body bytes, want none",
			len(stored.RequestBody), len(stored.ResponseBody))
	}
}

// A deploy dispatches thousands of hops through one trace. Resolving every
// body into one response is unbounded, so the response is what carries the
// budget now — the ring no longer does.
func TestBuffer_inlinedHopBodiesAreBounded(t *testing.T) {
	// Given: a parent with more hop bodies than one response should inline
	buf := NewBuffer(200)
	base := time.Unix(0, 0)
	body := bytes.Repeat([]byte("z"), 1<<20)
	parent := NewRecorder("parent-1", base, http.MethodPost, "/", "localhost", "", http.Header{})
	for i := 0; i < 12; i++ {
		id := "child-" + strconv.Itoa(i)
		child(t, buf, id, body, nil, base.Add(time.Duration(i)*time.Millisecond))
		parent.AddHop(Hop{Service: "ssm", Operation: "PutParameter", RequestID: id, ResponseStatus: 200})
	}
	buf.Add(parent)

	// When: the parent is read
	entry, _ := buf.Get("parent-1")

	// Then: the response stops inlining once the budget is spent, and says so
	// — every hop still reports its metadata either way
	var inlined, budgeted int
	for _, hop := range entry.Hops {
		// Not a switch: the exhaustive linter would want every OmitReason
		// enumerated, and the point here is that only these two may occur.
		switch reason := hop.RequestBodyOmitted; {
		case reason == OmitNone:
			inlined++
		case reason == OmitTraceBudget:
			budgeted++
		default:
			t.Errorf("hop %s: unexpected omission %q", hop.ID, reason)
		}
	}
	if inlined == 0 {
		t.Error("no hop bodies were inlined at all")
	}
	if budgeted == 0 {
		t.Error("the budget never bound, so this test proves nothing")
	}
	if len(entry.Hops) != 12 {
		t.Errorf("hops = %d, want all 12 regardless of the budget", len(entry.Hops))
	}
}
