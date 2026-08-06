package middleware

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/trace"
)

// maxTraceBody is the largest request or response body captured into a trace
// entry. Bodies exceeding this are truncated.
const maxTraceBody = 1 << 20 // 1 MiB

// maxTraceLogFields is the per-entry cap on structured log field sizes.
const maxTraceLogFields = 16 << 10 // 16 KiB

// DebugTrace returns middleware that captures full request/response traces into
// a ring buffer when cfg.Debug is true. When debug is off the middleware is an
// identity function — chi calls it once at startup and the handler chain is
// unchanged.
func DebugTrace(cfg *config.Config, buf *trace.Buffer, clk clock.Clock) func(http.Handler) http.Handler {
	if !cfg.Debug {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := clk.Now()
			reqID := protocol.RequestIDFromContext(r.Context())

			requestHeaders := r.Header.Clone()
			var requestBody []byte
			if r.Body != nil {
				var readErr error
				requestBody, readErr = io.ReadAll(r.Body)
				r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(requestBody))
				if readErr != nil {
					requestBody = nil
				}
			}

			rec := trace.NewRecorder(reqID, start, r.Method, r.URL.Path, r.Host, r.URL.RawQuery, requestHeaders)
			rec.SetRequestBody(requestBody, maxTraceBody)
			rec.SetMeta(r.RemoteAddr, r.UserAgent(), "", "")
			rec.SetServiceInfo(detectService(r), detectOperation(r), RegionFromContext(r.Context(), cfg.Region))

			ctx := trace.ContextWithRecorder(r.Context(), rec)
			r = r.WithContext(ctx)

			// Early push: store a partial entry before the handler runs so the
			// trace is visible (with StatusCode=0, no duration) while the
			// request is still being processed.
			if buf != nil {
				e := rec.Entry()
				buf.Store(&e)
			}

			trw := &traceResponseWriter{
				ResponseWriter: w,
				tee:            new(bytes.Buffer),
				cap:            maxTraceBody,
			}
			next.ServeHTTP(trw, r)

			if ct := trw.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
				trw.streaming = true
			}

			respHeaders := make(http.Header)
			for k, vv := range trw.Header() {
				respHeaders[k] = vv
			}
			rec.SetResponse(respHeaders, trw.tee.Bytes(), trw.status, maxTraceBody, trw.streaming)
			rec.SetDuration(clk.Since(start))

			if buf != nil {
				e := rec.Entry()
				buf.Store(&e)
			}
		})
	}
}

// traceResponseWriter wraps http.ResponseWriter to tee response writes into an
// in-memory buffer (capped at cap bytes), capture the status code, and detect
// streaming responses (via Flush calls).
type traceResponseWriter struct {
	http.ResponseWriter
	tee       *bytes.Buffer
	cap       int64
	status    int
	streaming bool
}

func (w *traceResponseWriter) Write(b []byte) (int, error) {
	if !w.streaming {
		remaining := w.cap - int64(w.tee.Len())
		if remaining > 0 {
			n := int64(len(b))
			if n > remaining {
				n = remaining
			}
			w.tee.Write(b[:n])
		}
	}
	return w.ResponseWriter.Write(b)
}

func (w *traceResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *traceResponseWriter) Flush() {
	w.streaming = true
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *traceResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *traceResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

func (w *traceResponseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := w.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}
