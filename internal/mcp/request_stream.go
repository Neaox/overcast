package mcp

// request_stream.go — the response to one request, written as it happens.
//
// # What this is for
//
// 2026-07-28 divides notifications by who they are about. The broadcast ones —
// `tools/list_changed` and its siblings — go to whoever opened a
// `subscriptions/listen` stream and asked for them (subscriptions.go). The rest
// are about a single request:
//
//	"Request-scoped notifications flow only on the response stream of the
//	 request they relate to."
//
// Progress, log messages and cancellation are that kind. A listen stream must
// not carry them, and subscriptionFilter.wants refuses them by kind rather than
// by omission, so there is no filter a client could set that would deliver one.
// Their only correct destination is the response to the request that produced
// them — which is what this file is.
//
// # Why the headers are written late
//
// A streamed response commits to 200 the moment its headers go out, and some
// rejections must still be an HTTP status: an unsupported protocol version or a
// header that disagrees with the body is refused before a handler runs, and
// answering that with 200 and an SSE event would bury it. So nothing is written
// until there is something to write. The first notification starts the stream;
// if none is ever emitted, the result or the error decides what the response
// turns out to be.
//
// This also gives a cancelled request the right shape. Such a request has no
// result — handleToolsCall answers it with no body at all — so its stream
// carries the cancellation and then ends, which is exactly what a client waiting
// on it should see.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// requestStream is one request's SSE response.
//
// Writes are serialised because two goroutines legitimately reach it: the
// handler's own, emitting progress as it works, and whichever goroutine is
// serving the `notifications/cancelled` that cancels it. That second one is
// also why the stream is held on inFlightRequest rather than only in a context:
// cancellation is asked for by a *different* request, so the goroutine emitting
// the notification has nothing but the cancelled request's id to find it by.
type requestStream struct {
	mu  sync.Mutex
	out messageWriter

	// http is set only when this stream is an HTTP response, and carries the
	// bits that only exist there: headers that have to be written before the
	// first message, and a status that can still change until they are. Nil over
	// stdio, where a message is a line and there is nothing to write first.
	http    http.ResponseWriter
	started bool

	// logLevel is the threshold this request named in `_meta`, empty when it
	// named none. Written once by the dispatcher before the request is
	// registered in flight, so no other goroutine can be holding this stream
	// yet; read from there on, under no lock, because nothing writes it again.
	logLevel string
}

// requestStreamKey carries the stream from the HTTP layer to the dispatcher.
// Unexported and of a private type, so nothing outside this package can plant
// one.
type requestStreamKey struct{}

func withRequestStream(ctx context.Context, rs *requestStream) context.Context {
	return context.WithValue(ctx, requestStreamKey{}, rs)
}

// requestStreamFrom returns the stream this request is being answered on, or
// nil if the client did not ask for one.
func requestStreamFrom(ctx context.Context) *requestStream {
	rs, _ := ctx.Value(requestStreamKey{}).(*requestStream)
	return rs
}

// begin writes the SSE headers, once. Callers hold rs.mu.
func (rs *requestStream) begin() {
	if rs.started {
		return
	}
	rs.started = true
	if rs.http == nil {
		return // stdio: a message is a line, and there is no header to write
	}
	rs.http.Header().Set("Content-Type", "text/event-stream")
	rs.http.Header().Set("Cache-Control", "no-cache")
	rs.http.Header().Set("Connection", "keep-alive")
	rs.http.Header().Set("X-Accel-Buffering", "no")
	rs.http.WriteHeader(http.StatusOK)
}

// notify writes one notification to the stream.
//
// nil receiver is the ordinary case, not an error: a request that did not ask
// for a stream has nowhere to put a notification, and dropping it is correct —
// there is no other channel a request-scoped notification may travel on.
func (rs *requestStream) notify(method string, params any) {
	if rs == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.begin()
	rs.out.writeMessage(payload)
}

// setLogLevel records the threshold this request named. A nil receiver is a
// request that asked for no stream, which has nothing to record it on.
func (rs *requestStream) setLogLevel(level string) {
	if rs == nil {
		return
	}
	rs.logLevel = level
}

// wantsLog reports whether this request asked to be told about something at
// this level.
//
// Silence is the default and the request is what breaks it: "servers MUST NOT
// emit notifications/message for requests that did not include this field". So
// an absent level is not "the usual threshold" — it is none, and a request that
// asked for nothing hears nothing.
//
// A nil receiver — a request that asked for no stream at all — answers the same
// way, because it has nowhere to put a notification either. Both answers are
// given here rather than inside notify so that a caller assembling a message
// body can be told not to bother first. See emitLogMessage.
func (rs *requestStream) wantsLog(level string) bool {
	if rs == nil || rs.logLevel == "" {
		return false
	}
	return loggingLevelRank(level) <= loggingLevelRank(rs.logLevel)
}

// hasStarted reports whether anything has gone out yet, which is what decides
// whether the response can still become a plain HTTP status.
func (rs *requestStream) hasStarted() bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.started
}

// finish writes the request's answer as the last event on the stream.
//
// An empty body is a request with no answer — a cancelled call — and ends the
// stream without one rather than inventing a result to end it with.
func (rs *requestStream) finish(body string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.begin()
	if body == "" {
		// A request with no answer — a cancelled call. Over HTTP the stream ends
		// with a comment so the client sees a well-formed close; over stdio
		// there is simply nothing more to say about this id.
		if sse, overHTTP := rs.out.(sseWriter); overHTTP {
			_, _ = fmt.Fprint(sse.w, ": no response\n\n")
			sse.flusher.Flush()
		}
		return
	}
	rs.out.writeMessage([]byte(body))
}
