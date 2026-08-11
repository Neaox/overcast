package eks

import (
	"context"
	"strings"
	"testing"
	"time"
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
