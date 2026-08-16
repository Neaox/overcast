package mcp

// notification_delivery_test.go — the notifications overcast emits, observed
// through the transport that survives.
//
// # Why this file exists
//
// Around two dozen tests in server_test.go are named `..._OnSSE` and read like
// tests of the GET stream. They are not. Each asserts a real behaviour — that a
// runtime mutation emits `resources/list_changed`, that a provider registering
// emits `tools/list_changed`, that an update reaches only the clients that asked
// for that URI — and merely *observes* it through the GET stream, because until
// `subscriptions/listen` existed that was the only way to watch a notification
// leave the server.
//
// Revision 2026-07-28 deletes the GET stream. Deleting those tests along with it
// would drop the coverage of `emitNotification` and all seven `emit*` helpers
// without a single failing build to say so. So the subject moves to a transport
// that is not going away, and it moves *first*: everything here is added while
// the GET stream still works, so the deletion that follows is provably a change
// of observer and not a loss of coverage.
//
// # The ack is a barrier, not a formality
//
// `subscriptions/listen` must acknowledge before it sends anything, and the
// acknowledgement is written after the subscription is registered. Reading it is
// therefore proof that the server will see every subsequent emission — which is
// what lets the assertions below be about delivery rather than about timing.
// The GET stream offered no such point; its tests connected and hoped.
//
// # Negative assertions do not use a timeout
//
// "No notification arrived" is the easiest assertion to write badly: sleep, see
// nothing, declare victory — and pass just as happily when the notification was
// merely slow. Every negative here instead emits a sentinel *after* the thing
// that must not be delivered, and reads until the sentinel arrives. One
// subscription is one channel, so delivery on it is FIFO: anything the server
// emitted before the sentinel is already queued ahead of it. Seeing the sentinel
// is proof that the unwanted notification was not emitted, rather than evidence
// that it had not been emitted yet.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// notificationWait bounds how long a test waits for a notification it expects.
//
// This is a backstop against a hung server, not a race window. By the time a
// test's trigger returns, the payload is already in the subscription's queue:
// emitters run synchronously on the caller's goroutine, and a tool call's
// emissions happen before its response is written. So a passing test is never
// close to this bound, and a test that reaches it is broken rather than slow.
const notificationWait = 2 * time.Second

// sentinelMethod marks "everything emitted before now has been delivered".
//
// Deliberately a broadcast type no negative assertion here is about, so using it
// as a marker never collides with what a test is measuring. listen() adds it to
// every filter so that quiesce() always has a way through.
const sentinelMethod = "notifications/prompts/list_changed"

// newNotificationServer builds a server whose shutdown is sequenced correctly
// against its listeners'.
//
// `httptest.Server.Close` waits for every connection to leave the *active*
// state, and a listen stream is active until its body is closed — so a test
// written the usual way, with `defer srv.Close()`, deadlocks: the deferred close
// runs before the `t.Cleanup` that would release the stream. Registering the
// server's shutdown as a cleanup puts both on the same LIFO stack, and since
// this runs before any listen() the server is torn down last.
func newNotificationServer(t *testing.T, providers ...ToolProvider) (*Server, *httptest.Server) {
	t.Helper()
	s := NewServer(nil, providers...)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv
}

// listener is one open `subscriptions/listen` stream, and the lens every test
// in this file looks through.
type listener struct {
	t    *testing.T
	resp *http.Response
	msgs chan map[string]any
	id   string
}

// listenSeq hands each stream a distinct JSON-RPC id. The subscription id *is*
// the request id, and two streams sharing one would be indistinguishable to a
// client reading them off a single channel — so a test that opens two must not
// be the thing that makes them collide.
var listenSeq atomic.Int64

func nextListenID() string {
	return "listen-" + strconv.FormatInt(listenSeq.Add(1), 10)
}

