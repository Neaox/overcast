package mcp

// request_stream_test.go — the notifications that belong to one request, on
// that request's own response.
//
// # Why these cannot go where the others went
//
// Phase 3b moved the broadcast notifications onto `subscriptions/listen`
// (notification_delivery_test.go). Three did not come along, and not by
// oversight: progress, cancellation and log messages are scoped to a single
// request. The revision says such notifications "flow only on the response
// stream of the request they relate to", and subscriptionFilter.wants already
// refuses them by kind — a listen stream must not carry them even if a client
// asks. So the listen stream is the wrong destination for them by construction,
// and their right one is the response to the request that produced them.
//
// That destination did not exist. A POST asking for `text/event-stream` was
// answered by buffering the whole response and writing it as a single event, so
// nothing could be interleaved ahead of the result. These tests are what make
// it exist, and they are also what lets phase 4 delete the GET stream without
// taking emitProgress, emitCancelled and emitLogMessage's coverage with it.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// errDeliberateToolFailure is what the failing-tool test's handler returns, so
// the assertion can match on the message rather than on a substring of prose.
var errDeliberateToolFailure = errors.New("tool failed on purpose")

// requestStreamReader reads one request's SSE response.
type requestStreamReader struct {
	t    *testing.T
	resp *http.Response
	msgs chan map[string]any
}

// openRequestStream sends a request asking for a streamed response and returns
// a reader over it.
//
// Unlike a listen stream this has no acknowledgement to synchronise on, and
// needs none: everything it will carry is produced by the request it is the
// response to, so nothing can be emitted before the stream exists.
func openRequestStream(t *testing.T, srv *httptest.Server, id any, method string, params map[string]any, extraHeaders map[string]string) *requestStreamReader {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	existing, _ := params["_meta"].(map[string]any)
	params["_meta"] = mergeMeta(metaBlock(), existing)

	headers := map[string]string{
		"MCP-Protocol-Version": ProtocolVersion,
		"Mcp-Method":           method,
		"Accept":               "text/event-stream",
	}
	for k, v := range extraHeaders {
		headers[k] = v
	}

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	}, headers)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close() //nolint:errcheck
		t.Fatalf("%s status = %d, want 200", method, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		resp.Body.Close() //nolint:errcheck
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	r := &requestStreamReader{t: t, resp: resp, msgs: make(chan map[string]any, 32)}
	go r.scan()
	t.Cleanup(func() { resp.Body.Close() }) //nolint:errcheck
	return r
}

func (r *requestStreamReader) scan() {
	defer close(r.msgs)
	scanner := bufio.NewScanner(r.resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &msg); err != nil {
			continue
		}
		r.msgs <- msg
	}
}

// collect reads the stream to its end and returns everything on it.
//
// The stream ends when the request is answered, which is what makes this exact
// rather than timed: the result is the last thing written, so a closed stream
// means the server has said everything it is going to say about this request.
// "No progress notification was sent" is then a fact about the whole response,
// not about a window someone chose.
func (r *requestStreamReader) collect() []map[string]any {
	r.t.Helper()
	var got []map[string]any
	deadline := time.After(notificationWait)
	for {
		select {
		case msg, ok := <-r.msgs:
			if !ok {
				return got
			}
			got = append(got, msg)
		case <-deadline:
			r.t.Fatalf("response stream did not end within %s; got %d messages so far", notificationWait, len(got))
			return nil
		}
	}
}

// await waits for one notification of the given method.
func (r *requestStreamReader) await(method string) map[string]any {
	r.t.Helper()
	deadline := time.After(notificationWait)
	for {
		select {
		case msg, ok := <-r.msgs:
			if !ok {
				r.t.Fatalf("response stream ended before %s arrived", method)
			}
			if msg["method"] == method {
				params, _ := msg["params"].(map[string]any)
				return params
			}
		case <-deadline:
			r.t.Fatalf("did not receive %s within %s", method, notificationWait)
			return nil
		}
	}
}

