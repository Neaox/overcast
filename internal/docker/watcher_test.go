package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/events"
	"go.uber.org/zap/zaptest"
)

// inspectStateForID returns a State struct for the mock inspect endpoint. The
// container ID encodes which branch of inspectDieReason to hit:
//   - prefix "oom"   → OOMKilled=true
//   - prefix "err"   → Error set
//   - prefix "exit"  → non-zero exit code
//   - anything else  → clean exit (ExitCode 0)
func inspectStateForID(id string) map[string]any {
	switch {
	case strings.HasPrefix(id, "oom"):
		return map[string]any{"Status": "exited", "ExitCode": 137, "OOMKilled": true}
	case strings.HasPrefix(id, "err"):
		return map[string]any{"Status": "exited", "ExitCode": 128, "Error": "OCI runtime create failed"}
	case strings.HasPrefix(id, "exit"):
		return map[string]any{"Status": "exited", "ExitCode": 1}
	default:
		return map[string]any{"Status": "exited", "ExitCode": 0}
	}
}

// fakeEventsServer creates an httptest server that streams Docker-style
// newline-delimited JSON events. The caller pushes events into the returned
// channel; closing the channel ends the stream.
func fakeEventsServer(t *testing.T) (*httptest.Server, chan dockerEvent) {
	t.Helper()
	ch := make(chan dockerEvent, 16)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock container inspect for the inspect-on-die path. The test pushes
		// exit-code/error/oom hints via the container ID prefix so each die
		// event can exercise a different branch of inspectDieReason.
		if strings.HasPrefix(r.URL.Path, "/v1.45/containers/") && strings.HasSuffix(r.URL.Path, "/json") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.45/containers/"), "/json")
			body := map[string]any{
				"Id":    id,
				"Name":  "/test",
				"State": inspectStateForID(id),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(body)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v1.45/events") {
			http.NotFound(w, r)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		enc := json.NewEncoder(w)
		for ev := range ch {
			if err := enc.Encode(ev); err != nil {
				return
			}
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

// clientFromHTTPTest creates a Client that talks to the given httptest server
// instead of a Unix socket.
func clientFromHTTPTest(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return &Client{
		httpClient: srv.Client(),
		host:       srv.URL,
		logger:     zaptest.NewLogger(t),
	}
}

func TestWatcher_DispatchEvents(t *testing.T) {
	srv, ch := fakeEventsServer(t)
	client := clientFromHTTPTest(t, srv)
	bus := events.NewBus()
	defer bus.Stop()

	// Subscribe to all Docker events.
	var mu sync.Mutex
	var got []events.Event
	for _, typ := range []events.Type{
		events.DockerContainerStarted,
		events.DockerContainerDied,
		events.DockerContainerStopped,
		events.DockerContainerOOM,
		events.DockerContainerHealthStatus,
	} {
		bus.Subscribe(typ, func(ctx context.Context, e events.Event) {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := NewWatcher(client, bus, zaptest.NewLogger(t))
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Push events.
	ch <- dockerEvent{
		Type:   "container",
		Action: "start",
		Actor: struct {
			ID         string            `json:"ID"`
			Attributes map[string]string `json:"Attributes"`
		}{
			ID: "exit123def456",
			Attributes: map[string]string{
				"image":         "node:18",
				LabelManaged:    "true",
				LabelService:    "lambda",
				LabelResourceID: "my-function",
			},
		},
	}
	ch <- dockerEvent{
		Type:   "container",
		Action: "die",
		Actor: struct {
			ID         string            `json:"ID"`
			Attributes map[string]string `json:"Attributes"`
		}{
			ID: "exit123def456",
			Attributes: map[string]string{
				"exitCode":      "1",
				"image":         "node:18",
				LabelManaged:    "true",
				LabelService:    "lambda",
				LabelResourceID: "my-function",
			},
		},
	}
	ch <- dockerEvent{
		Type:   "container",
		Action: "oom",
		Actor: struct {
			ID         string            `json:"ID"`
			Attributes map[string]string `json:"Attributes"`
		}{
			ID: "oom999container",
			Attributes: map[string]string{
				LabelManaged:    "true",
				LabelService:    "ecs",
				LabelResourceID: "task-1",
			},
		},
	}

	// Close the channel to end the stream, which causes the watcher to
	// disconnect and attempt reconnect. We cancel the context before it
	// reconnects so the test can complete.
	close(ch)

	// Wait for the bus to process all events.
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for events; got %d, want 3", n)
		case <-time.After(50 * time.Millisecond):
		}
	}

	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()

	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}

	// Build a map by event type for order-independent assertions (the bus
	// uses a worker pool so delivery order is non-deterministic).
	byType := make(map[events.Type]events.Event, len(got))
	for _, e := range got {
		byType[e.Type] = e
	}

	// Verify: start
	startEvt, ok := byType[events.DockerContainerStarted]
	if !ok {
		t.Fatal("missing DockerContainerStarted event")
	}
	p0 := startEvt.Payload.(events.DockerContainerPayload)
	if p0.ContainerID != "exit123def456" {
		t.Errorf("start.ContainerID = %s, want exit123def456", p0.ContainerID)
	}
	if p0.Service != "lambda" {
		t.Errorf("start.Service = %s, want lambda", p0.Service)
	}
	if p0.ResourceID != "my-function" {
		t.Errorf("start.ResourceID = %s, want my-function", p0.ResourceID)
	}

	// Verify: die
	dieEvt, ok := byType[events.DockerContainerDied]
	if !ok {
		t.Fatal("missing DockerContainerDied event")
	}
	p1 := dieEvt.Payload.(events.DockerContainerPayload)
	if p1.ExitCode != "1" {
		t.Errorf("die.ExitCode = %s, want 1", p1.ExitCode)
	}
	if p1.Reason != "exit 1" {
		t.Errorf("die.Reason = %q, want %q", p1.Reason, "exit 1")
	}

	// Verify: oom
	oomEvt, ok := byType[events.DockerContainerOOM]
	if !ok {
		t.Fatal("missing DockerContainerOOM event")
	}
	p2 := oomEvt.Payload.(events.DockerContainerPayload)
	if p2.Service != "ecs" {
		t.Errorf("oom.Service = %s, want ecs", p2.Service)
	}
}

func TestWatcher_ContextCancellation(t *testing.T) {
	srv, _ := fakeEventsServer(t)
	client := clientFromHTTPTest(t, srv)
	bus := events.NewBus()
	defer bus.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	w := NewWatcher(client, bus, zaptest.NewLogger(t))

	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	// Cancel immediately — Run should return promptly without hanging.
	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("Watcher.Run did not exit after context cancellation")
	}
}