// listen opens a listen stream with the given filter and returns once the
// server has acknowledged it.
//
// The filter is the test's, plus sentinelMethod's type. Returning only after the
// acknowledgement is the whole point: see the file comment.
func listen(t *testing.T, srv *httptest.Server, filter map[string]any) *listener {
	t.Helper()

	wanted := map[string]any{"promptsListChanged": true}
	for k, v := range filter {
		wanted[k] = v
	}

	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": nextListenID(), "method": subscriptionsListenMethod,
		"params": map[string]any{
			"_meta":         modernMeta(),
			"notifications": wanted,
		},
	}, map[string]string{
		"MCP-Protocol-Version": ModernProtocolVersion,
		"Mcp-Method":           subscriptionsListenMethod,
		"Accept":               "text/event-stream",
	})
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close() //nolint:errcheck
		t.Fatalf("subscriptions/listen status = %d, want 200", resp.StatusCode)
	}

	l := &listener{t: t, resp: resp, msgs: make(chan map[string]any, 64)}
	go l.scan()

	ack := l.next()
	if ack["method"] != notificationsAcknowledged {
		t.Fatalf("first message = %v, want %s", ack["method"], notificationsAcknowledged)
	}
	l.id = subscriptionIDFrom(ack)
	if l.id == "" {
		t.Fatalf("acknowledgement carries no %s: %v", metaSubscriptionID, ack)
	}
	t.Cleanup(func() { resp.Body.Close() }) //nolint:errcheck
	return l
}

// scan turns the SSE body into decoded messages. Heartbeat comments and
// anything that will not parse are skipped; the stream ending closes the
// channel, so a reader waiting on a dead stream finds out rather than blocking.
func (l *listener) scan() {
	defer close(l.msgs)
	scanner := bufio.NewScanner(l.resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) < len("data: ") || string(line[:len("data: ")]) != "data: " {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line[len("data: "):], &msg); err != nil {
			continue
		}
		l.msgs <- msg
	}
}

// next returns the next message on the stream, failing if none arrives.
func (l *listener) next() map[string]any {
	l.t.Helper()
	select {
	case msg, ok := <-l.msgs:
		if !ok {
			l.t.Fatal("listen stream ended while waiting for a notification")
		}
		return msg
	case <-time.After(notificationWait):
		l.t.Fatalf("no notification within %s", notificationWait)
		return nil
	}
}

// await waits for a notification of the given method and returns its params.
//
// Anything else on the stream is skipped rather than failed on: a test asserting
// that a tool call emits `resources/list_changed` should not break because the
// server also, correctly, emitted something else.
func (l *listener) await(method string) map[string]any {
	l.t.Helper()
	deadline := time.After(notificationWait)
	for {
		select {
		case msg, ok := <-l.msgs:
			if !ok {
				l.t.Fatalf("listen stream ended before %s arrived", method)
			}
			if msg["method"] != method {
				continue
			}
			l.requireTagged(msg)
			params, _ := msg["params"].(map[string]any)
			return params
		case <-deadline:
			l.t.Fatalf("did not receive %s within %s", method, notificationWait)
			return nil
		}
	}
}

// quiesce emits the sentinel and returns every notification queued ahead of it.
//
// This is how a negative assertion is made exact. Delivery on one subscription
// is FIFO, so the sentinel arriving proves that everything the server had
// already emitted is in the returned slice — "nothing arrived" becomes a fact
// about what the server did, not about how long the test was willing to wait.
func (l *listener) quiesce(s *Server) []map[string]any {
	l.t.Helper()
	s.emitPromptsListChanged()

	var seen []map[string]any
	deadline := time.After(notificationWait)
	for {
		select {
		case msg, ok := <-l.msgs:
			if !ok {
				l.t.Fatal("listen stream ended before the sentinel arrived")
			}
			if msg["method"] == sentinelMethod {
				return seen
			}
			seen = append(seen, msg)
		case <-deadline:
			l.t.Fatalf("sentinel %s did not arrive within %s", sentinelMethod, notificationWait)
			return nil
		}
	}
}

// requireNone fails if any notification of the given method was delivered ahead
// of the sentinel.
func (l *listener) requireNone(s *Server, method string) {
	l.t.Helper()
	for _, msg := range l.quiesce(s) {
		if msg["method"] == method {
			l.t.Fatalf("received %s, which nothing had subscribed to: %v", method, msg)
		}
	}
}

