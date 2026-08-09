package trace

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

func newTestRecorder() *Recorder {
	return NewRecorder("req-1", time.Unix(0, 0), "PUT", "/bucket/key", "localhost", "", http.Header{})
}

// The recorder records what the capture hands it: bounding and copying belong
// to the middleware, which is the only party that can decide how much of a
// body to read off the wire at all. (The bounded-copy guarantee itself is
// covered by TestReadRequestBody_truncatedCaptureDoesNotAliasReadBuffer in
// internal/middleware.)
func TestSetRequestBody_truncatedCapture(t *testing.T) {
	// Given: a capture that was truncated, from a request whose full size is
	// known from Content-Length
	body := bytes.Repeat([]byte("x"), 16)
	rec := newTestRecorder()

	// When: it is recorded
	rec.SetRequestBody(body, true, 4096)

	// Then: the entry reports the captured prefix, the truncation, and the
	// full request size rather than the captured length
	entry := rec.Entry()
	if len(entry.RequestBody) != len(body) {
		t.Fatalf("len(RequestBody) = %d, want %d", len(entry.RequestBody), len(body))
	}
	if !entry.RequestBodyTruncated {
		t.Error("expected RequestBodyTruncated true")
	}
	if entry.RequestSize != 4096 {
		t.Errorf("RequestSize = %d, want 4096", entry.RequestSize)
	}
}

// An unknown size (a chunked upload declares no Content-Length) falls back to
// what was actually captured.
func TestSetRequestBody_unknownSize(t *testing.T) {
	// Given: a capture of a body whose full size the caller could not know
	rec := newTestRecorder()

	// When: it is recorded with an unknown size
	rec.SetRequestBody([]byte("hello"), false, -1)

	// Then: the captured length stands in
	if got := rec.Entry().RequestSize; got != 5 {
		t.Errorf("RequestSize = %d, want 5", got)
	}
}

func TestSetResponse_truncationDoesNotAliasOriginalBackingArray(t *testing.T) {
	// Given: a response body far larger than the capture cap
	const maxBody = 16
	body := bytes.Repeat([]byte("y"), 4096)
	rec := newTestRecorder()

	// When: the response is captured with truncation
	rec.SetResponse(http.Header{}, body, http.StatusOK, maxBody, false)

	// Then: the stored slice is a bounded copy, not a view of the original
	entry := rec.Entry()
	if len(entry.ResponseBody) != maxBody {
		t.Fatalf("len(ResponseBody) = %d, want %d", len(entry.ResponseBody), maxBody)
	}
	if !entry.ResponseBodyTruncated {
		t.Error("expected ResponseBodyTruncated true")
	}
	if got := cap(rec.entry.ResponseBody); got >= len(body) {
		t.Errorf("cap(ResponseBody) = %d, aliases the %d-byte original backing array", got, len(body))
	}
}

// Hop bodies come from internal dispatch (CloudFormation provisioning) and
// are stored uncapped by callers; AddHop itself must bound them the same way
// top-level entries are bounded.
func TestAddHop_oversizedBodiesAreCappedAndCopied(t *testing.T) {
	// Given: hop request/response bodies larger than the hop cap
	big := bytes.Repeat([]byte("z"), MaxHopBody+4096)
	rec := newTestRecorder()

	// When: the hop is recorded
	rec.AddHop(Hop{
		CallerService: "cloudformation",
		Service:       "s3",
		Operation:     "PUT",
		RequestBody:   big,
		ResponseBody:  big,
	})

	// Then: both bodies are capped copies with truncation marked
	entry := rec.Entry()
	if len(entry.Hops) != 1 {
		t.Fatalf("hops = %d, want 1", len(entry.Hops))
	}
	h := entry.Hops[0]
	if len(h.RequestBody) != MaxHopBody {
		t.Errorf("len(hop RequestBody) = %d, want %d", len(h.RequestBody), MaxHopBody)
	}
	if len(h.ResponseBody) != MaxHopBody {
		t.Errorf("len(hop ResponseBody) = %d, want %d", len(h.ResponseBody), MaxHopBody)
	}
	if !h.RequestBodyTruncated || !h.ResponseBodyTruncated {
		t.Errorf("truncation flags = (%t, %t), want (true, true)", h.RequestBodyTruncated, h.ResponseBodyTruncated)
	}
	stored := rec.entry.Hops[0]
	if got := cap(stored.RequestBody); got >= len(big) {
		t.Errorf("cap(hop RequestBody) = %d, aliases the %d-byte original backing array", got, len(big))
	}
	if got := cap(stored.ResponseBody); got >= len(big) {
		t.Errorf("cap(hop ResponseBody) = %d, aliases the %d-byte original backing array", got, len(big))
	}
}

