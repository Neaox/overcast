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

// A truncated request body must be copied out of the caller's slice: keeping
// body[:maxBody] aliases the full backing array, pinning an arbitrarily large
// upload in the ring buffer until eviction.
func TestSetRequestBody_truncationDoesNotAliasOriginalBackingArray(t *testing.T) {
	// Given: a request body far larger than the capture cap
	const maxBody = 16
	body := bytes.Repeat([]byte("x"), 4096)
	rec := newTestRecorder()

	// When: the body is captured with truncation
	rec.SetRequestBody(body, maxBody)

	// Then: the stored slice is a bounded copy, not a view of the original
	entry := rec.Entry()
	if len(entry.RequestBody) != maxBody {
		t.Fatalf("len(RequestBody) = %d, want %d", len(entry.RequestBody), maxBody)
	}
	if !entry.RequestBodyTruncated {
		t.Error("expected RequestBodyTruncated true")
	}
	if got := cap(rec.entry.RequestBody); got >= len(body) {
		t.Errorf("cap(RequestBody) = %d, aliases the %d-byte original backing array", got, len(body))
	}
	if entry.RequestSize != int64(len(body)) {
		t.Errorf("RequestSize = %d, want %d", entry.RequestSize, len(body))
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