// requireTagged checks the notification carries the subscription's id.
//
// Every notification on a listen stream must: on stdio all subscriptions share
// one channel, and the id is the only thing telling them apart. Asserting it
// here covers every notification every test in this file observes, rather than
// once in the one test that thought to look.
func (l *listener) requireTagged(msg map[string]any) {
	l.t.Helper()
	if got := subscriptionIDFrom(msg); got != l.id {
		l.t.Errorf("notification %v carries subscription id %q, want %q", msg["method"], got, l.id)
	}
}

// subscriptionIDFrom digs the subscription id out of a message's params._meta.
func subscriptionIDFrom(msg map[string]any) string {
	params, _ := msg["params"].(map[string]any)
	meta, _ := params["_meta"].(map[string]any)
	id, _ := meta[metaSubscriptionID].(string)
	return id
}

// --- list_changed, emitted directly -----------------------------------------

// Ports TestServer_EmitResourceListChanged_OnSSE.
func TestNotifications_ResourceListChangedReachesAListener(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"resourcesListChanged": true})

	s.emitResourceListChanged()

	l.await("notifications/resources/list_changed")
}

// Ports TestServer_EmitPromptsListChanged_OnSSE and
// TestServer_EmitPromptsListChanged_SendsNotificationOnSSE, which asserted the
// same thing twice.
func TestNotifications_PromptsListChangedReachesAListener(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"promptsListChanged": true})

	s.emitPromptsListChanged()

	l.await("notifications/prompts/list_changed")
}

// Ports TestServer_EmitToolsListChanged_OnSSE.
func TestNotifications_ToolsListChangedReachesAListener(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"toolsListChanged": true})

	s.emitToolsListChanged()

	l.await("notifications/tools/list_changed")
}

// --- list_changed, emitted by registering a provider ------------------------
//
// These three are also where the handshake stopped gating emission. None of them
// performs an `initialize`, because a 2026-07-28 client never does — so between
// them they are the successor to
// TestServer_RegisterProvider_DoesNotEmitListChangedBeforeLifecycleReady, whose
// subject (a readiness moment to wait for) no longer exists. See registerProvider.

// Ports TestServer_RegisterProvider_EmitsToolsListChanged_OnSSE.
//
// Registering a provider after the server is serving is what the emulator does
// when a subsystem comes up late, and the client's tool list is stale until it
// hears about it.
func TestNotifications_RegisteringAProviderEmitsToolsListChanged(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"toolsListChanged": true})

	s.registerProvider(&staticProvider{
		tools: []Tool{{Name: "late", Description: "late", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})

	l.await("notifications/tools/list_changed")
}

// Ports TestServer_RegisterProvider_EmitsResourcesListChanged_OnSSE.
func TestNotifications_RegisteringAResourceProviderEmitsResourcesListChanged(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"resourcesListChanged": true})

	s.registerProvider(&staticResourceProvider{})

	l.await("notifications/resources/list_changed")
}

// Ports TestServer_RegisterProvider_EmitsPromptsListChanged_OnSSE.
func TestNotifications_RegisteringAPromptProviderEmitsPromptsListChanged(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"promptsListChanged": true})

	s.registerProvider(&staticPromptProvider{})

	l.await("notifications/prompts/list_changed")
}

// --- list_changed, emitted by a tool that mutates runtime state -------------

// Ports TestServer_RuntimeMutationTool_EmitsResourcesListChangedOnSSE.
//
// This is the path the performance rules in subscriptions.go are written for:
// the emission is on the emulator's own way out of a mutating tool call.
func TestNotifications_RuntimeMutationToolEmitsResourcesListChanged(t *testing.T) {
	_, srv := newNotificationServer(t, &mutableRuntimeResourceProvider{})
	l := listen(t, srv, map[string]any{"resourcesListChanged": true})

	callToolModern(t, srv, "runtime_mutate_demo")

	l.await("notifications/resources/list_changed")
}

// Ports TestServer_RuntimeNonDestructiveMutationTool_EmitsResourcesListChangedOnSSE.
//
// A tool that updates rather than creates or destroys still changes what a
// resource read would return, so it still has to say so.
func TestNotifications_NonDestructiveMutationToolEmitsResourcesListChanged(t *testing.T) {
	_, srv := newNotificationServer(t, &nonDestructiveRuntimeResourceProvider{})
	l := listen(t, srv, map[string]any{"resourcesListChanged": true})

	callToolModern(t, srv, "runtime_update_demo")

	l.await("notifications/resources/list_changed")
}