// The ring buffer retains live traces, so a trace's hop bodies stay resident
// for as long as the trace does. A deploy must not be able to pin unbounded
// memory — but it must also never lose a hop, so the budget costs bodies only.
func TestAddHop_bodyBudgetDropsBodiesNotHops(t *testing.T) {
	// Given: a trace whose hops each carry a full MaxHopBody request body
	body := bytes.Repeat([]byte("b"), MaxHopBody)
	rec := newTestRecorder()

	// When: enough hops arrive to spend the whole MaxHopBodyBytes budget,
	// plus two more
	const overBudget = MaxHopBodyBytes/MaxHopBody + 2
	for i := 0; i < overBudget; i++ {
		rec.AddHop(Hop{
			CallerService:  "cloudformation",
			Service:        "sqs",
			Operation:      "CreateQueue",
			RequestBody:    body,
			ResponseStatus: 200,
		})
	}

	// Then: every hop is recorded, in order, with its metadata intact
	hops := rec.Entry().Hops
	if len(hops) != overBudget {
		t.Fatalf("hops = %d, want %d — a hop was dropped for the body budget", len(hops), overBudget)
	}
	for i, h := range hops {
		if h.Order != i+1 || h.Operation != "CreateQueue" || h.ResponseStatus != 200 {
			t.Fatalf("hop %d lost metadata: %+v", i, h)
		}
	}

	// And: bodies stop being retained once the budget is spent, flagged with
	// the same truncation marker the per-hop cap uses
	kept, dropped := 0, 0
	for _, h := range hops {
		if len(h.RequestBody) > 0 {
			kept++
			if h.RequestBodyTruncated {
				t.Error("a retained body was flagged truncated")
			}
			continue
		}
		dropped++
		if !h.RequestBodyTruncated {
			t.Error("a dropped body was not flagged truncated")
		}
	}
	if kept != MaxHopBodyBytes/MaxHopBody {
		t.Errorf("retained bodies = %d, want %d", kept, MaxHopBodyBytes/MaxHopBody)
	}
	if dropped != 2 {
		t.Errorf("dropped bodies = %d, want 2", dropped)
	}
}

// A hop that carries no body at all costs nothing against the budget, so a
// deploy of body-less hops keeps recording them indefinitely.
func TestAddHop_bodylessHopsSpendNoBudget(t *testing.T) {
	// Given/When: many hops with no bodies
	rec := newTestRecorder()
	for i := 0; i < 1000; i++ {
		rec.AddHop(Hop{Service: "sqs", Operation: "GetQueueUrl", ResponseStatus: 200})
	}

	// Then: none of them is flagged truncated
	for i, h := range rec.Entry().Hops {
		if h.RequestBodyTruncated || h.ResponseBodyTruncated {
			t.Fatalf("hop %d flagged truncated despite carrying no body", i)
		}
	}
}

// A small hop body is stored as-is and not flagged truncated.
func TestAddHop_smallBodiesAreStoredVerbatim(t *testing.T) {
	rec := newTestRecorder()
	rec.AddHop(Hop{RequestBody: []byte("req"), ResponseBody: []byte("resp")})

	h := rec.Entry().Hops[0]
	if string(h.RequestBody) != "req" || string(h.ResponseBody) != "resp" {
		t.Errorf("hop bodies = (%q, %q), want (req, resp)", h.RequestBody, h.ResponseBody)
	}
	if h.RequestBodyTruncated || h.ResponseBodyTruncated {
		t.Error("small hop bodies must not be flagged truncated")
	}
}
