package msk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/state"
)

// TestSetClusterEndpoint_deletedMidStartTearsDownContainer covers the window
// between a container starting and its ID reaching the cluster record.
// deleteCluster stops the ID it finds on the record, so a delete landing in
// that window has nothing to stop — the start goroutine is the only party
// holding the ID and therefore owns the teardown. Same race as RDS (#412) and
// ElastiCache (#459); tracked for MSK in #460.
func TestSetClusterEndpoint_deletedMidStartTearsDownContainer(t *testing.T) {
	// Given: a handler whose Docker calls go to a daemon that is not there.
	// The teardown path must still run — what matters is that the record does
	// not gain a container ID and the port is released, not that Docker
	// answers.
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	h.docker = docker.NewClient("tcp://127.0.0.1:1", zap.NewNop())
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")

	const arn = "arn:aws:kafka:us-east-1:123456789012:cluster/compat-msk/abcd"
	if aerr := h.store.putCluster(ctx, &Cluster{
		ClusterArn: arn, ClusterName: "compat-msk", State: "DELETING",
		CreationTime: time.Now(),
	}); aerr != nil {
		t.Fatalf("seed cluster: %v", aerr)
	}

	// When: the in-flight start finally reports its container
	h.setClusterEndpoint(ctx, arn, "cafecafecafe", 49092)

	// Then: the ID is not written onto a deleting record. Recording it would
	// leave the container running behind a record that is about to vanish,
	// with the delete's stop having already run against an empty ID.
	got, aerr := h.store.getCluster(ctx, arn)
	if aerr != nil {
		t.Fatalf("get cluster: %v", aerr)
	}
	if got.DockerContainerID != "" {
		t.Errorf("DockerContainerID = %q, want empty — a deleting cluster must not adopt the container", got.DockerContainerID)
	}
}

func TestSetClusterEndpoint_recordsContainerForALiveCluster(t *testing.T) {
	// Given: a cluster that is still being created
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")

	const arn = "arn:aws:kafka:us-east-1:123456789012:cluster/compat-msk/abcd"
	if aerr := h.store.putCluster(ctx, &Cluster{
		ClusterArn: arn, ClusterName: "compat-msk", State: "CREATING",
		CreationTime: time.Now(),
	}); aerr != nil {
		t.Fatalf("seed cluster: %v", aerr)
	}

	// When/Then: the normal path is unchanged — the cluster adopts its
	// container so deleteCluster can stop it later.
	h.setClusterEndpoint(ctx, arn, "cafecafecafe", 49092)
	got, aerr := h.store.getCluster(ctx, arn)
	if aerr != nil {
		t.Fatalf("get cluster: %v", aerr)
	}
	if got.DockerContainerID != "cafecafecafe" || got.HostPort != 49092 {
		t.Errorf("cluster = %q/%d, want the container id and port recorded", got.DockerContainerID, got.HostPort)
	}
}

// blockingMSKDaemon is an httptest server speaking just enough of the Docker
// Engine API for the MSK container-start path. The first container start
// request blocks until release() is called, so a test can land a delete while
// the background start is still in flight — the exact interleaving
// TestSetClusterEndpoint_deletedMidStartTearsDownContainer exercises directly
// on setClusterEndpoint. This is the same race driven end-to-end through
// createCluster and deleteCluster, mirroring
// rds/handler_docker_race_test.go and elasticache/handler_docker_race_test.go.
type blockingMSKDaemon struct {
	srv         *httptest.Server
	containerID string
	started     chan struct{}
	release     func()

	mu      sync.Mutex
	stopped bool
	removed bool
}

func (fd *blockingMSKDaemon) stoppedOrRemoved() bool {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return fd.stopped || fd.removed
}