// A read-only tool changes nothing, so it must stay silent — the negative half
// of the two tests above, which the GET-stream suite never asserted.
func TestNotifications_ReadOnlyToolEmitsNoResourcesListChanged(t *testing.T) {
	s, srv := newNotificationServer(t, &staticProvider{
		tools: []Tool{{Name: "readonly", Description: "readonly", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "ok", nil
		},
	})
	l := listen(t, srv, map[string]any{"resourcesListChanged": true})

	callToolModern(t, srv, "readonly")

	l.requireNone(s, "notifications/resources/list_changed")
}

// --- resources/updated, which is subscribed per URI -------------------------

// Ports TestServer_ResourcesSubscribe_EmitsResourceUpdatedOnSSE.
//
// The subscription moves from a `resources/subscribe` call to the listen
// stream's own filter, which is where 2026-07-28 puts it.
func TestNotifications_ResourceUpdatedReachesAListenerSubscribedToTheURI(t *testing.T) {
	const uri = "file:///workspace/README.md"
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"resourceSubscriptions": []string{uri}})

	s.emitResourceUpdated(uri)

	params := l.await("notifications/resources/updated")
	if params["uri"] != uri {
		t.Errorf("uri = %v, want %s", params["uri"], uri)
	}
}

// Ports TestServer_ResourcesSubscribe_NoEmitWhenNotSubscribed.
func TestNotifications_ResourceUpdatedIsNotSentForAnUnsubscribedURI(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"resourceSubscriptions": []string{"file:///subscribed"}})

	s.emitResourceUpdated("file:///something-else")

	l.requireNone(s, "notifications/resources/updated")
}

// Ports TestServer_ResourcesSubscribe_EmitResourceUpdatedTrimsURI: a URI emitted
// with surrounding whitespace still matches the subscription, and is delivered
// in its canonical form.
func TestNotifications_ResourceUpdatedTrimsTheEmittedURI(t *testing.T) {
	const uri = "file:///workspace/README.md"
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"resourceSubscriptions": []string{uri}})

	s.emitResourceUpdated("  " + uri + "  ")

	params := l.await("notifications/resources/updated")
	if params["uri"] != uri {
		t.Errorf("uri = %v, want the trimmed %s", params["uri"], uri)
	}
}

// An empty URI is not a resource, and must not reach anyone.
//
// The listener deliberately subscribes to the empty URI, which no real client
// would: it is the only filter that *would* match if the guard in
// emitResourceUpdated were removed, so it is what makes this test able to fail.
// Subscribing to anything else would pass whether the guard was there or not.
func TestNotifications_ResourceUpdatedIgnoresABlankURI(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"resourceSubscriptions": []string{""}})

	s.emitResourceUpdated("   ")

	l.requireNone(s, "notifications/resources/updated")
}

// --- one stream's subscription is its own -----------------------------------

// Ports TestServer_ResourcesSubscribe_UnsubscribeStopsResourceUpdatedOnSSE and
// TestServer_ResourcesSubscribe_ReferenceCountRequiresMatchingUnsubscribes.
//
// Those two tested a server-side refcount: `resources/subscribe` twice needed
// `resources/unsubscribe` twice before updates stopped. 2026-07-28 has no such
// bookkeeping to get wrong, because a subscription is not a server-side set any
// more — it is a property of one stream, and it ends when that stream does. The
// behaviour worth carrying forward is what the refcount existed to protect:
// two clients watching the same resource are independent, and one leaving does
// not silence the other.
func TestNotifications_ClosingOneListenerDoesNotSilenceAnother(t *testing.T) {
	const uri = "file:///workspace/README.md"
	s, srv := newNotificationServer(t)

	staying := listen(t, srv, map[string]any{"resourceSubscriptions": []string{uri}})
	leaving := listen(t, srv, map[string]any{"resourceSubscriptions": []string{uri}})

	// Both are watching, and each hears about it under its own subscription id.
	s.emitResourceUpdated(uri)
	staying.await("notifications/resources/updated")
	leaving.await("notifications/resources/updated")
	if staying.id == leaving.id {
		t.Fatalf("two streams share subscription id %q", staying.id)
	}

	leaving.resp.Body.Close() //nolint:errcheck
	waitForSubscriptions(t, s, 1)

	// The one that stayed still hears about it.
	s.emitResourceUpdated(uri)
	staying.await("notifications/resources/updated")
}

