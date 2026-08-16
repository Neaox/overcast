package mcp

// subscriptions.go — `subscriptions/listen`, the stream that replaces the GET
// endpoint and `resources/subscribe`.
//
// A client opens one long-lived stream and says on it exactly which
// notifications it wants. The server "MUST NOT send notification types the
// client has not explicitly requested", and every notification on the stream
// carries the subscription's id so a client running several can tell them
// apart. The id is the JSON-RPC id of the listen request itself.
//
// # Performance: this must cost the emulator nothing when nobody is listening
//
// These notifications are emitted from the emulator's own paths — a tool that
// mutates runtime state emits `resources/list_changed` on the way out. So the
// cost of an emitter with no audience is paid by the emulator, not by MCP, and
// it is paid on every mutation whether or not an MCP client exists. Three rules
// follow, and all three are load-bearing rather than tidiness:
//
//  1. **Decide before serialising.** matchingSubscriptions runs under a read
//     lock over a map that is almost always empty, and returns before anything
//     is marshalled. An emulator with no MCP client attached does a map length
//     check and returns; it never builds a payload for nobody.
//  2. **Never hold the lock across a write.** The subscriber set is copied
//     under the lock and released before any send, so a slow reader cannot
//     stall an emitter that is holding a lock the emulator needs.
//  3. **Never block on a slow client.** Sends are non-blocking with a drop, the
//     same rule the emulator's own event bus follows. A client that cannot keep
//     up loses notifications; it does not get to slow down the emulator. That
//     is a deliberate trade — the alternative is an MCP client applying
//     backpressure to AWS API emulation, which would be absurd.
//
// The filter is matched before serialisation for the same reason: a client
// subscribed only to `tools/list_changed` costs nothing when a resource
// changes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	// subscriptionsListenMethod opens the stream.
	subscriptionsListenMethod = "subscriptions/listen"

	// notificationsAcknowledged is the first message on it. The server "MUST"
	// send this before any notification belonging to the subscription, so a
	// client knows the filter took effect and what the server agreed to honour.
	notificationsAcknowledged = "notifications/subscriptions/acknowledged"

	// metaSubscriptionID correlates a notification with the subscription that
	// asked for it. Required on every notification delivered on a listen
	// stream: on stdio all subscriptions share one channel, so this is the only
	// thing that tells them apart.
	metaSubscriptionID = "io.modelcontextprotocol/subscriptionId"

	// subscriptionQueueDepth is how far behind a client may fall before it
	// starts losing notifications. Deep enough to absorb a burst — a
	// provider registering many tools at once — and bounded so a client that
	// has stopped reading cannot grow memory without limit.
	subscriptionQueueDepth = 64
)

// subscriptionFilter is what a client asked for. Every field is optional, and
// omitting one means "not subscribed to that": the spec is explicit that
// "Omitting a field is equivalent to not subscribing to that notification
// type", which is why these are plain bools rather than pointers.
type subscriptionFilter struct {
	ToolsListChanged      bool     `json:"toolsListChanged,omitempty"`
	PromptsListChanged    bool     `json:"promptsListChanged,omitempty"`
	ResourcesListChanged  bool     `json:"resourcesListChanged,omitempty"`
	ResourceSubscriptions []string `json:"resourceSubscriptions,omitempty"`
}

// wants reports whether this filter asked for a given notification.
//
// uri is consulted only for `resources/updated`, where the client subscribes to
// named resources rather than to the type.
func (f subscriptionFilter) wants(method, uri string) bool {
	switch method {
	case "notifications/tools/list_changed":
		return f.ToolsListChanged
	case "notifications/prompts/list_changed":
		return f.PromptsListChanged
	case "notifications/resources/list_changed":
		return f.ResourcesListChanged
	case "notifications/resources/updated":
		for _, subscribed := range f.ResourceSubscriptions {
			if subscribed == uri {
				return true
			}
		}
		return false
	default:
		// Request-scoped notifications — progress, message — never belong here.
		// They "flow only on the response stream of the request they relate
		// to", so a listen stream must not carry them even if asked.
		return false
	}
}

// subscription is one open listen stream.
type subscription struct {
	id     string
	filter subscriptionFilter
	ch     chan []byte
}

// addSubscription registers a stream and returns it with a function to remove
// it again.
func (s *Server) addSubscription(id string, filter subscriptionFilter) (*subscription, func()) {
	sub := &subscription{id: id, filter: filter, ch: make(chan []byte, subscriptionQueueDepth)}
	s.mu.Lock()
	if s.subscriptions == nil {
		s.subscriptions = make(map[*subscription]struct{})
	}
	s.subscriptions[sub] = struct{}{}
	s.mu.Unlock()
	return sub, func() {
		s.mu.Lock()
		delete(s.subscriptions, sub)
		s.mu.Unlock()
	}
}

// matchingSubscriptions returns the streams that asked for this notification.
//
// The early return on an empty map is the property that keeps MCP off the
// emulator's bill: with no client attached this is a lock, a length check and a
// return, and the caller never serialises anything.
func (s *Server) matchingSubscriptions(method, uri string) []*subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.subscriptions) == 0 {
		return nil
	}
	var matched []*subscription
	for sub := range s.subscriptions {
		if sub.filter.wants(method, uri) {
			matched = append(matched, sub)
		}
	}
	return matched
}