func newBlockingMSKDaemon(t *testing.T) *blockingMSKDaemon {
	t.Helper()
	const containerID = "cafecafecafe"
	started := make(chan struct{})
	releaseCh := make(chan struct{})
	var startMu sync.Mutex
	startCount := 0

	fd := &blockingMSKDaemon{containerID: containerID, started: started}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/images/create"), strings.Contains(p, "/images/"):
			w.WriteHeader(http.StatusOK)

		// GetContainerByName lookup before create — no existing container.
		case strings.Contains(p, "/containers/overcast-msk-") && strings.HasSuffix(p, "/json"):
			w.WriteHeader(http.StatusNotFound)

		case strings.HasSuffix(p, "/networks/create"):
			w.Write([]byte(`{"Id":"net-msk-1"}`)) //nolint:errcheck

		case strings.Contains(p, "/networks/") && r.Method == http.MethodGet:
			w.Write([]byte(`{"Id":"net-msk-1","Name":"bridge","Containers":{}}`)) //nolint:errcheck

		case strings.HasSuffix(p, "/containers/create"):
			w.Write([]byte(`{"Id":"` + containerID + `"}`)) //nolint:errcheck

		case strings.HasSuffix(p, "/containers/"+containerID+"/start"):
			startMu.Lock()
			startCount++
			first := startCount == 1
			startMu.Unlock()
			if first {
				close(started)
				<-releaseCh
			}
			w.WriteHeader(http.StatusNoContent)

		case strings.HasSuffix(p, "/containers/"+containerID+"/stop"):
			fd.mu.Lock()
			fd.stopped = true
			fd.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodDelete && strings.Contains(p, "/containers/"+containerID):
			fd.mu.Lock()
			fd.removed = true
			fd.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		case strings.HasSuffix(p, "/containers/"+containerID+"/json"):
			w.Write([]byte(`{"Id":"` + containerID + `",` + //nolint:errcheck
				`"State":{"Status":"running","Running":true},` +
				`"NetworkSettings":{"Networks":{"overcast_msk":{"IPAddress":"127.0.0.1"}},"Ports":{}}}`))

		default:
			w.WriteHeader(http.StatusOK)
		}
	}))

	var once sync.Once
	fd.srv = srv
	fd.release = func() { once.Do(func() { close(releaseCh) }) }
	// Cleanups run LIFO: srv.Close must run after release, or Close hangs on
	// the blocked start request.
	t.Cleanup(srv.Close)
	t.Cleanup(fd.release)
	return fd
}

func waitMSKStarted(t *testing.T, fd *blockingMSKDaemon) {
	t.Helper()
	select {
	case <-fd.started:
	case <-time.After(10 * time.Second):
		t.Fatal("container start request never reached the fake Docker daemon")
	}
}

// waitMSKCondition polls until ok returns true or the deadline passes.
func waitMSKCondition(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestCreateCluster_deletedMidStartDoesNotLeakContainer drives the race end to
// end through the public handlers, rather than calling setClusterEndpoint
// directly: createCluster dispatches startClusterAsync's goroutine, and while
// it is blocked in the container start, deleteCluster runs. deleteCluster
// stops whatever container ID is on the record — empty, since the start
// hasn't reached setClusterEndpoint yet — so the only thing left to reclaim
// the container is the start goroutine noticing, once it does reach
// setClusterEndpoint, that the record it would write to is gone.
func TestCreateCluster_deletedMidStartDoesNotLeakContainer(t *testing.T) {
	fd := newBlockingMSKDaemon(t)
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	h.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
		h.dockerWg.Wait()
	})

	arn := createClusterVia(t, h, h.createCluster, map[string]any{"clusterName": "leak-test"})
	waitMSKStarted(t, fd)

	// When: the cluster is deleted while the container start is in flight.
	ctx := middleware.ContextWithRegion(context.Background(), "eu-west-1")
	req := httptest.NewRequest(http.MethodDelete, "/v1/clusters/"+arn, nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.deleteCluster(w, req, arn)
	if w.Code != http.StatusOK {
		t.Fatalf("deleteCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	fd.release()

	// Then: the start goroutine tears its own container down. Anything else
	// leaves a running Redpanda container owned by a cluster that no longer
	// exists.
	waitMSKCondition(t, "container stop/remove after mid-start delete", fd.stoppedOrRemoved)

	// And: once the deferred record removal (50ms scheduler timer) lands, the
	// cluster stays gone rather than being resurrected by the start goroutine
	// writing its stale pre-delete snapshot back.
	waitMSKCondition(t, "deleted cluster record to be removed", func() bool {
		_, aerr := h.store.getCluster(ctx, arn)
		return aerr != nil
	})
}
