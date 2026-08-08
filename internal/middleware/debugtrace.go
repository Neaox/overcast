package middleware

import (
	"bufio"
	"bytes"
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

			// One bounded read of the request, shared: the Logger middleware
			// downstream picks this capture up off the context rather than
			// reading and holding the body a second time. The read has to be
			// eager here — service/operation detection and the early push both
			// need the body before the handler runs. See bodycapture.go.
			capture := readRequestBody(r, maxTraceBody)
			requestBody := capture.buf

			rec := trace.NewRecorder(reqID, start, r.Method, r.URL.Path, r.Host, r.URL.RawQuery, capture.headers)
			if pid := r.Header.Get("X-Overcast-Parent-Request-Id"); pid != "" {
				rec.SetParentRequestID(pid)
			}
			rec.SetRequestBody(requestBody, capture.truncated, capture.size())
			rec.SetMeta(r.RemoteAddr, r.UserAgent(), r.Header.Get("Referer"), "", "")
			if r.Header.Get("X-Overcast-Client") == "webui" {
				rec.AddMeta("source", "webui")
			}
			// Best-effort detection for the early push: set service/operation
			// when known, leave blank when the handler hasn't classified the
			// request yet. The Logger middleware sets the final values after
			// the handler runs.
			svc := detectService(r, requestBody)
			op := detectOperationForService(r, svc, requestBody)
			rec.SetServiceInfo(svc, op, RegionFromContext(r.Context(), cfg.Region))

			ctx := withRequestCapture(trace.ContextWithRecorder(r.Context(), rec), capture)
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
				// Default like Logger's wrapper: a handler that writes
				// without an explicit WriteHeader responds 200, and a trace
				// with StatusCode 0 reads as still in flight.
				status: http.StatusOK,
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
			if trw.truncated && !trw.streaming {
				rec.SetResponseBodyTruncated()
			}
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
	truncated bool
}

func (w *traceResponseWriter) Write(b []byte) (int, error) {
	if !w.streaming {
		remaining := w.cap - int64(w.tee.Len())
		if remaining > 0 {
			n := int64(len(b))
			if n > remaining {
				n = remaining
				w.truncated = true
			}
			w.tee.Write(b[:n])
		} else {
			w.truncated = true
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