// A listener that asked for one type does not receive another, whatever else
// the server is emitting.
func TestNotifications_AListenerReceivesOnlyTheTypesItAskedFor(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"toolsListChanged": true})

	s.emitResourceListChanged()

	l.requireNone(s, "notifications/resources/list_changed")
}

// --- helpers ----------------------------------------------------------------

// callToolModern makes a 2026-07-28 tools/call and fails on anything but a
// clean result, so a test asserting on a notification is never really failing
// because the call that should have produced it did not run.
func callToolModern(t *testing.T, srv *httptest.Server, name string) {
	t.Helper()
	resp := mcpPost(t, srv, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"_meta":     modernMeta(),
			"name":      name,
			"arguments": map[string]any{},
		},
	}, map[string]string{
		"MCP-Protocol-Version": ModernProtocolVersion,
		"Mcp-Method":           "tools/call",
		"Mcp-Name":             name,
	})
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close() //nolint:errcheck
		t.Fatalf("tools/call %s status = %d, want 200", name, resp.StatusCode)
	}
	body := decodeBodyMap(t, resp)
	if body["error"] != nil {
		t.Fatalf("tools/call %s: %v", name, body["error"])
	}
}

// waitForSubscriptions blocks until the server holds exactly n listen streams.
//
// Closing a response body tells the server nothing directly — the handler
// notices when its request context is cancelled, which happens on the server's
// own schedule. Polling the set the emitter actually consults is what makes the
// next assertion a statement about delivery rather than about how long the test
// waited for a teardown.
func waitForSubscriptions(t *testing.T, s *Server, n int) {
	t.Helper()
	deadline := time.Now().Add(notificationWait)
	for {
		s.mu.RLock()
		got := len(s.subscriptions)
		s.mu.RUnlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server holds %d subscriptions, want %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// --- the emitter callbacks handed to providers -------------------------------
//
// registerProvider hands a provider three closures so it can announce its own
// changes. These ported from tests that watched the server-wide broadcast, which
// 2026-07-28 removes; what they assert is unchanged, and is two things at once —
// that the callback was wired at all, and that calling it reaches a client.

// Ports TestServer_RegisterProvider_WiresResourceListChangedEmitter.
func TestNotifications_ProviderCanAnnounceAResourceListChange(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"resourcesListChanged": true})

	provider := &emitterAwareProvider{}
	s.registerProvider(provider)
	if provider.emitter == nil {
		t.Fatal("resource list changed emitter was not wired")
	}

	provider.emitter()

	l.await("notifications/resources/list_changed")
}

// Ports TestServer_RegisterProvider_WiresResourceUpdatedEmitter.
//
// The subscription moves from a server-side set to the listen stream's filter,
// which is the only place a resource subscription lives now.
func TestNotifications_ProviderCanAnnounceAResourceUpdate(t *testing.T) {
	const uri = "oc://demo/item"
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"resourceSubscriptions": []string{uri}})

	provider := &emitterAwareProvider{}
	s.registerProvider(provider)
	if provider.updatedEmitter == nil {
		t.Fatal("resource updated emitter was not wired")
	}

	provider.updatedEmitter(uri)

	params := l.await("notifications/resources/updated")
	if params["uri"] != uri {
		t.Errorf("uri = %v, want %s", params["uri"], uri)
	}
}

// Ports TestServer_RegisterProvider_WiresPromptListChangedEmitter.
func TestNotifications_ProviderCanAnnounceAPromptListChange(t *testing.T) {
	s, srv := newNotificationServer(t)
	l := listen(t, srv, map[string]any{"promptsListChanged": true})

	provider := &emitterAwareProvider{}
	s.registerProvider(provider)
	if provider.promptEmitter == nil {
		t.Fatal("prompt list changed emitter was not wired")
	}

	provider.promptEmitter()

	l.await("notifications/prompts/list_changed")
}
