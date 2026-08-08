package middleware

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// requestCapture is the one place a request body is copied for observability.
//
// Both Logger (which logs the body of a failed request) and DebugTrace (which
// stores it in the trace entry) need a snapshot of a request taken before the
// handler ran. Each used to take its own with an unbounded io.ReadAll, so a
// debug-on request read and held the body twice — and a debug-off request,
// which is every request in normal use, still paid a full read plus a copy of
// an upload no logger would ever print. DebugTrace publishes its capture on
// the request context and Logger reuses it, so a request is captured once.
//
// A capture is confined to the request goroutine, like http.Request.Body
// itself, and needs no locking.
type requestCapture struct {
	headers       http.Header // snapshot taken before the handler ran
	contentLength int64       // r.ContentLength: -1 when unknown
	limit         int

	src       io.ReadCloser // the real body, for a top-up after the handler
	buf       []byte
	truncated bool
	err       error
	sealed    bool // stop copying: the handler has returned
}

// maxLoggedRequestBody bounds the request body Logger keeps for a 5xx failure
// log. Failure logs exist to show what the client sent, and an AWS API request
// that needs more than this to diagnose is not one anybody reads out of a log
// line — while an S3 upload would otherwise be buffered in full.
const maxLoggedRequestBody = 64 << 10 // 64 KiB

type captureCtxKey struct{}

// withRequestCapture returns a context carrying c so later middleware reuses
// the capture instead of taking its own.
func withRequestCapture(ctx context.Context, c *requestCapture) context.Context {
	return context.WithValue(ctx, captureCtxKey{}, c)
}

// requestCaptureFromContext returns the capture an earlier middleware
// published, or nil.
func requestCaptureFromContext(ctx context.Context) *requestCapture {
	c, _ := ctx.Value(captureCtxKey{}).(*requestCapture)
	return c
}

// teeRequestBody installs a lazy tee on r.Body: nothing is read and no buffer
// is allocated until the handler itself reads, and at most limit bytes are
// retained. A request whose body the handler never touches costs one small
// wrapper.
func teeRequestBody(r *http.Request, limit int) *requestCapture {
	c := newCapture(r, limit)
	if c.src == nil {
		return c
	}
	r.Body = &teeBody{capture: c}
	return c
}

// readRequestBody eagerly reads up to limit bytes of r.Body and splices the
// buffered prefix back in front of the remainder, so the handler still sees
// the complete body. Callers that need the body *before* the handler runs
// (DebugTrace classifies Query-protocol requests from it, and pushes a partial
// trace entry) have to read it here; bounding that read is what keeps a large
// upload from being held whole in the ring buffer.
func readRequestBody(r *http.Request, limit int) *requestCapture {
	c := newCapture(r, limit)
	if c.src == nil {
		return c
	}
	// limit+1 so a body sitting exactly on the limit is not called truncated.
	buf, err := io.ReadAll(io.LimitReader(c.src, int64(limit)+1))
	if err != nil {
		c.err = err
	}
	if len(buf) > limit {
		// Copy: keeping buf[:limit] would alias a backing array that
		// io.ReadAll grows geometrically, over-retaining in the ring buffer.
		c.buf = append([]byte(nil), buf[:limit]...)
		c.truncated = true
	} else {
		c.buf = buf
	}
	c.sealed = true
	r.Body = &spliceBody{
		Reader: io.MultiReader(bytes.NewReader(buf), c.src),
		closer: c.src,
	}
	return c
}

func newCapture(r *http.Request, limit int) *requestCapture {
	c := &requestCapture{
		headers:       r.Header.Clone(),
		contentLength: r.ContentLength,
		limit:         limit,
	}
	if r.Body != nil && r.Body != http.NoBody {
		c.src = r.Body
	} else {
		c.sealed = true
	}
	return c
}

// record copies up to the remaining budget of p into the capture, unless
// capture has been sealed.
func (c *requestCapture) record(p []byte) {
	if c.sealed {
		return
	}
	c.appendBounded(p)
}

func (c *requestCapture) appendBounded(p []byte) {
	if len(p) == 0 {
		return
	}
	room := c.limit - len(c.buf)
	if room <= 0 {
		c.truncated = true
		return
	}
	if len(p) > room {
		p = p[:room]
		c.truncated = true
	}
	if c.buf == nil && c.contentLength > 0 {
		// Size the buffer once from what the request declares it holds,
		// rather than letting append double its way up to the limit: an
		// upload streamed past in 32 KiB chunks would otherwise allocate,
		// and immediately discard, most of the limit again on the way there.
		n := c.contentLength
		if n > int64(c.limit) {
			n = int64(c.limit)
		}
		c.buf = make([]byte, 0, n)
	}
	c.buf = append(c.buf, p...)
}

// seal ends passive capture. Called once the handler has returned so that a
// later drain (see DrainBody) does not copy bytes nobody will read.
func (c *requestCapture) seal() {
	c.sealed = true
}

// body returns the captured bytes, first topping the buffer up from any
// unread remainder. Only the failure path calls this, so a handler that
// rejected a request before reading its body still logs what the client sent
// without every successful request paying for the read.
func (c *requestCapture) body() []byte {
	if c.src == nil || c.truncated {
		return c.buf
	}
	if room := c.limit - len(c.buf); room > 0 {
		rest, err := io.ReadAll(io.LimitReader(c.src, int64(room)+1))
		if err != nil && c.err == nil {
			c.err = err
		}
		c.appendBounded(rest)
	}
	return c.buf
}

// size is the full request size. Content-Length is preferred because a
// bounded capture cannot count what it deliberately did not read; a chunked
// upload declares none, and there the captured length is the best available
// answer (and an exact one whenever nothing was truncated).
func (c *requestCapture) size() int64 {
	if c.contentLength >= 0 {
		return c.contentLength
	}
	return int64(len(c.buf))
}

// teeBody copies what the handler reads into the capture on its way past.
type teeBody struct {
	capture *requestCapture
}

func (b *teeBody) Read(p []byte) (int, error) {
	n, err := b.capture.src.Read(p)
	if n > 0 {
		b.capture.record(p[:n])
	}
	if err != nil && err != io.EOF && b.capture.err == nil {
		b.capture.err = err
	}
	return n, err
}

func (b *teeBody) Close() error { return b.capture.src.Close() }

// spliceBody serves an already-read prefix followed by the rest of the real
// body, while still closing the real body underneath.
type spliceBody struct {
	io.Reader
	closer io.Closer
}

func (b *spliceBody) Close() error { return b.closer.Close() }
