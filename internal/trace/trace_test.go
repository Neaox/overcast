package trace

import (
	"bytes"
	"net/http"
	"strconv"
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
	if entry.RequestBodyOmitted != OmitSize {
		t.Errorf("RequestBodyOmitted = %q, want %q", entry.RequestBodyOmitted, OmitSize)
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
	if entry.ResponseBodyOmitted != OmitSize {
		t.Errorf("ResponseBodyOmitted = %q, want %q", entry.ResponseBodyOmitted, OmitSize)
	}
	if got := cap(rec.entry.ResponseBody); got >= len(body) {
		t.Errorf("cap(ResponseBody) = %d, aliases the %d-byte original backing array", got, len(body))
	}
}

// A deploy dispatches thousands of hops through one trace. None of them may be
// dropped, and none of them retains a body — the call each hop records is a
// trace of its own, and that is where the bodies live. See MaxInlinedHopBodies.
func TestAddHop_keepsEveryHopAndNoBodies(t *testing.T) {
	// Given/When: many hops, some carrying large bodies
	rec := newTestRecorder()
	const hops = 200
	body := bytes.Repeat([]byte("b"), 1<<20)
	for i := 0; i < hops; i++ {
		rec.AddHop(Hop{
			CallerService:  "cloudformation",
			Service:        "sqs",
			Operation:      "CreateQueue",
			RequestID:      "child-" + strconv.Itoa(i),
			RequestBody:    body,
			ResponseBody:   body,
			ResponseStatus: 200,
		})
	}

	// Then: every hop is recorded, in order, with its metadata intact
	got := rec.Entry().Hops
	if len(got) != hops {
		t.Fatalf("hops = %d, want %d", len(got), hops)
	}
	for i, h := range got {
		if h.Order != i+1 || h.Operation != "CreateQueue" || h.ResponseStatus != 200 {
			t.Fatalf("hop %d lost metadata: %+v", i, h)
		}
	}

	// And: not one byte of body is held, whatever the caller passed. The
	// omission reasons are a read-time answer, so the recorder carries none.
	rec.mu.RLock()
	defer rec.mu.RUnlock()
	for i, h := range rec.entry.Hops {
		if len(h.RequestBody) != 0 || len(h.ResponseBody) != 0 {
			t.Fatalf("hop %d retained %d + %d body bytes", i, len(h.RequestBody), len(h.ResponseBody))
		}
		if h.RequestBodyOmitted != OmitNone || h.ResponseBodyOmitted != OmitNone {
			t.Fatalf("hop %d carries a stored omission reason (%q, %q); those are resolved on read",
				i, h.RequestBodyOmitted, h.ResponseBodyOmitted)
		}
	}
}
