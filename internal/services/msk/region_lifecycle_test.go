package msk

// Regression test: MSK clusters are stored under region-scoped keys, but the
// CREATING → ACTIVE transition runs on a scheduler callback outside any
// request context. Before the fix the callback resolved the DEFAULT region
// and clusters created elsewhere were stuck in CREATING forever.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
)

func TestClusterLifecycle_nonDefaultRegion(t *testing.T) {
	// Docker is wired to a fake daemon rather than skipped for want of a real
	// one. The subject is the health check's region handling, and only the
	// Docker path schedules a health check: a metadata-only handler reaches
	// ACTIVE by the create path's own transition (readiness.go), which would
	// pass this test without the health check having run at all.
	// See docker_fake_test.go.
	fd := newFakeMSKDockerDaemon(t)

	// Take the port before Overcast allocates it, and make it the base so the
	// allocation lands on the one already held.
	//
	// The order matters and the other way round is a flake: allocatePort only
	// scans its own store, so for a fresh store it returns portBase itself
	// without ever asking the OS whether that port is free. Listening on the
	// allocated port afterwards therefore fails with "address already in use"
	// wherever something else holds it — which is how a hardcoded 49092 passed
	// on one CI runner and reddened main on the next.
	// The stand-in answers ApiVersions, because since the readiness rework a
	// dial that merely connects no longer promotes anything: an accepting port
	// is a port proxy, and only something speaking Kafka is a broker. A bare
	// net.Listen here left the cluster in CREATING — correctly.
	_, brokerPort := serveBroker(t, 0)

	clk := clock.NewMock()
	// MSKPortBase must be set: the broker's published port is allocated from it,
	// and the health check dials that port. Left at zero the allocation yields
	// no port and the cluster never leaves CREATING.
	svc := New(&config.Config{Region: "us-east-1", AccountID: "123456789012", MSKPortBase: brokerPort},
		state.NewMemoryStore(), zap.NewNop(), clk)
	h := svc.handler
	h.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
		h.dockerWg.Wait()
	})
	ctx := middleware.ContextWithRegion(context.Background(), "eu-west-1")

	body, _ := json.Marshal(map[string]any{"clusterName": "eu-kafka"})
	req := httptest.NewRequest(http.MethodPost, "/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.createCluster(w, req)
	if w.Code != 200 {
		t.Fatalf("createCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ClusterArn string `json:"clusterArn"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.ClusterArn == "" {
		t.Fatalf("createCluster response: %v (body %s)", err, w.Body.String())
	}

	// The ARN must carry the request region, and the record must be invisible
	// under the default region.
	if got, _ := h.store.getCluster(context.Background(), resp.ClusterArn); got != nil {
		t.Fatal("cluster unexpectedly visible under the default region")
	}

	// The container start runs on a background goroutine, and it is what
	// schedules the health check that promotes the cluster. Wait for it before
	// touching the clock, or there is no health check to fire yet.
	h.dockerWg.Wait()

	started, aerr := h.store.getCluster(ctx, resp.ClusterArn)
	if aerr != nil || started == nil {
		t.Fatalf("getCluster after start: %v", aerr)
	}
	// The health check promotes the cluster only once something answers on the
	// published port — it is a real TCP dial, not a metadata flip — and the
	// listener standing in for the broker is the one reserved above.
	if started.HostPort != brokerPort {
		t.Fatalf("cluster published port %d, want the reserved %d: nothing is listening there",
			started.HostPort, brokerPort)
	}

	// Fire the 1s health check and wait for it: the mock clock runs the callback
	// on a goroutine of its own, so advancing the clock alone leaves the read on
	// the next line racing it.
	h.scheduler.AdvanceAndSettle(clk, 2*time.Second)
	got, aerr := h.store.getCluster(ctx, resp.ClusterArn)
	if aerr != nil {
		t.Fatalf("getCluster(eu-west-1): %s", aerr.Message)
	}
	if got.State != "ACTIVE" {
		t.Fatalf("cluster state = %q, want %q (CREATING → ACTIVE no-oped)", got.State, "ACTIVE")
	}

	// A cluster reporting ACTIVE must have a container behind it — otherwise
	// the handler was metadata-only and the promotion proved nothing.
	if n := fd.startedCount(); n != 1 {
		t.Fatalf("containers started = %d, want 1: the cluster reported ACTIVE without one behind it", n)
	}
}
