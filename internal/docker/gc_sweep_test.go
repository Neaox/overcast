package docker

import (
	"context"
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

// gc_sweep_test.go — the startup sweep's veto, and the instance scope that
// makes the veto safe.
//
// The sweep removes containers left behind by a previous run, and decided that
// by container state alone: anything not running was litter. That is true for
// compute recreated on demand, and false for a database. A stopped RDS DB
// instance is a resource the user still owns and expects to start again, so
// sweeping its container stranded the instance pointing at an ID Docker no
// longer had — after which every start failed and every log fetch 404'd.
//
// The veto answers that from the sweeping instance's own records, which is
// only half an answer. Two Overcasts share a Docker daemon but keep separate
// stores, so each one's veto reports "nothing owns this" about every container
// the other is using. Every sweep here is therefore scoped by
// docker.LabelInstance first, and the tests below pin both halves: the veto
// still works, and it is no longer asked about containers that were never
// ours.

// thisInstance and otherInstance stand for two Overcasts sharing one daemon.
const (
	thisInstance  = "11111111-1111-1111-1111-111111111111"
	otherInstance = "22222222-2222-2222-2222-222222222222"
)

// fixedDomain is an InstanceDomainFunc that resolves to a known identity,
// standing in for serviceutil.InstanceDomain.Resolve.
func fixedDomain(id string) InstanceDomainFunc {
	return func(context.Context) string { return id }
}

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

// managedContainer is a container created by the instance under test.
func managedContainer(id, resourceID, state string) ContainerSummary {
	return ownedContainer(id, resourceID, state, thisInstance)
}

// ownedContainer is managedContainer with an explicit owner. Pass "" for a
// container that carries no identity at all, as one created before the label
// existed does.
func ownedContainer(id, resourceID, state, instance string) ContainerSummary {
	labels := map[string]string{
		LabelService:    "rds",
		LabelResourceID: resourceID,
	}
	if instance != "" {
		labels[LabelInstance] = instance
	}
	return ContainerSummary{
		ID:     id,
		Names:  []string{"/overcast-rds-" + resourceID},
		State:  state,
		Labels: labels,
	}
}

// waitForRemoval gives an async sweep a bounded window to remove id.
func waitForRemoval(d *sweepDaemon, id string) bool {
	deadline := time.Now().Add(5 * time.Second)
	for !d.wasRemoved(id) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	return d.wasRemoved(id)
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
	gc := NewGC(client, zap.NewNop(), false, fixedDomain(thisInstance))

	gc.SweepExcept("rds", func(resourceID string) bool {
		return resourceID == "my-stopped-db"
	})

	// The sweep runs in its own goroutine; give it a bounded window to finish.
	if !waitForRemoval(d, "orphan-container") {
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
	gc := NewGC(client, zap.NewNop(), false, fixedDomain(thisInstance))

	gc.SweepExcept("rds", func(string) bool { return false })

	time.Sleep(200 * time.Millisecond)
	if d.wasRemoved("running-container") {
		t.Error("swept a running container")
	}
}

// KeepContainers exists for post-mortem inspection, and the inspection usually
// happens after a restart — so the startup sweep has to honour it too. Every
// other removal path already did; this one removed the evidence the flag was
// set to preserve, the first time Overcast came back up.
func TestSweepExcept_keepContainersSurvivesRestart(t *testing.T) {
	stopped := managedContainer("post-mortem-container", "crashed-task", "exited")
	d := newSweepDaemon(t, []ContainerSummary{stopped})

	client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	gc := NewGC(client, zap.NewNop(), true, fixedDomain(thisInstance))

	gc.Sweep("ecs")

	time.Sleep(200 * time.Millisecond)
	if d.wasRemoved("post-mortem-container") {
		t.Error("startup sweep removed a container KeepContainers asked to keep")
	}
}

// Sweep keeps its old meaning for callers with no attachment to their
// containers, so the services that recreate compute on demand are unaffected.
func TestSweep_withoutAVetoStillRemovesStoppedContainers(t *testing.T) {
	stopped := managedContainer("stopped-container", "task-1", "exited")
	d := newSweepDaemon(t, []ContainerSummary{stopped})

	client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	gc := NewGC(client, zap.NewNop(), false, fixedDomain(thisInstance))

	gc.Sweep("ecs")

	if !waitForRemoval(d, "stopped-container") {
		t.Error("Sweep without a veto should still remove a stopped container")
	}
}

// ─── Instance scoping ─────────────────────────────────────────────────────────
//
// Each test below fails against the unscoped code, which is what makes it a
// regression rather than a description.

// The worst of the three, because it needs neither a crash nor a restart to
// fire and it does not spare running containers. One Overcast's ordinary
// shutdown listed every managed container for its service and stopped and
// force-removed the lot — including another Overcast's live RDS databases,
// ECS tasks, Lambda runtimes and MSK brokers on the same daemon.
func TestDrainAndSweep_leavesAnotherInstancesRunningContainers(t *testing.T) {
	foreign := ownedContainer("their-db", "their-db", "running", otherInstance)
	d := newSweepDaemon(t, []ContainerSummary{foreign})

	client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	gc := NewGC(client, zap.NewNop(), false, fixedDomain(thisInstance))

	gc.DrainAndSweep(context.Background(), "rds")

	if d.wasRemoved("their-db") {
		t.Error("shutdown destroyed another Overcast's running database")
	}
}

// …while still doing the job it exists for: this instance's own containers go,
// running or not, because at shutdown a running container of ours is exactly
// what needs stopping.
func TestDrainAndSweep_removesItsOwnContainers(t *testing.T) {
	mine := managedContainer("my-db", "my-db", "running")
	d := newSweepDaemon(t, []ContainerSummary{mine})

	client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	gc := NewGC(client, zap.NewNop(), false, fixedDomain(thisInstance))

	gc.DrainAndSweep(context.Background(), "rds")

	if !d.wasRemoved("my-db") {
		t.Error("shutdown left this instance's own container behind")
	}
}

// The data-loss one. An RDS container carries no volume and no bind mount —
// the database lives in the container's writable layer — so a startup sweep
// that removes another instance's stopped DB instance destroys the data, not
// merely the record pointing at it. The veto cannot prevent this on its own:
// it reads this instance's store, which has never heard of the other's
// instances and so vetoes nothing.
func TestSweepExcept_leavesAnotherInstancesStoppedContainers(t *testing.T) {
	foreign := ownedContainer("their-stopped-db", "their-db", "exited", otherInstance)
	d := newSweepDaemon(t, []ContainerSummary{foreign})

	client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	gc := NewGC(client, zap.NewNop(), false, fixedDomain(thisInstance))

	// The veto another instance's container gets: no record of it here.
	gc.SweepExcept("rds", func(string) bool { return false })

	time.Sleep(200 * time.Millisecond)
	if d.wasRemoved("their-stopped-db") {
		t.Error("startup destroyed another Overcast's stopped database — an RDS container holds the data in its writable layer")
	}
}

// Sweep has no veto at all, which is how ElastiCache calls it. Lower stakes —
// a cache is ephemeral — but the same defect: it removed every non-running
// managed container on the daemon, including another instance's.
func TestSweep_leavesAnotherInstancesStoppedContainers(t *testing.T) {
	foreign := ownedContainer("their-cache", "their-cache", "exited", otherInstance)
	d := newSweepDaemon(t, []ContainerSummary{foreign})

	client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	gc := NewGC(client, zap.NewNop(), false, fixedDomain(thisInstance))

	gc.Sweep("elasticache")

	time.Sleep(200 * time.Millisecond)
	if d.wasRemoved("their-cache") {
		t.Error("startup removed another Overcast's cache container")
	}
}

// A container from a version of Overcast that predates the label has no owner
// to establish. Absence of the label is not permission to remove: every sweep
// must leave it, in both directions.
func TestSweeps_leaveUnlabelledContainers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sweep func(*GC)
	}{
		{"startup", func(g *GC) { g.Sweep("rds"); time.Sleep(200 * time.Millisecond) }},
		{"shutdown", func(g *GC) { g.DrainAndSweep(context.Background(), "rds") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := ownedContainer("legacy-container", "legacy-db", "exited", "")
			d := newSweepDaemon(t, []ContainerSummary{legacy})

			client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
			tc.sweep(NewGC(client, zap.NewNop(), false, fixedDomain(thisInstance)))

			if d.wasRemoved("legacy-container") {
				t.Error("removed a container whose owner could not be established")
			}
		})
	}
}

// A store that will not answer is not evidence that anything on the daemon is
// litter, and neither is a GC that was never given a domain to resolve. Both
// resolve to "", and "" sweeps nothing — even the containers that really are
// this instance's own, since it cannot prove that they are.
func TestSweeps_removeNothingWhenOwnershipCannotBeEstablished(t *testing.T) {
	for _, tc := range []struct {
		name   string
		domain InstanceDomainFunc
	}{
		{"no domain wired", nil},
		{"store would not answer", fixedDomain("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mine := managedContainer("some-container", "some-db", "exited")
			d := newSweepDaemon(t, []ContainerSummary{mine})

			client := NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
			gc := NewGC(client, zap.NewNop(), false, tc.domain)

			gc.Sweep("rds")
			time.Sleep(200 * time.Millisecond)
			gc.DrainAndSweep(context.Background(), "rds")

			if d.wasRemoved("some-container") {
				t.Error("swept on an identity that was never established")
			}
		})
	}
}
