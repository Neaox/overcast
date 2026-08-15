package msk

// metadata_only_test.go — a cluster that no broker is ever coming for.
//
// Without a Docker daemon there is no Redpanda container to start, so no
// readiness watch is scheduled and nothing is left that could move the cluster
// out of CREATING. It stayed there for as long as the record existed:
// `aws kafka wait cluster-active` spun out its attempts and reported a timeout
// over a cluster that was never going to change, and a CloudFormation stack
// holding a wait on one had the same three-quarters of an hour to spend before
// rolling back.
//
// MSK already answers this correctly one shape over — a serverless cluster has
// no brokers to provision, so createClusterV2 marks it ACTIVE straight away —
// and RDS and Lambda answer it for their own metadata-only resources. This is
// the provisioned cluster agreeing.

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
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
)

// createCluster drives a create handler and returns the new cluster's ARN. The
// region is deliberately not the default one: the transition runs on a
// scheduler callback outside any request context, where losing the region means
// reading the wrong store key and silently doing nothing.
func createClusterVia(t *testing.T, h *Handler, fn http.HandlerFunc, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/clusters", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.ContextWithRegion(context.Background(), "eu-west-1"))
	w := httptest.NewRecorder()
	fn(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create: HTTP %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ClusterArn string `json:"clusterArn"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.ClusterArn == "" {
		t.Fatalf("create response: %v (body %s)", err, w.Body.String())
	}
	return resp.ClusterArn
}

// settle runs the transition the create path scheduled. Production runs on a
// real clock, where the scheduler executes a zero-delay transition inline; a
// mock holds it until the clock moves, so the test moves it.
func settle(t *testing.T, h *Handler, clk *clock.Mock) {
	t.Helper()
	h.scheduler.AdvanceAndSettle(clk, time.Millisecond)
}

func TestCreateCluster_withoutABrokerRuntime_becomesACTIVE(t *testing.T) {
	h, clk, _ := newReadinessHandler(t) // no Docker: dockerReady stays false
	ctx := middleware.ContextWithRegion(context.Background(), "eu-west-1")

	arn := createClusterVia(t, h, h.createCluster, map[string]any{"clusterName": "metadata-kafka"})
	settle(t, h, clk)

	got, aerr := h.store.getCluster(ctx, arn)
	if aerr != nil {
		t.Fatalf("getCluster: %s", aerr.Message)
	}
	if got.State == "CREATING" {
		t.Fatal("State is still CREATING: no broker container was started, so no readiness " +
			"watch exists to move it — the cluster is as ready as it will ever be and " +
			"`aws kafka wait cluster-active` spins until it gives up")
	}
	if got.State != "ACTIVE" {
		t.Fatalf("State = %q, want ACTIVE", got.State)
	}
	if got.StateInfo != nil {
		t.Errorf("StateInfo = %+v, want none: nothing failed", got.StateInfo)
	}
}

// CreateClusterV2's provisioned branch is a second create path onto the same
// record, and it took the same CREATING that nothing would change.
func TestCreateClusterV2Provisioned_withoutABrokerRuntime_becomesACTIVE(t *testing.T) {
	h, clk, _ := newReadinessHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "eu-west-1")

	arn := createClusterVia(t, h, h.createClusterV2, map[string]any{
		"clusterName": "metadata-kafka-v2",
		"provisioned": map[string]any{"numberOfBrokerNodes": 1},
	})
	settle(t, h, clk)

	got, aerr := h.store.getCluster(ctx, arn)
	if aerr != nil {
		t.Fatalf("getCluster: %s", aerr.Message)
	}
	if got.State != "ACTIVE" {
		t.Fatalf("State = %q, want ACTIVE", got.State)
	}
}

// The other half of the rule: where a broker container is on its way, create
// must leave the state alone. Promoting there would make the readiness watch a
// no-op — it only ever moves a cluster out of CREATING — which is how a cluster
// whose broker never answered would go back to reporting ACTIVE forever.
func TestCreateCluster_withABrokerComing_staysCREATING(t *testing.T) {
	fd := newFakeMSKDockerDaemon(t)
	h, _, _ := newReadinessHandler(t)
	h.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	ctx := middleware.ContextWithRegion(context.Background(), "eu-west-1")

	arn := createClusterVia(t, h, h.createCluster, map[string]any{"clusterName": "container-kafka"})
	h.dockerWg.Wait()

	got, aerr := h.store.getCluster(ctx, arn)
	if aerr != nil {
		t.Fatalf("getCluster: %s", aerr.Message)
	}
	if got.State != "CREATING" {
		t.Errorf("State = %q, want CREATING: nothing has confirmed the broker answers yet", got.State)
	}
}
