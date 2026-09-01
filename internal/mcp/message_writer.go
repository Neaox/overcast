package mcp

// message_writer.go — how a message reaches a client, on either transport.
//
// # Why this exists
//
// Until 2026-07-28 the stdio loop had its own answer to notifications: it
// subscribed to a server-wide broadcast and copied everything onto stdout. That
// broadcast is gone, because the revision has no server-wide notifications left
// to make — every notification now belongs either to a `subscriptions/listen`
// stream or to the response of one request.
//
// Both of those are *streams*, and stdio had no way to serve one. Its dispatcher
// synthesises an http.Request and pushes it through the same handler as HTTP
// (see ServeStdio), against a captureResponseWriter that buffers the whole
// response and is deliberately not an http.Flusher. A long-lived stream written
// to that would emit nothing until it ended, which for a listen stream means
// never.
//
// So the streams write through this instead of writing to an http.ResponseWriter
// directly. The framing is the only thing that differs — SSE events over HTTP,
// newline-delimited JSON over stdio — and it is the transport's business, not
// the protocol's.
//
// Without this, deleting the broadcast would have silently left a stdio client
// with no notifications at all. That is the transport `go run
// ./cmd/overcast-mcp --stdio` uses.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// messageWriter delivers one JSON-RPC message to a client.
//
// Implementations are responsible for framing and for getting the bytes out
// rather than buffering them: a stream that does not reach the client until it
// closes is not a stream.
type messageWriter interface {
	writeMessage(payload []byte)
}

// sseWriter frames messages as SSE events over HTTP.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s sseWriter) writeMessage(payload []byte) {
	_, _ = fmt.Fprintf(s.w, "data: %s\n\n", payload)
	s.flusher.Flush()
}

// ndjsonWriter frames messages as newline-delimited JSON over stdio.
//
// The mutex is the stdio loop's own: responses and notifications share one
// descriptor, and two writers interleaving mid-line would corrupt both.
type ndjsonWriter struct {
	out io.Writer
	mu  *sync.Mutex
}

func (n ndjsonWriter) writeMessage(payload []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, _ = fmt.Fprintf(n.out, "%s\n", payload)
}

// stdioWriterKey carries the stdio loop's writer to the handlers.
//
// Unexported and of a private type, for the same reason the stdio transport
// marker is: nothing outside this package can plant one, so no HTTP caller can
// redirect another client's stream.
type stdioWriterKey struct{}

func withStdioWriter(ctx context.Context, w messageWriter) context.Context {
	return context.WithValue(ctx, stdioWriterKey{}, w)
}

// stdioWriterFrom returns the stdio loop's writer, or nil over HTTP.
func stdioWriterFrom(ctx context.Context) messageWriter {
	w, _ := ctx.Value(stdioWriterKey{}).(messageWriter)
	return w
}
