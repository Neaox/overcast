package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// countingBody reports how many bytes were actually pulled off the wire, so a
// test can tell "read nothing" apart from "read and threw away".
type countingBody struct {
	r    io.Reader
	read int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read += n
	return n, err
}

func (b *countingBody) Close() error { return nil }

func requestWithBody(payload []byte) (*http.Request, *countingBody) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	src := &countingBody{r: bytes.NewReader(payload)}
	req.Body = src
	req.ContentLength = int64(len(payload))
	return req, src
}

// The hot path is a request nobody logs. A handler that never touches the body
// must not cause it to be read or buffered.
func TestTeeRequestBody_handlerThatDoesNotReadCostsNoRead(t *testing.T) {
	// Given: a request with a body, tee'd for capture
	req, src := requestWithBody(bytes.Repeat([]byte("x"), 4096))
	c := teeRequestBody(req, maxLoggedRequestBody)

	// When: the handler returns without reading it
	c.seal()

	// Then: nothing was pulled off the wire and nothing was buffered
	if src.read != 0 {
		t.Errorf("read %d bytes from the body, want 0", src.read)
	}
	if len(c.buf) != 0 {
		t.Errorf("buffered %d bytes, want 0", len(c.buf))
	}
}

// What the handler reads is what gets captured — the tee must not change the
// bytes the handler sees.
func TestTeeRequestBody_capturesWhatTheHandlerReads(t *testing.T) {
	// Given: a request with a body, tee'd for capture
	payload := []byte(`{"QueueUrl":"http://localhost:4566/000000000000/q"}`)
	req, _ := requestWithBody(payload)
	c := teeRequestBody(req, maxLoggedRequestBody)

	// When: the handler reads the body in full
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("handler read: %v", err)
	}
	c.seal()

	// Then: the handler saw the real body and the capture holds a copy
	if !bytes.Equal(got, payload) {
		t.Fatalf("handler body = %q, want %q", got, payload)
	}
	if !bytes.Equal(c.body(), payload) {
		t.Fatalf("captured body = %q, want %q", c.body(), payload)
	}
	if c.truncated {
		t.Error("capture reported truncation for a body under the limit")
	}
}

// A request rejected before the handler read anything still has to log what
// the client sent, so the failure path tops the capture up from the remainder.
func TestRequestCapture_bodyTopsUpFromUnreadRemainder(t *testing.T) {
	// Given: a tee'd request whose handler read nothing
	payload := []byte("Action=SendMessage&MessageBody=hello")
	req, _ := requestWithBody(payload)
	c := teeRequestBody(req, maxLoggedRequestBody)
	c.seal()

	// When: the failure path asks for the body
	got := c.body()

	// Then: it is the full request body
	if !bytes.Equal(got, payload) {
		t.Fatalf("body() = %q, want %q", got, payload)
	}
}

// The capture is bounded so a large upload is never held whole for a log line.
func TestTeeRequestBody_truncatesAtTheLimit(t *testing.T) {
	// Given: a tee'd request with a body larger than the limit
	const limit = 16
	payload := bytes.Repeat([]byte("x"), 4096)
	req, _ := requestWithBody(payload)
	c := teeRequestBody(req, limit)

	// When: the handler reads all of it
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("handler read: %v", err)
	}
	c.seal()

	// Then: the handler still saw everything, and the capture kept the
	// bounded prefix and said so
	if len(got) != len(payload) {
		t.Fatalf("handler saw %d bytes, want %d", len(got), len(payload))
	}
	if len(c.buf) != limit {
		t.Errorf("captured %d bytes, want %d", len(c.buf), limit)
	}
	if !c.truncated {
		t.Error("expected truncated to be true")
	}
}

// The eager capture must leave the handler a complete body: it reads a bounded
// prefix and splices it back in front of the remainder.
func TestReadRequestBody_handlerStillSeesTheWholeBody(t *testing.T) {
	// Given: a request whose body is larger than the capture limit
	const limit = 16
	payload := bytes.Repeat([]byte("y"), 4096)
	req, _ := requestWithBody(payload)

	// When: the body is captured eagerly and then read by the handler
	c := readRequestBody(req, limit)
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("handler read: %v", err)
	}

	// Then: the handler saw the complete body, and the capture is bounded
	if !bytes.Equal(got, payload) {
		t.Fatalf("handler saw %d bytes, want the original %d", len(got), len(payload))
	}
	if len(c.buf) != limit {
		t.Errorf("captured %d bytes, want %d", len(c.buf), limit)
	}
	if !c.truncated {
		t.Error("expected truncated to be true")
	}
}

// A truncated capture must be copied out of the read buffer: keeping a prefix
// of it aliases a backing array io.ReadAll grew geometrically, pinning far
// more than the limit in the ring buffer until eviction.
func TestReadRequestBody_truncatedCaptureDoesNotAliasReadBuffer(t *testing.T) {
	// Given: a request body far larger than the capture limit
	const limit = 16
	payload := bytes.Repeat([]byte("z"), 4096)
	req, _ := requestWithBody(payload)

	// When: it is captured eagerly
	c := readRequestBody(req, limit)

	// Then: the stored slice is a bounded copy, not a view of the read buffer
	if got := cap(c.buf); got > limit*2 {
		t.Errorf("cap(buf) = %d, aliases the read buffer instead of copying %d bytes", got, limit)
	}
}

// A body sitting exactly on the limit is complete, not truncated.
func TestReadRequestBody_bodyExactlyAtLimitIsNotTruncated(t *testing.T) {
	// Given: a request body the same size as the limit
	const limit = 16
	payload := bytes.Repeat([]byte("w"), limit)
	req, _ := requestWithBody(payload)

	// When: it is captured eagerly
	c := readRequestBody(req, limit)

	// Then: the whole body is captured and truncation is not claimed
	if !bytes.Equal(c.buf, payload) {
		t.Fatalf("captured %q, want %q", c.buf, payload)
	}
	if c.truncated {
		t.Error("body exactly at the limit reported as truncated")
	}
}

// A bodyless request costs no wrapper and no buffer.
func TestTeeRequestBody_noBody(t *testing.T) {
	// Given: a request with no body
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Body = http.NoBody

	// When: it is tee'd
	c := teeRequestBody(req, maxLoggedRequestBody)

	// Then: r.Body is untouched and the capture is empty
	if req.Body != http.NoBody {
		t.Error("r.Body was wrapped for a bodyless request")
	}
	if len(c.body()) != 0 {
		t.Errorf("captured %d bytes for a bodyless request", len(c.body()))
	}
}

// Content-Length is the honest answer for the full request size, because a
// bounded capture cannot count what it deliberately did not read.
func TestRequestCapture_sizePrefersContentLength(t *testing.T) {
	// Given: a truncated capture of a request that declared its length
	const limit = 16
	req, _ := requestWithBody(bytes.Repeat([]byte("q"), 4096))
	c := readRequestBody(req, limit)

	// When/Then: the reported size is the declared one
	if got := c.size(); got != 4096 {
		t.Errorf("size() = %d, want 4096", got)
	}
}

func TestRequestCapture_sizeFallsBackToCapturedLength(t *testing.T) {
	// Given: a capture of a request that declared no length (chunked upload)
	req, _ := requestWithBody([]byte("hello"))
	req.ContentLength = -1

	// When: it is captured eagerly
	c := readRequestBody(req, maxLoggedRequestBody)

	// Then: the captured length stands in
	if got := c.size(); got != 5 {
		t.Errorf("size() = %d, want 5", got)
	}
}
