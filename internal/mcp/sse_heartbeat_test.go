package mcp

// sse_heartbeat_test.go — the keep-alive on the MCP stream, and the two
// properties that keep it additive.
//
// A stream with no traffic on it is indistinguishable from a stream whose peer
// has gone, in both directions: the client cannot tell a quiet server from a
// dead one, and the server never attempts a write, so it never learns the
// client is gone either. The emulator's other two SSE endpoints have always
// sent a periodic comment for that reason.
//
// The subject moved with the stream. These used to watch the GET endpoint, which
// revision 2026-07-28 removes; the keep-alive itself did not go with it, because
// `subscriptions/listen` is now the long-lived stream and the spec names exactly
// that kind when it recommends one. With `ping` also removed, this is the only
// liveness mechanism left.
//
// The tests below are mostly about what the heartbeat must NOT do. It rides on
// a transport whose payloads are JSON-RPC messages, so anything a client's event
// parser would surface as a message is a protocol change rather than a
// keep-alive — see the sseHeartbeatInterval doc comment for why a comment line
// is the only shape available, and for the two shapes that were rejected.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withFastHeartbeat winds the interval down for the duration of one test.
func withFastHeartbeat(t *testing.T, d time.Duration) {
	t.Helper()
	original := sseHeartbeatInterval
	sseHeartbeatInterval = d
	t.Cleanup(func() { sseHeartbeatInterval = original })
}

// openListenStream opens a `subscriptions/listen` stream and returns a reader
// over its raw lines.
//
// Raw rather than decoded, because these tests are about the framing: a
// heartbeat is a line the message-level harness in notification_delivery_test.go
// deliberately skips, so watching for it needs to see what actually crosses the
// wire.
//
// The request carries a deadline, and that is load-bearing rather than
// belt-and-braces: a bufio read on a silent stream blocks in the syscall, so a
// reader that polled a wall-clock deadline between reads would never get back
// round to checking it. Cancelling the request is what unblocks the read, which
// is what lets a test assert that something did *not* arrive without hanging the
// package the way an unbounded read does.
func openListenStream(t *testing.T, srv *httptest.Server, within time.Duration) (*http.Response, *bufio.Reader) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	t.Cleanup(cancel)

	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "heartbeat", "method": subscriptionsListenMethod,
		"params": map[string]any{
			"_meta":         modernMeta(),
			"notifications": map[string]any{"toolsListChanged": true},
		},
	})
	if err != nil {
		t.Fatalf("marshal listen request: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/mcp/", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ModernProtocolVersion)
	req.Header.Set("Mcp-Method", subscriptionsListenMethod)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", subscriptionsListenMethod, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	return resp, bufio.NewReader(resp.Body)
}

// readLines reads until want is found or the stream ends, returning everything
// read. Termination is guaranteed by the deadline openListenStream put on the
// request, not by anything here.
func readLines(r *bufio.Reader, want string) []string {
	var seen []string
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			seen = append(seen, strings.TrimRight(line, "\r\n"))
			if strings.Contains(line, want) {
				return seen
			}
		}
		if err != nil {
			return seen
		}
	}
}

// The stream must produce a keep-alive on its own when nothing else is
// happening. Without one an idle stream is silent indefinitely, which is what
// let a dead connection sit in the handler unnoticed.
func TestSSEHeartbeat_isSentOnAnIdleStream(t *testing.T) {
	withFastHeartbeat(t, 20*time.Millisecond)
	_, srv := newNotificationServer(t)

	resp, reader := openListenStream(t, srv, 2*time.Second)
	defer resp.Body.Close() //nolint:errcheck

	seen := readLines(reader, ": heartbeat")
	found := false
	for _, line := range seen {
		if strings.HasPrefix(line, ": heartbeat") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no heartbeat on an idle stream; saw %q", seen)
	}
}

// The heartbeat must be a comment. Anything a client's event parser dispatches
// as a message would be read as JSON-RPC on this transport, and a keep-alive is
// not a JSON-RPC message — so a `data:` line here would be a protocol
// violation, not a heartbeat.
func TestSSEHeartbeat_isACommentAndNotAMessage(t *testing.T) {
	withFastHeartbeat(t, 20*time.Millisecond)
	_, srv := newNotificationServer(t)

	resp, reader := openListenStream(t, srv, 2*time.Second)
	defer resp.Body.Close() //nolint:errcheck

	seen := readLines(reader, ": heartbeat")
	for _, line := range seen {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		// The acknowledgement is a real message and is expected; anything else
		// arriving on an idle stream came from the keep-alive.
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			t.Fatalf("unparseable message on an idle stream: %q", line)
		}
		if msg["method"] != notificationsAcknowledged {
			t.Fatalf("the heartbeat surfaced as a message: %q", line)
		}
	}
}

// The heartbeat must not disturb real traffic: a notification emitted while the
// stream is beating still arrives, and still arrives intact.
func TestSSEHeartbeat_doesNotDisturbRealMessages(t *testing.T) {
	withFastHeartbeat(t, 20*time.Millisecond)
	s, srv := newNotificationServer(t)

	resp, reader := openListenStream(t, srv, 3*time.Second)
	defer resp.Body.Close() //nolint:errcheck

	// Let the stream beat a few times before anything real happens.
	readLines(reader, ": heartbeat")
	s.emitToolsListChanged()

	seen := readLines(reader, "notifications/tools/list_changed")
	for _, line := range seen {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var msg map[string]any
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			t.Fatalf("heartbeat corrupted a message: %q", line)
		}
		if msg["method"] == "notifications/tools/list_changed" {
			return
		}
	}
	t.Fatalf("the notification never arrived on a beating stream; saw %q", seen)
}
