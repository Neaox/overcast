package mcp

// sse_heartbeat_test.go — the keep-alive on the MCP SSE stream, and the two
// properties that keep it additive.
//
// A stream with no traffic on it is indistinguishable from a stream whose peer
// has gone, in both directions: the client cannot tell a quiet server from a
// dead one, and the server never attempts a write, so it never learns the
// client is gone either. The emulator's other two SSE endpoints have always
// sent a periodic comment for that reason; this is the MCP stream catching up.
//
// The tests below are mostly about what the heartbeat must NOT do. It rides on
// a transport whose payloads are JSON-RPC messages, so anything a client's
// event parser would surface as a message is a protocol change rather than a
// keep-alive — see the sseHeartbeatInterval doc comment for why a comment line
// is the only shape available, and for the two shapes that were rejected.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
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

// openSSE opens the legacy SSE stream and returns a reader over it.
//
// The request carries a deadline, and that is load-bearing rather than
// belt-and-braces: a bufio read on a silent stream blocks in the syscall, so a
// reader that polled a wall-clock deadline between reads would never get back
// round to checking it. Cancelling the request is what unblocks the read, which
// is what lets a test assert that something did *not* arrive without hanging
// the package the way an unbounded read does — the very failure mode
// internal/router/mcp_shutdown_test.go exists to prevent.
func openSSE(t *testing.T, url string, within time.Duration) (*http.Response, *bufio.Reader) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	return resp, bufio.NewReader(resp.Body)
}

// readLines reads until want is found or the stream ends, returning everything
// read. Termination is guaranteed by the deadline openSSE put on the request,
// not by anything here.
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
	withFastHeartbeat(t, 50*time.Millisecond)
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp, reader := openSSE(t, srv.URL+"/mcp/sse", 5*time.Second)
	defer resp.Body.Close() //nolint:errcheck

	seen := readLines(reader, ": heartbeat")
	for _, line := range seen {
		if strings.Contains(line, ": heartbeat") {
			return
		}
	}
	t.Fatalf("no heartbeat on an idle stream within 5s; saw %q — an idle stream that "+
		"says nothing is indistinguishable from a dead one, in both directions", seen)
}

// The heartbeat must be a comment. Anything a client's event parser dispatches
// as a message would be read as JSON-RPC on this transport, and a keep-alive is
// not a JSON-RPC message — so a `data:` line here would be a protocol
// violation, not a heartbeat.
func TestSSEHeartbeat_isACommentAndNotAMessage(t *testing.T) {
	withFastHeartbeat(t, 50*time.Millisecond)
	srv := newTestHTTPServer(t)
	defer srv.Close()

	resp, reader := openSSE(t, srv.URL+"/mcp/sse", 5*time.Second)
	defer resp.Body.Close() //nolint:errcheck

	seen := readLines(reader, ": heartbeat")

	// Assert one was seen before inspecting its shape. Without this the loop
	// below has nothing to iterate when no heartbeat is emitted at all, and the
	// test passes for the wrong reason.
	var found bool
	for _, line := range seen {
		if strings.Contains(line, "heartbeat") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no heartbeat to inspect; saw %q", seen)
	}

	for _, line := range seen {
		if !strings.Contains(line, "heartbeat") {
			continue
		}
		if !strings.HasPrefix(line, ":") {
			t.Errorf("heartbeat line %q is not an SSE comment — a client parses this as a "+
				"message and tries to read JSON-RPC out of it", line)
		}
		// An id would be consumed as the resumability cursor, so a resuming
		// client would ask to replay from a point no message was delivered at.
		if strings.HasPrefix(line, "id:") {
			t.Errorf("heartbeat line %q carries an event id — that is the Last-Event-ID cursor", line)
		}
		if strings.HasPrefix(line, "data:") || strings.HasPrefix(line, "event:") {
			t.Errorf("heartbeat line %q is a dispatched SSE field, not a comment", line)
		}
	}

	// Nothing on the stream so far may parse as JSON. The prelude and the
	// heartbeats are the only traffic, and both are comments.
	for _, line := range seen {
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		var msg map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(payload)), &msg) == nil {
			t.Errorf("idle stream delivered a JSON-RPC message %q — the heartbeat must add none", line)
		}
	}
}

// The heartbeat must not disturb real traffic: a notification emitted while the
// stream is beating still arrives, still carries its own id, and that id is the
// one a resuming client would use.
func TestSSEHeartbeat_doesNotDisturbRealMessages(t *testing.T) {
	withFastHeartbeat(t, 50*time.Millisecond)
	server, srv := newTestHTTPServerPair(t)
	defer srv.Close()

	resp, reader := openSSE(t, srv.URL+"/mcp/sse", 10*time.Second)
	defer resp.Body.Close() //nolint:errcheck

	// Let a heartbeat go by first, so the notification lands on a stream that
	// has already been beating rather than a fresh one.
	readLines(reader, ": heartbeat")
	server.emitNotification("notifications/message", map[string]any{"level": "info", "data": "after-heartbeat"})

	seen := readLines(reader, "after-heartbeat")
	var sawData, sawID bool
	for _, line := range seen {
		if strings.HasPrefix(line, "data:") && strings.Contains(line, "after-heartbeat") {
			sawData = true
		}
		if strings.HasPrefix(line, "id:") {
			sawID = true
		}
	}
	if !sawData {
		t.Fatalf("the notification never arrived on a beating stream; saw %q", seen)
	}
	if !sawID {
		t.Errorf("the notification arrived without an event id; saw %q — resumability depends on it", seen)
	}
}
