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
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
	"github.com/Neaox/overcast/tests/helpers"
)

func TestClusterLifecycle_nonDefaultRegion(t *testing.T) {
	helpers.SkipWithoutDocker(t)
	clk := clock.NewMock()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"}, state.NewMemoryStore(), zap.NewNop(), clk)
	h := svc.handler
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
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

	// Fire the 0-delay CREATING → ACTIVE transition and wait for it: the mock
	// clock runs the callback on a goroutine of its own, so advancing the clock
	// alone leaves the read on the next line racing it.
	h.scheduler.AdvanceAndSettle(clk, time.Millisecond)
	got, aerr := h.store.getCluster(ctx, resp.ClusterArn)
	if aerr != nil {
		t.Fatalf("getCluster(eu-west-1): %s", aerr.Message)
	}
	if got.State != "ACTIVE" {
		t.Fatalf("cluster state = %q, want %q (CREATING → ACTIVE no-oped)", got.State, "ACTIVE")
	}
}
