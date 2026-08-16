package mcp

// subscriptions_test.go — the listen stream, and the promise that it costs the
// emulator nothing when nobody is on it.
//
// Two kinds of test here. The protocol ones check what the revision requires: an
// acknowledgement first, carrying the subscription id; only the notification
// types the client asked for; the id on everything delivered.
//
// The last one is different in kind and is the reason this file exists in the
// shape it does. These notifications are emitted from the emulator's own paths —
// a tool that mutates runtime state emits `resources/list_changed` on its way
// out — so an emitter with no audience is a cost the emulator pays, on every
// mutation, whether or not anyone is using MCP. It must be free.

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// The acknowledgement is the first thing on the stream, and carries the id
// everything else on it will be tagged with.
func TestSubscriptions_acknowledgesFirstWithItsID(t *testing.T) {
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 7, "method": subscriptionsListenMethod,
		"params": map[string]any{
			"_meta":         modernMeta(),
			"notifications": map[string]any{"toolsListChanged": true},
		},
	}, map[string]string{
		"MCP-Protocol-Version": ProtocolVersion,
		"Mcp-Method":           subscriptionsListenMethod,
		"Accept":               "text/event-stream",
	})
	defer resp.Body.Close() //nolint:errcheck

	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	first := string(buf[:n])
	if !strings.Contains(first, notificationsAcknowledged) {
		t.Fatalf("first message = %q, want %s", first, notificationsAcknowledged)
	}
	if !strings.Contains(first, metaSubscriptionID) {
		t.Errorf("acknowledgement carries no %s: %q", metaSubscriptionID, first)
	}
	// The id is the JSON-RPC id of the listen request.
	if !strings.Contains(first, `"7"`) {
		t.Errorf("acknowledgement does not carry the request id: %q", first)
	}
}

// "The server MUST NOT send notification types the client has not explicitly
// requested." A filter that asked for tools must not receive prompts.
func TestSubscriptionFilter_wantsOnlyWhatWasAskedFor(t *testing.T) {
	filter := subscriptionFilter{ToolsListChanged: true}

	if !filter.wants("notifications/tools/list_changed", "") {
		t.Error("a filter that asked for tools/list_changed did not want it")
	}
	for _, unwanted := range []string{
		"notifications/prompts/list_changed",
		"notifications/resources/list_changed",
		"notifications/resources/updated",
	} {
		if filter.wants(unwanted, "") {
			t.Errorf("a tools-only filter wanted %s", unwanted)
		}
	}
}

// Resource updates are subscribed per URI, not per type.
func TestSubscriptionFilter_matchesResourcesByURI(t *testing.T) {
	filter := subscriptionFilter{ResourceSubscriptions: []string{"file:///a"}}

	if !filter.wants("notifications/resources/updated", "file:///a") {
		t.Error("a subscribed URI was not wanted")
	}
	if filter.wants("notifications/resources/updated", "file:///b") {
		t.Error("an unsubscribed URI was wanted")
	}
}

// Request-scoped notifications "flow only on the response stream of the request
// they relate to" and must never appear on a listen stream, whatever the filter
// says.
func TestSubscriptionFilter_neverCarriesRequestScopedNotifications(t *testing.T) {
	// Everything on, to prove the exclusion is by kind and not by omission.
	filter := subscriptionFilter{
		ToolsListChanged:      true,
		PromptsListChanged:    true,
		ResourcesListChanged:  true,
		ResourceSubscriptions: []string{"file:///a"},
	}
	for _, requestScoped := range []string{
		"notifications/progress",
		"notifications/message",
		"notifications/cancelled",
	} {
		if filter.wants(requestScoped, "file:///a") {
			t.Errorf("a listen stream wanted %s, which belongs to a request's own stream", requestScoped)
		}
	}
}

// Each stream sees its own id, and one subscription's tag does not leak into
// another's copy of the params.
func TestSubscriptions_tagsEachStreamWithItsOwnID(t *testing.T) {
	params := map[string]any{"uri": "file:///a"}

	first := withSubscriptionID(params, "1")
	second := withSubscriptionID(params, "2")

	firstMeta := first["_meta"].(map[string]any)
	secondMeta := second["_meta"].(map[string]any)
	if firstMeta[metaSubscriptionID] != "1" || secondMeta[metaSubscriptionID] != "2" {
		t.Errorf("ids crossed: %v and %v", firstMeta, secondMeta)
	}
	// The caller's map is untouched, so an emitter can reuse it.
	if _, tagged := params["_meta"]; tagged {
		t.Error("withSubscriptionID mutated the params it was given")
	}
}

// The performance promise, and the reason it is a test rather than a comment.
//
// With no subscriptions open, an emit must not serialise, allocate a payload or
// touch anything but a length check. This is asserted through the public
// emitter because that is what the emulator calls, and measured in allocations
// because that is the cost that would otherwise be invisible.
func TestSubscriptions_emitCostsNothingWithNoListeners(t *testing.T) {
	s := NewServer(nil)

	allocs := testing.AllocsPerRun(100, func() {
		s.emitToSubscriptions("notifications/resources/list_changed", map[string]any{}, "")
	})
	if allocs > 0 {
		t.Errorf("emitting to nobody allocated %.0f times per call — the emulator pays "+
			"this on every runtime mutation whether or not MCP is in use", allocs)
	}
}

