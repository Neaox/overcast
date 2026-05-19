package middleware

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strconv"
)

// maxResponseBuffer is the largest response body we buffer in memory to set
// Content-Length. Responses exceeding this are sent with chunked encoding.
const maxResponseBuffer = 4 << 20 // 4 MiB

// DrainBody returns a middleware that:
//  1. Drains and closes the request body after the handler returns.
//  2. Buffers the response body so that a Content-Length header is always
//     set before the first byte reaches the client.
//
// Together these prevent the Go AWS SDK v2 warning:
//
//	"WARN failed to close HTTP response body, this may affect connection reuse"
//
// The warning fires on the CLIENT side when resp.Body.Close() fails. The root
// cause is a server response without Content-Length: the client relies on
// chunked transfer or connection close to detect the end of the body, and any
// framing issue makes Close() return an error. Setting Content-Length on every
// response eliminates the problem.
//
// If the handler calls http.Flusher.Flush() (streaming / SSE) or the buffered
// body exceeds maxResponseBuffer, the middleware switches to direct pass-through
// so large or streaming responses are never fully buffered.
func DrainBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain request body on exit so HTTP/1.1 connections are reusable
		// even when the handler doesn't consume the full body.
		defer func() {
			if r.Body != nil {
				io.Copy(io.Discard, r.Body) //nolint:errcheck
				r.Body.Close()              //nolint:errcheck
			}
		}()

		bw := &bufResponseWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		next.ServeHTTP(bw, r)
		bw.finish()
	})
}

// bufResponseWriter intercepts writes to buffer the response body so that
// Content-Length can be set before headers are flushed to the network.
type bufResponseWriter struct {
	http.ResponseWriter
	buf      bytes.Buffer
	status   int
	direct   bool // true once headers have been flushed (streaming mode)
	hijacked bool // true after Hijack() — skip finish()
}

// WriteHeader records the status code without flushing it.
// The special case for 101 (Switching Protocols) is required for WebSocket
// upgrades: the handshake response must reach the client before the
// connection is hijacked, so we flush immediately.
func (w *bufResponseWriter) WriteHeader(status int) {
	if w.direct {
		return // already flushed
	}
	if status == http.StatusSwitchingProtocols {
		w.direct = true
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.status = status
}

// Write buffers data until the buffer exceeds maxResponseBuffer, at which
// point it flushes headers and switches to direct writes.
func (w *bufResponseWriter) Write(b []byte) (int, error) {
	if w.direct {
		return w.ResponseWriter.Write(b)
	}
	if w.buf.Len()+len(b) > maxResponseBuffer {
		w.flushHeaders()
		return w.ResponseWriter.Write(b)
	}
	return w.buf.Write(b)
}

// Flush switches to direct mode and flushes the underlying writer.
// Required for streaming responses (SSE, Lambda invoke streaming).
func (w *bufResponseWriter) Flush() {
	w.flushHeaders()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets chi's middleware stack (and http.ResponseController) reach the
// underlying ResponseWriter for interface detection (Hijacker, Pusher, etc.).
func (w *bufResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Hijack implements http.Hijacker by delegating to the underlying writer.
// Required for WebSocket upgrades (e.g. AppSync real-time subscriptions).
func (w *bufResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		w.hijacked = true
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// flushHeaders sends buffered headers + body and switches to direct mode.
// Content-Length is intentionally NOT set here: this is called when the
// handler begins streaming (Flush) or the buffer overflows a large response,
// so the final body length is unknown. finish() handles Content-Length for
// fully-buffered responses.
func (w *bufResponseWriter) flushHeaders() {
	if w.direct {
		return
	}
	w.direct = true
	w.ResponseWriter.WriteHeader(w.status)
	if w.buf.Len() > 0 {
		w.ResponseWriter.Write(w.buf.Bytes()) //nolint:errcheck
		w.buf.Reset()
	}
}

// finish is called when the handler returns. If still buffering, it sets
// Content-Length (including 0 for bodyless responses) and flushes.
func (w *bufResponseWriter) finish() {
	if w.direct || w.hijacked {
		return
	}
	w.direct = true
	if w.Header().Get("Content-Length") == "" {
		w.Header().Set("Content-Length", strconv.Itoa(w.buf.Len()))
	}
	w.ResponseWriter.WriteHeader(w.status)
	if w.buf.Len() > 0 {
		w.ResponseWriter.Write(w.buf.Bytes()) //nolint:errcheck
	}
}
