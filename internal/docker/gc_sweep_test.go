package docker

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// gc_sweep_test.go — the startup sweep's veto.
//
// The sweep removes containers left behind by a previous run, and decided that
// by container state alone: anything not running was litter. That is true for
// compute recreated on demand, and false for a database. A stopped RDS DB
// instance is a resource the user still owns and expects to start again, so
// sweeping its container stranded the instance pointing at an ID Docker no
// longer had — after which every start failed and every log fetch 404'd.

// sweepDaemon is a fake Docker daemon exposing a fixed container list and
// recording which containers were removed.
type sweepDaemon struct {
	srv *httptest.Server

	mu      sync.Mutex
	removed map[string]bool
}

func newSweepDaemon(t *testing.T, containers []ContainerSummary) *sweepDaemon {
	t.Helper()
	d := &sweepDaemon{removed: map[string]bool{}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_ = json.NewEncoder(w).Encode(containers)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/containers/"):
			parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
			d.mu.Lock()
			d.removed[parts[len(parts)-1]] = true
			d.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *sweepDaemon) wasRemoved(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.removed[id]
}

func managedContainer(id, resourceID, state string) ContainerSummary {
	return ContainerSummary{
		ID:    id,
		Names: []string{"/overcast-rds-" + resourceID},
		State: state,
		Labels: map[string]string{
			LabelService:    "rds",
			LabelResourceID: resourceID,
		},
	}
}

// Both error shapes this package produces must be recognised as 404. The
// direct-request helpers were invisible to IsNotFound, so callers could not
// tell "the container is gone" from "the call failed".
func TestIsNotFound_recognisesBothErrorShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"doJSON shape", errors.New("docker GET /v1.45/containers/abc/json: 404: {\"message\":\"No such container\"}"), true},
		{"direct-request shape", errors.New("start container abc: status 404"), true},
		{"logs shape with body", errors.New("container logs abc: status 404: {\"message\":\"No such container: abc\"}"), true},
		{"a conflict is not a not-found", errors.New("create container: 409: name in use"), false},
		{"a server error is not a not-found", errors.New("start container abc: status 500"), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNotFound(tc.err); got != tc.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// A container whose resource is still live must survive the sweep; one whose
// resource is gone is genuinely orphaned and must not.
func TestSweepExcept_keepsContainersOwnedByLiveResources(t *testing.T) {
	live := managedContainer("live-container", "my-stopped-db", "exited")
	orphan := managedContainer("orphan-container", "deleted-db", "exited")
	d := newSweepDaemon(t, []ContainerSummary{live, orphan})

	client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	gc := NewGC(client, zap.NewNop(), false)

	gc.SweepExcept("rds", func(resourceID string) bool {
		return resourceID == "my-stopped-db"
	})

	// The sweep runs in its own goroutine; give it a bounded window to finish.
	deadline := time.Now().Add(5 * time.Second)
	for !d.wasRemoved("orphan-container") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if !d.wasRemoved("orphan-container") {
		t.Error("orphaned container was not swept")
	}
	if d.wasRemoved("live-container") {
		t.Error("swept the container of a live resource — a stopped DB instance can no longer be started")
	}
}

// A running container is never touched, veto or no veto.
func TestSweepExcept_neverTouchesRunningContainers(t *testing.T) {
	running := managedContainer("running-container", "busy-db", "running")
	d := newSweepDaemon(t, []ContainerSummary{running})

	client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	gc := NewGC(client, zap.NewNop(), false)

	gc.SweepExcept("rds", func(string) bool { return false })

	time.Sleep(200 * time.Millisecond)
	if d.wasRemoved("running-container") {
		t.Error("swept a running container")
	}
}

// Sweep keeps its old meaning for callers with no attachment to their
// containers, so the services that recreate compute on demand are unaffected.
func TestSweep_withoutAVetoStillRemovesStoppedContainers(t *testing.T) {
	stopped := managedContainer("stopped-container", "task-1", "exited")
	d := newSweepDaemon(t, []ContainerSummary{stopped})

	client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	gc := NewGC(client, zap.NewNop(), false)

	gc.Sweep("ecs")

	deadline := time.Now().Add(5 * time.Second)
	for !d.wasRemoved("stopped-container") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !d.wasRemoved("stopped-container") {
		t.Error("Sweep without a veto should still remove a stopped container")
	}
}