// And with a listener that wants something else, it still does not serialise.
func TestSubscriptions_emitCostsNothingWhenNoFilterMatches(t *testing.T) {
	s := NewServer(nil)
	_, remove := s.addSubscription("1", subscriptionFilter{ToolsListChanged: true})
	defer remove()

	allocs := testing.AllocsPerRun(100, func() {
		s.emitToSubscriptions("notifications/resources/list_changed", map[string]any{}, "")
	})
	if allocs > 0 {
		t.Errorf("emitting a notification nobody's filter wanted allocated %.0f times per call", allocs)
	}
}

// The same promise one level up, at the emitter the emulator actually holds.
//
// Providers are handed emitResourceUpdated through SetResourceUpdatedEmitter and
// call it whenever a resource changes, so this runs on emulator paths rather
// than MCP ones. matchingSubscriptions being free is not enough on its own: if
// the params map is built before the audience is checked, the emulator pays for
// a notification body on every update that nobody asked for.
func TestSubscriptions_resourceUpdatedCostsNothingWithNoAudience(t *testing.T) {
	s := NewServer(nil)
	// Someone is listening, but for a different resource — the case that would
	// otherwise look like an audience to a check that only counted streams.
	_, remove := s.addSubscription("1", subscriptionFilter{ResourceSubscriptions: []string{"file:///other"}})
	defer remove()

	allocs := testing.AllocsPerRun(100, func() {
		s.emitResourceUpdated("file:///workspace/README.md")
	})
	if allocs > 0 {
		t.Errorf("emitting a resource update nobody subscribed to allocated %.0f times per call — "+
			"the emulator pays this on every resource change whether or not MCP is in use", allocs)
	}
}

// Shutdown ends the stream, and says so before it goes.
//
// The spec asks for the empty result that marks a graceful end, so a client can
// tell an orderly close from a dropped connection and decide whether to
// reconnect. serveSubscription has done this since the listen stream landed and
// nothing has ever checked it — the equivalent property was only ever asserted
// on the GET stream, by TestRuntimeMCPRoutes_SSEStreamLetsGoOnPreShutdown, which
// goes with that stream.
//
// This is deliberately a *positive* assertion. The GET-stream version asserts an
// absence — that a read does not block — which can only fail by waiting, and a
// test that fails by waiting is one whose failures are slow, load-sensitive and
// uninformative. The closing result is an observable the server either sends or
// does not, so this fails immediately and says which.
func TestSubscriptions_shutdownEndsTheStreamWithAClosingResult(t *testing.T) {
	shutdown := make(chan struct{})
	s := NewServer(nil)
	s.SetShutdownSignal(shutdown)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	l := listen(t, srv, map[string]any{"toolsListChanged": true})

	close(shutdown)

	// The closing message is a JSON-RPC *result*, not a notification: it answers
	// the listen request itself, which is what makes it an orderly end.
	closing := l.next()
	result, ok := closing["result"].(map[string]any)
	if !ok {
		t.Fatalf("closing message carries no result: %v", closing)
	}
	if result["resultType"] != resultTypeComplete {
		t.Errorf("resultType = %v, want %q", result["resultType"], resultTypeComplete)
	}
	meta, _ := result["_meta"].(map[string]any)
	if meta[metaSubscriptionID] != l.id {
		t.Errorf("closing result carries subscription id %v, want %q", meta[metaSubscriptionID], l.id)
	}

	// And then the stream really is over. The handler returning completes the
	// response, so the reader sees the body end rather than waiting on a
	// connection nobody is going to write to again.
	if _, stillOpen := <-l.msgs; stillOpen {
		t.Error("the stream carried something after its closing result")
	}
}

// A client that has stopped reading loses notifications rather than blocking
// the emulator goroutine that produced them.
func TestSubscriptions_slowClientIsDroppedNotWaitedFor(t *testing.T) {
	s := NewServer(nil)
	sub, remove := s.addSubscription("1", subscriptionFilter{ToolsListChanged: true})
	defer remove()

	// Fill the queue and then some. If sends blocked, this would deadlock.
	for i := 0; i < subscriptionQueueDepth*3; i++ {
		s.emitToSubscriptions("notifications/tools/list_changed", map[string]any{}, "")
	}
	if len(sub.ch) != subscriptionQueueDepth {
		t.Errorf("queue depth = %d, want it capped at %d", len(sub.ch), subscriptionQueueDepth)
	}

	// What did arrive is still well-formed — dropping must not corrupt.
	var decoded map[string]any
	if err := json.Unmarshal(<-sub.ch, &decoded); err != nil {
		t.Fatalf("queued notification does not parse: %v", err)
	}
	if decoded["method"] != "notifications/tools/list_changed" {
		t.Errorf("method = %v", decoded["method"])
	}
}