// methodsOf names what arrived, for assertions about the shape of a whole
// response rather than about one message in it.
func methodsOf(msgs []map[string]any) []string {
	names := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		if method, ok := msg["method"].(string); ok {
			names = append(names, method)
			continue
		}
		names = append(names, "<result>")
	}
	return names
}

// --- progress ---------------------------------------------------------------

// Ports TestServer_ToolsCall_EmitsProgressNotification.
//
// Both notifications must arrive ahead of the result, which is the property the
// GET stream could not express: there, progress and result travelled on two
// unrelated connections and nothing ordered them.
func TestRequestStream_ProgressArrivesOnTheCallsOwnStreamBeforeTheResult(t *testing.T) {
	_, srv := newNotificationServer(t, &staticProvider{
		tools: []Tool{{Name: "slow", Description: "slow", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "done", nil
		},
	})

	stream := openRequestStream(t, srv, 50, "tools/call", map[string]any{
		"_meta":     map[string]any{"progressToken": "prog-1"},
		"name":      "slow",
		"arguments": map[string]any{},
	}, map[string]string{"Mcp-Name": "slow"})

	msgs := stream.collect()

	var progress []float64
	for _, msg := range msgs {
		if msg["method"] != "notifications/progress" {
			continue
		}
		params, _ := msg["params"].(map[string]any)
		if params["progressToken"] != "prog-1" {
			t.Errorf("progressToken = %v, want prog-1", params["progressToken"])
		}
		value, _ := params["progress"].(float64)
		progress = append(progress, value)
	}
	if len(progress) != 2 || progress[0] != 0 || progress[1] != 1 {
		t.Fatalf("progress values = %v, want [0 1]; whole stream was %v", progress, methodsOf(msgs))
	}
	// The result is last: a client reading in order sees the work reported
	// before the answer, which is the point of streaming it at all.
	if names := methodsOf(msgs); names[len(names)-1] != "<result>" {
		t.Errorf("stream ended with %q, want the result last: %v", names[len(names)-1], names)
	}
}

// Ports TestServer_ToolsCall_NoProgressWhenNoToken.
//
// Exact rather than timed: the stream ending is the server saying it has
// finished with this request, so an absent notification is absent for good.
func TestRequestStream_NoProgressWithoutAToken(t *testing.T) {
	_, srv := newNotificationServer(t, &staticProvider{
		tools: []Tool{{Name: "noop", Description: "noop", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "ok", nil
		},
	})

	stream := openRequestStream(t, srv, 51, "tools/call", map[string]any{
		"name":      "noop",
		"arguments": map[string]any{},
	}, map[string]string{"Mcp-Name": "noop"})

	for _, msg := range stream.collect() {
		if msg["method"] == "notifications/progress" {
			t.Fatalf("a call with no progressToken reported progress: %v", msg)
		}
	}
}

// --- cancellation -----------------------------------------------------------

// Ports TestServer_Cancellation_EmitsCancelledNotificationOnSSE.
//
// The cancellation is requested by a *different* request, so this is the case
// that proves notifications are routed to the stream they belong to rather than
// to whichever stream happens to be open.
func TestRequestStream_CancellationIsReportedOnTheCancelledRequestsStream(t *testing.T) {
	started := make(chan struct{}, 1)
	_, srv := newNotificationServer(t, &staticProvider{
		tools: []Tool{{Name: "block", Description: "block", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	streamCh := make(chan *requestStreamReader, 1)
	go func() {
		streamCh <- openRequestStream(t, srv, 11, "tools/call", map[string]any{
			"name":      "block",
			"arguments": map[string]any{},
		}, map[string]string{"Mcp-Name": "block"})
	}()

	select {
	case <-started:
	case <-time.After(notificationWait):
		t.Fatal("tool handler never started")
	}

	cancel := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "method": "notifications/cancelled",
		"params": map[string]any{"requestId": 11, "reason": "user requested cancel"},
	}, nil)
	cancel.Body.Close() //nolint:errcheck

	stream := <-streamCh
	params := stream.await("notifications/cancelled")
	if params["requestId"] != float64(11) {
		t.Errorf("requestId = %v, want 11", params["requestId"])
	}
	if params["reason"] != "user requested cancel" {
		t.Errorf("reason = %v, want user requested cancel", params["reason"])
	}
}

// --- log messages -----------------------------------------------------------

// failingToolServer is a server whose one tool always fails, which is what
// produces a notifications/message now that `logging/setLevel` is gone.
func failingToolServer(t *testing.T) *httptest.Server {
	t.Helper()
	_, srv := newNotificationServer(t, &staticProvider{
		tools: []Tool{{Name: "boom", Description: "boom", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, errDeliberateToolFailure
		},
	})
	return srv
}

// Ports TestServer_LoggingSetLevel_EmitsMessageNotificationOnSSE and
// TestServer_LoggingNotification_IncludesLoggerField.
//
// Their subject was the `logging/setLevel` method, which 2026-07-28 removes —
// but what they actually asserted was that a notifications/message is emitted
// and carries level, logger and data. That survives the method's removal,
// because a failing tool still reports one, and it belongs to the call that
// failed. Asserting it there rather than through setLevel is what makes it a
// test of emitLogMessage instead of a test of a method that is going away.
//
// The threshold that used to be set by `logging/setLevel` is now stated by the
// request, in `_meta`, which is why this one says so.
func TestRequestStream_AFailingToolReportsAMessageOnItsOwnStream(t *testing.T) {
	srv := failingToolServer(t)

	stream := openRequestStream(t, srv, 60, "tools/call", map[string]any{
		"_meta":     map[string]any{metaLogLevel: "debug"},
		"name":      "boom",
		"arguments": map[string]any{},
	}, map[string]string{"Mcp-Name": "boom"})

	params := stream.await("notifications/message")
	if params["level"] != "error" {
		t.Errorf("level = %v, want error", params["level"])
	}
	if params["logger"] != "overcast" {
		t.Errorf("logger = %v, want overcast", params["logger"])
	}
	if data, _ := params["data"].(string); !strings.Contains(data, errDeliberateToolFailure.Error()) {
		t.Errorf("data = %v, want it to carry the tool's error", params["data"])
	}
}

// Silence is the default, and it is the request that breaks it.
//
// This closes the thread phase 1 opened. `parseRequestMeta` has read
// `io.modelcontextprotocol/logLevel` since then and nothing consumed it, while
// the filter still asked the connection-scoped level that `logging/setLevel`
// used to set — and phase 4 deletes that method, which would have left the
// threshold pinned at its "info" default forever. The revision does not want a
// default at all: a server "MUST NOT emit notifications/message for requests
// that did not include this field", so a request that asked for nothing is told
// nothing, however the call goes.
//
// Asserted with collect() rather than a timed absence: the stream ends when the
// request is answered, so "no message arrived" is a fact about the entire
// response and not about a window someone picked.
func TestRequestStream_ARequestThatNamedNoLogLevelIsToldNothing(t *testing.T) {
	srv := failingToolServer(t)

	stream := openRequestStream(t, srv, 61, "tools/call", map[string]any{
		"name":      "boom",
		"arguments": map[string]any{},
	}, map[string]string{"Mcp-Name": "boom"})

	msgs := stream.collect()
	for _, msg := range msgs {
		if msg["method"] == "notifications/message" {
			t.Fatalf("a request that named no %s was sent one anyway: %v", metaLogLevel, methodsOf(msgs))
		}
	}
}

// And a request that named a level hears only what reaches it.
//
// "emergency" is the most severe level there is, so the "error" a failing tool
// reports sits below it. Same failure, same stream, different request — which
// is the whole point of moving the threshold onto the request: two clients
// talking to one server no longer share a setting, and neither can change what
// the other hears.
func TestRequestStream_ALevelBelowWhatTheRequestAskedForIsNotEmitted(t *testing.T) {
	srv := failingToolServer(t)

	stream := openRequestStream(t, srv, 62, "tools/call", map[string]any{
		"_meta":     map[string]any{metaLogLevel: "emergency"},
		"name":      "boom",
		"arguments": map[string]any{},
	}, map[string]string{"Mcp-Name": "boom"})

	for _, msg := range stream.collect() {
		if msg["method"] == "notifications/message" {
			params, _ := msg["params"].(map[string]any)
			t.Fatalf("a request that asked for emergency was sent a %v", params["level"])
		}
	}
}

// The threshold is checked before the message is built, and that ordering is
// what this measures.
//
// Every streamed request carries a stream, and almost none of them name a log
// level — so the common case is an emit with no audience. Deciding that after
// assembling logMessageParams would put a notification body on the heap for a
// message nobody asked for, which is the same cost subscriptions.go refuses to
// pay on the broadcast side. See TestSubscriptions_emitCostsNothingWithNoListeners.
func TestRequestStream_LogEmitCostsNothingWhenTheRequestAskedForNoLogs(t *testing.T) {
	s := NewServer(nil)
	// A real stream, asked for by the request, that named no level — not a nil
	// one. The nil case is free for a different reason and would not catch a
	// gate that ran after the params were built.
	stream := &requestStream{out: ndjsonWriter{out: io.Discard, mu: &sync.Mutex{}}, started: true}
	var data any = errDeliberateToolFailure.Error()

	allocs := testing.AllocsPerRun(100, func() {
		s.emitLogMessage(stream, "error", data)
	})
	if allocs > 0 {
		t.Errorf("emitting a log message to a request that asked for none allocated %.0f times per call", allocs)
	}
}

// --- the stream is only opened when one was asked for -----------------------

// A request that did not ask for a stream still gets a plain JSON answer, and
// the notifications that would have gone on a stream are simply not sent.
// Nothing may be written to a response that is not a stream.
func TestRequestStream_APlainRequestStillGetsAPlainAnswer(t *testing.T) {
	_, srv := newNotificationServer(t, &staticProvider{
		tools: []Tool{{Name: "slow", Description: "slow", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "done", nil
		},
	})

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 70, "method": "tools/call",
		"params": map[string]any{
			"_meta":     mergeMeta(metaBlock(), map[string]any{"progressToken": "prog-2"}),
			"name":      "slow",
			"arguments": map[string]any{},
		},
	}, map[string]string{
		"MCP-Protocol-Version": ProtocolVersion,
		"Mcp-Method":           "tools/call",
		"Mcp-Name":             "slow",
	})
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("tools/call: %v", body["error"])
	}
	if body["result"] == nil {
		t.Fatal("no result on a non-streamed response")
	}
}

// A request refused before it reaches a handler is answered as an HTTP error,
// even though it asked for a stream.
//
// This is what the late headers buy. A stream commits to 200 the moment its
// headers go out, so writing them up front would turn every such rejection into
// a 200 with the refusal buried in an event — and `-32020` and `-32022` are
// exactly the errors an intermediary is meant to be able to see. Nothing has
// been written at the point these are decided, so the response is still free to
// be what it should be.
func TestRequestStream_ARejectedRequestIsStillAnHTTPError(t *testing.T) {
	_, srv := newNotificationServer(t)

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 80, "method": "tools/list",
		"params": map[string]any{
			"_meta": mergeMeta(metaBlock(), map[string]any{
				metaProtocolVersion: "1999-01-01",
			}),
		},
	}, map[string]string{
		"MCP-Protocol-Version": "1999-01-01",
		"Mcp-Method":           "tools/list",
		"Accept":               "text/event-stream",
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json — a refusal is not a stream", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error object in %v", body)
	}
	if errObj["code"] != float64(RPCUnsupportedProtocolVersion) {
		t.Errorf("error.code = %v, want %d", errObj["code"], RPCUnsupportedProtocolVersion)
	}
}

func mergeMeta(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}
