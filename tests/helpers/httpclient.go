package helpers

import (
	"io"
	"net/http"
	"sync"
	"time"
)

// This file retunes the process-global HTTP client for every test binary that
// links this package. The canonical test idiom (see tests/AGENTS.md) is
//
//	resp, err := http.DefaultClient.Do(req)
//	defer resp.Body.Close()
//
// and on Windows that idiom, unmodified, exhausts the OS. Closing a response
// body with unread bytes makes the transport tear the connection down instead
// of returning it to the pool, so every helpers.AssertStatus-style call — the
// overwhelming majority of the ~3000 requests an integration package makes —
// burns a fresh TCP connection. The client is the closing side, so each one
// pins a dynamic port in TIME_WAIT for two minutes, out of a range only 16384
// wide. A full `go test ./...` run burns ~11k ports; a second run started
// inside the two-minute window then fails with "Only one usage of each socket
// address" dial cascades across whole packages (measured 2026-08-25; a single
// 3.6s iam run alone left 783 sockets behind).
//
// Rewriting hundreds of call sites to drain before closing would fix today's
// tests and regress with tomorrow's, so the fix lives here instead: init()
// wraps http.DefaultClient's transport so Close drains short bodies first,
// letting the pool keep the connection. Pool limits are lifted to the same
// numbers tests/load settled on — DefaultTransport's 2 idle conns per host
// re-dials constantly under any parallelism, and MaxConnsPerHost keeps a dial
// burst under the ~200-entry Windows loopback accept backlog, which rejects
// (not queues) overflow.
//
// This package is linked only into test binaries, so production behaviour is
// untouched. Within a test binary the wrapper also applies to emulator code
// that calls out through http.DefaultClient (the AppSync HTTP resolver);
// that code reads bodies fully before closing, making the drain a no-op.
func init() {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport.MaxIdleConns = 0 // unlimited pool; the per-host caps below are the limit
		transport.MaxConnsPerHost = 128
		transport.MaxIdleConnsPerHost = 128
	}
	http.DefaultClient.Transport = drainOnCloseTransport{base: http.DefaultTransport}
}

const (
	// drainLimit bounds how much of an unread body Close will consume on the
	// pool's behalf. API responses here are small XML/JSON documents; anything
	// larger (a multi-megabyte S3 GetObject a test abandoned) is cheaper to
	// tear down than to read.
	drainLimit = 1 << 20

	// drainTimeout bounds how long Close will wait for those bytes. It exists
	// for response bodies that are still streaming — closing mid-stream is how
	// a test abandons a trace/SSE subscription, and a drain that blocked until
	// EOF would hang it forever. Buffered bytes drain in microseconds; only a
	// genuinely open stream waits this out, and it still gets closed.
	drainTimeout = 100 * time.Millisecond
)

// drainOnCloseTransport wraps response bodies so that Close drains any unread
// remainder (bounded by drainLimit/drainTimeout) before closing. A body read
// to EOF hands its connection back to the pool; one closed with bytes still
// buffered kills it.
type drainOnCloseTransport struct{ base http.RoundTripper }

func (t drainOnCloseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.Body == nil || resp.Body == http.NoBody {
		return resp, err
	}
	if resp.StatusCode == http.StatusSwitchingProtocols {
		// The body is the raw upgraded connection (io.ReadWriteCloser);
		// draining it would eat the peer's protocol frames.
		return resp, nil
	}
	resp.Body = &drainOnCloseBody{inner: resp.Body}
	return resp, nil
}

type drainOnCloseBody struct {
	inner io.ReadCloser
	once  sync.Once
}

func (b *drainOnCloseBody) Read(p []byte) (int, error) { return b.inner.Read(p) }

func (b *drainOnCloseBody) Close() error {
	b.once.Do(func() {
		// The drain runs on its own goroutine because Read blocks on a body
		// the server is still holding open. If the timeout fires, the
		// inner.Close below unblocks that Read (net/http bodies support Close
		// during Read) and the goroutine exits.
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = io.CopyN(io.Discard, b.inner, drainLimit)
		}()
		select {
		case <-done:
		case <-time.After(drainTimeout):
		}
	})
	return b.inner.Close()
}