// hasSubscriberFor reports whether any stream asked for this notification.
//
// The same question matchingSubscriptions answers, without building the slice —
// for callers that need to know whether there is an audience *before* they
// construct the notification's params. An empty subscription map makes this a
// lock and a loop that does not run.
func (s *Server) hasSubscriberFor(method, uri string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for sub := range s.subscriptions {
		if sub.filter.wants(method, uri) {
			return true
		}
	}
	return false
}

// emitToSubscriptions delivers one notification to every stream that asked for
// it, tagged with that stream's subscription id.
//
// uri is empty for everything except `resources/updated`.
func (s *Server) emitToSubscriptions(method string, params map[string]any, uri string) {
	matched := s.matchingSubscriptions(method, uri)
	if len(matched) == 0 {
		return // nothing marshalled, nothing copied
	}
	for _, sub := range matched {
		payload, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"method":  method,
			"params":  withSubscriptionID(params, sub.id),
		})
		if err != nil {
			continue
		}
		// Non-blocking: a client that has stopped reading loses this rather
		// than holding up whatever emulator path emitted it.
		select {
		case sub.ch <- payload:
		default:
		}
	}
}

// withSubscriptionID copies params and tags them, so two subscriptions do not
// share a map and see each other's id.
func withSubscriptionID(params map[string]any, id string) map[string]any {
	tagged := make(map[string]any, len(params)+1)
	for k, v := range params {
		tagged[k] = v
	}
	meta, _ := tagged["_meta"].(map[string]any)
	merged := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		merged[k] = v
	}
	merged[metaSubscriptionID] = id
	tagged["_meta"] = merged
	return tagged
}

// handleSubscriptionsListen opens the stream and serves it until the client
// goes away or the emulator shuts down.
func (s *Server) handleSubscriptionsListen(w http.ResponseWriter, req jsonRPCRequest, r *http.Request) {
	// Where this stream's messages go. Over stdio the loop supplies its own
	// writer, because a synthesised request has no real ResponseWriter to
	// stream through; over HTTP the response itself is the stream. See
	// message_writer.go.
	out := stdioWriterFrom(r.Context())
	if out == nil {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONRPCError(w, req.ID, &rpcError{Code: RPCInternalError, Message: "streaming unsupported"})
			return
		}
		out = sseWriter{w: w, flusher: flusher}
	}

	var params struct {
		Notifications subscriptionFilter `json:"notifications"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSONRPCError(w, req.ID, &rpcError{Code: RPCInvalidParams, Message: "invalid notifications filter"})
			return
		}
	}

	id := subscriptionIDOf(req.ID)
	sub, remove := s.addSubscription(id, params.Notifications)
	defer remove()

	if _, overHTTP := out.(sseWriter); overHTTP {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}

	// The acknowledgement comes first and carries the subscription id. The
	// filter echoed back is what the server agreed to honour — identical here,
	// since every type in the filter is one this server emits.
	ack, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  notificationsAcknowledged,
		"params": map[string]any{
			"notifications": params.Notifications,
			"_meta":         map[string]any{metaSubscriptionID: id},
		},
	})
	if err != nil {
		return
	}
	out.writeMessage(ack)

	s.serveSubscription(out, sub, req, r.Context())
}

// serveSubscription is the stream's lifetime.
func (s *Server) serveSubscription(out messageWriter, sub *subscription, req jsonRPCRequest, ctx context.Context) {
	shutdown := s.ShutdownSignal()
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-shutdown:
			// Going down. The spec asks for the empty result that marks a
			// graceful end, so a client can tell this from a dropped
			// connection and decide whether to reconnect.
			s.endSubscription(out, sub, req)
			return
		case <-ctx.Done():
			return
		case payload := <-sub.ch:
			out.writeMessage(payload)
		case <-heartbeat.C:
			// Keep-alive, and only where a keep-alive means anything. An SSE
			// comment tells an idle HTTP connection apart from a dead one — see
			// sseHeartbeatInterval, and note that with `ping` removed this is
			// the only liveness mechanism left. Stdio has neither the problem
			// nor the syntax: a pipe is alive until it closes, and anything
			// written there has to be a JSON-RPC message rather than a comment.
			// The revision gives no keep-alive story for stdio because it needs
			// none.
			if sse, overHTTP := out.(sseWriter); overHTTP {
				_, _ = fmt.Fprint(sse.w, ": heartbeat\n\n")
				sse.flusher.Flush()
			}
		}
	}
}

// endSubscription sends the empty result that marks a graceful close.
func (s *Server) endSubscription(out messageWriter, sub *subscription, req jsonRPCRequest) {
	final, err := json.Marshal(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"resultType": resultTypeComplete,
			"_meta":      map[string]any{metaSubscriptionID: sub.id},
		},
	})
	if err != nil {
		return
	}
	out.writeMessage(final)
}

// subscriptionIDOf renders a JSON-RPC id as the string used to correlate
// notifications with their subscription.
func subscriptionIDOf(id any) string {
	switch value := id.(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// peekMethod reads a request body to find its JSON-RPC method, and hands back a
// replacement body so the real decode still sees everything.
//
// Needed because the choice of response path has to be made before the body is
// parsed: a stream and a single answer are written differently, and only the
// method says which this is. Returns false if the body cannot be read or is not
// JSON, leaving the caller to take its ordinary path and report the parse error
// in its own words.
func peekMethod(r *http.Request) (io.ReadCloser, string, bool) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", false
	}
	replacement := io.NopCloser(bytes.NewReader(raw))
	var peek struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return replacement, "", false
	}
	return replacement, peek.Method, true
}
