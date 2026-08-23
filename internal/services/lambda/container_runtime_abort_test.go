package lambda

// container_runtime_abort_test.go — a cold start abandoned mid-create must not
// leave a container behind.
//
// Cancelling the acquire (DeleteFunction reaching a cold start in flight, or an
// invocation giving up) aborts the HTTP request to the daemon, but the daemon
// may already have created the container by then. The response carrying its ID
// never arrives, so every later cleanup path — which works from that ID — has
// nothing to work with. The container name is the only handle left, and it is
// ours: unique per acquire.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// abandoningDaemon is a fake Docker Engine whose container create never
// answers: it reports the name it was asked for, waits for the client to give
// up, and behaves as though the container was created anyway. Removals are
// reported on removed.
type abandoningDaemon struct {
	*httptest.Server

	creating chan string
	removed  chan string
	// stop releases the create handler at the end of the test, so a handler
	// still parked there cannot hold the server's Close open.
	stop chan struct{}
}

func newAbandoningDaemon(t *testing.T) *abandoningDaemon {
	t.Helper()
	d := &abandoningDaemon{
		creating: make(chan string, 1),
		removed:  make(chan string, 4),
		stop:     make(chan struct{}),
	}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/create":
			select {
			case d.creating <- r.URL.Query().Get("name"):
			default:
			}
			// The daemon completes the create; the client is no longer there
			// to be told about it.
			select {
			case <-r.Context().Done():
			case <-d.stop:
			}

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1.45/containers/"):
			select {
			case d.removed <- strings.TrimPrefix(r.URL.Path, "/v1.45/containers/"):
			default:
			}
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/images/create":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"Pull complete"}` + "\n"))

		// Every image is absent, so the acquire always pulls first.
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1.45/images/"):
			http.Error(w, "no such image", http.StatusNotFound)

		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(func() {
		close(d.stop)
		d.Close()
	})
	return d
}

// TestAcquireContainer_createAbandonedMidFlightRemovesTheContainer covers the
// far end of #1336's leak: the cold start is cancelled while the create is in
// flight, so it never learns the container's ID — and a container whose ID
// nothing holds is exactly the one that outlives its function.
func TestAcquireContainer_createAbandonedMidFlightRemovesTheContainer(t *testing.T) {
	// Given: a cold start that has reached the daemon's container create.
	daemon := newAbandoningDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)
	fn := imageFunction()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := cr.acquireContainer(ctx, fn, func(string) {}, initTypeOnDemand, false)
		done <- err
	}()

	var name string
	select {
	case name = <-daemon.creating:
	case <-time.After(10 * time.Second):
		t.Fatal("the acquire never reached the container create")
	}

	// When: the acquire is cancelled before the create answers.
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the abandoned acquire reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the acquire never returned after its context was cancelled")
	}

	// Then: the container the daemon went on to create is removed by the name
	// this acquire gave it — the only handle left once the response was lost.
	select {
	case removed := <-daemon.removed:
		if got, _, _ := strings.Cut(removed, "?"); got != name {
			t.Fatalf("removed container %q, want %q", got, name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoned container was never removed — it outlives the cold start")
	}
}
