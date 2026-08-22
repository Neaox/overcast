package eks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// live_shutdown_test.go — what Stop owes a cluster bootstrap that is still
// running.
//
// CreateCluster hands the k3s bring-up to a goroutine, and Stop could neither
// wait for one nor end one. Both directions cost something real:
//
//   - Cleanup works from the runtime registry, which a bootstrap writes only
//     once it has started its container. A shutdown landing in that window tore
//     down every container Stop knew about and left that one running.
//   - The bootstrap ran on context.Background(), so a control plane that never
//     answers held pollK3sReady's five-minute deadline — against a shutdown
//     budget every other service's Stop shares.

// awaitTimeout bounds the waits below. Everything they wait on is local and
// takes milliseconds, so it is never reached by a working build on a loaded
// machine — it is there so that a bootstrap which fails earlier than expected
// fails these tests with a sentence, rather than hanging until the package's
// ten-minute timeout prints a stack dump.
const awaitTimeout = 30 * time.Second

func awaitSignal(t *testing.T, ch <-chan struct{}, waitingFor string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(awaitTimeout):
		t.Fatalf("timed out after %s waiting for %s", awaitTimeout, waitingFor)
	}
}

// The container a bootstrap is about to register must not outlive the process.
func TestStopWaitsForAnInFlightClusterBootstrap(t *testing.T) {
	fd := newFakeK3sDaemon(t)
	svc := newFakeDaemonService(t, fd)

	bootstrapping := make(chan struct{})
	release := make(chan struct{})
	svc.startLiveClusterHook = func(_ context.Context, region string, cluster *Cluster) {
		close(bootstrapping)
		<-release
		// What the real bootstrap does last: publish the container it started.
		svc.setLiveClusterRuntime(region, cluster.Name, &liveClusterRuntime{containerID: "late-ctr"})
	}

	createLiveCluster(t, svc, "slow-bootstrap")
	awaitSignal(t, bootstrapping, "CreateCluster to hand off to the bootstrap")

	stopReturned := make(chan struct{})
	go func() {
		svc.Stop(context.Background())
		close(stopReturned)
	}()

	// Nothing but this test can end the bootstrap, so a Stop that honours it
	// cannot reach the line below however slow the machine is — the window is
	// there to catch a Stop that does not wait at all, which returns at once.
	select {
	case <-stopReturned:
		t.Fatal("Stop returned while a cluster bootstrap was still in flight — " +
			"the container it was about to register would have outlived the process")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	<-stopReturned

	// And the wait is what makes the drain complete: the entry the bootstrap
	// registered on its way out is only there to be cleaned up if Stop was
	// still behind it.
	if _, found := svc.getLiveClusterRuntime(liveModeTestRegion, "slow-bootstrap"); found {
		t.Fatal("the container the bootstrap registered outlived Stop — " +
			"it drained the runtime registry before the bootstrap had written to it")
	}
}

// And a bootstrap waiting on a control plane that never answers must not hold
// shutdown open — nor leave the record claiming progress once it is ended.
func TestStopEndsABootstrapWaitingOnItsControlPlane(t *testing.T) {
	fd := newFakeK3sDaemon(t)
	// Nothing listens on port 1, so the readiness poll never succeeds and runs
	// to its five-minute deadline.
	fd.readyzPort = "1"
	svc := newFakeDaemonService(t, fd)

	createLiveCluster(t, svc, "never-ready")
	awaitSignal(t, fd.inspected, "the bootstrap to reach the readiness poll")

	stopped := make(chan struct{})
	go func() {
		svc.Stop(context.Background())
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(awaitTimeout):
		t.Fatal("Stop did not return — it is waiting out the readiness poll instead of ending it")
	}

	got := readCluster(t, svc, "never-ready")
	if got.Status != "FAILED" {
		t.Fatalf("expected a cluster whose bootstrap shutdown ended to reach a terminal state, got %q", got.Status)
	}
	if msg := clusterHealthMessage(t, got); !strings.Contains(msg, "shut down") {
		t.Fatalf("expected the health issue to say the shutdown ended the bootstrap, got %q", msg)
	}
}

// TestCreateClusterRefusesAfterStop is the deterministic half of issue #1291:
// CreateCluster's live bootstrap kickoff called liveWg.Add(1) without
// checking liveStopping under liveLifecycleMu first — unlike
// recoverLiveCluster, which does — so a create landing after Stop had begun
// draining could Add to a WaitGroup Stop was already waiting on, and could
// leave a k3s container starting after shutdown. Once liveStopping is set,
// CreateCluster must refuse instead: no runtime registered, no bootstrap
// goroutine, and the record left FAILED with a reason rather than stuck at
// CREATING forever.
func TestCreateClusterRefusesAfterStop(t *testing.T) {
	fd := newFakeK3sDaemon(t)
	svc := newFakeDaemonService(t, fd)

	svc.Stop(context.Background())

	createLiveCluster(t, svc, "too-late")

	if _, found := svc.getLiveClusterRuntime(liveModeTestRegion, "too-late"); found {
		t.Fatal("CreateCluster registered a runtime after Stop — it should refuse instead of starting a container")
	}
	if creates := fd.createdImages(); len(creates) != 0 {
		t.Fatalf("expected no container created for a cluster created after Stop, got %v", creates)
	}

	got := readCluster(t, svc, "too-late")
	if got.Status != "FAILED" {
		t.Fatalf("expected a cluster created after Stop to reach a terminal state, got %q", got.Status)
	}
	if msg := clusterHealthMessage(t, got); !strings.Contains(msg, "shut down") {
		t.Fatalf("expected the health issue to say shutdown refused the create, got %q", msg)
	}
}

// TestCreateClusterRacesStop is the -race regression test for issue #1291.
// bootstrapLiveCluster is suspended so this races only the liveWg.Add /
// liveStopping fence itself, not a real container start: run with
// `go test -race -count=20` to have Go's own repetition find the
// interleaving where a create's liveWg.Add(1) is not yet fenced against a
// concurrent Stop reaching its wg.Wait — the same shape the fixed
// lifecycle.Scheduler.Stop/After race (#1282, PR #1290) had. A single run only
// exercises one interleaving; TestCreateClusterRefusesAfterStop above pins
// down the deterministic contract without depending on winning a race window.
func TestCreateClusterRacesStop(t *testing.T) {
	fd := newFakeK3sDaemon(t)
	svc := newFakeDaemonService(t, fd)
	suspendLiveBootstrap(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)
	payload, err := json.Marshal(map[string]any{
		"name":    "racer",
		"roleArn": "arn:aws:iam::000000000000:role/eks-role",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	var code int
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		code = rec.Code
	}()
	go func() {
		defer wg.Done()
		svc.Stop(context.Background())
	}()
	wg.Wait()

	if code != http.StatusCreated {
		t.Fatalf("expected 201 from CreateCluster racing Stop, got %d — body would explain a real failure", code)
	}
}
