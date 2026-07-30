package rds

// Regression tests for region-scoped lifecycle transitions. The store keys
// instances per region, but the scheduler/event callbacks run outside any
// request context — before the fix they resolved the DEFAULT region, looked
// up the wrong store key for instances created elsewhere, and silently
// no-oped, leaving statuses stuck at "creating"/"stopping"/etc.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
)

const nonDefaultRegion = "eu-west-1"

// newRegionTestHandler returns a handler with a mock clock (so delayed
// transitions are driven explicitly via clk.Add) and no Docker.
func newRegionTestHandler(t *testing.T) (*Handler, *clock.Mock) {
	t.Helper()
	clk := clock.NewMock()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clk)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.handler.scheduler.Stop(ctx)
	})
	return s.handler, clk
}

func instanceStatus(t *testing.T, h *Handler, ctx context.Context, id string) string {
	t.Helper()
	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance(%s): %s: %s", id, aerr.Code, aerr.Message)
	}
	return inst.DBInstanceStatus
}

// TestInstanceLifecycle_nonDefaultRegion drives the full instance lifecycle
// under a non-default region and asserts every scheduled transition lands.
func TestInstanceLifecycle_nonDefaultRegion(t *testing.T) {
	h, clk := newRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), nonDefaultRegion)
	const id = "eu-instance"

	if _, aerr := h.createDBInstanceTyped(ctx, &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "postgres",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	// The instance must exist only under its own region.
	if _, aerr := h.store.getDBInstance(context.Background(), id); aerr == nil {
		t.Fatal("instance unexpectedly visible under the default region")
	}

	clk.Add(time.Millisecond) // fire the 0-delay creating → available transition
	if got := instanceStatus(t, h, ctx, id); got != "available" {
		t.Fatalf("after create: status = %q, want %q (creating → available no-oped)", got, "available")
	}

	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	clk.Add(time.Millisecond)
	if got := instanceStatus(t, h, ctx, id); got != "stopped" {
		t.Fatalf("after stop: status = %q, want %q (stopping → stopped no-oped)", got, "stopped")
	}

	if _, aerr := h.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StartDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	clk.Add(time.Millisecond)
	if got := instanceStatus(t, h, ctx, id); got != "available" {
		t.Fatalf("after start: status = %q, want %q (starting → available no-oped)", got, "available")
	}

	if _, aerr := h.modifyDBInstanceTyped(ctx, &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		DBInstanceClass:      "db.t3.large",
	}); aerr != nil {
		t.Fatalf("ModifyDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	clk.Add(time.Second)
	if got := instanceStatus(t, h, ctx, id); got != "available" {
		t.Fatalf("after modify: status = %q, want %q (modifying → available no-oped)", got, "available")
	}

	if _, aerr := h.deleteDBInstanceTyped(ctx, &deleteDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("DeleteDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	clk.Add(time.Second)
	if _, aerr := h.store.getDBInstance(ctx, id); aerr == nil {
		t.Fatal("after delete: instance record still present (deferred delete no-oped)")
	}
}

// TestClusterLifecycle_nonDefaultRegion covers the DB-cluster equivalents.
func TestClusterLifecycle_nonDefaultRegion(t *testing.T) {
	h, clk := newRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), nonDefaultRegion)
	const id = "eu-cluster"

	clusterStatus := func() string {
		t.Helper()
		c, aerr := h.store.getDBCluster(ctx, id)
		if aerr != nil {
			t.Fatalf("getDBCluster(%s): %s: %s", id, aerr.Code, aerr.Message)
		}
		return c.Status
	}

	if _, aerr := h.createDBClusterTyped(ctx, &createDBClusterReq{
		DBClusterIdentifier: id,
		Engine:              "aurora-postgresql",
		MasterUsername:      "admin",
		MasterUserPassword:  "password123",
	}); aerr != nil {
		t.Fatalf("CreateDBCluster: %s: %s", aerr.Code, aerr.Message)
	}
	clk.Add(time.Second)
	if got := clusterStatus(); got != "available" {
		t.Fatalf("after create: status = %q, want %q (creating → available no-oped)", got, "available")
	}

	if _, aerr := h.stopDBClusterTyped(ctx, &stopDBClusterReq{DBClusterIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBCluster: %s: %s", aerr.Code, aerr.Message)
	}
	clk.Add(time.Second)
	if got := clusterStatus(); got != "stopped" {
		t.Fatalf("after stop: status = %q, want %q (stopping → stopped no-oped)", got, "stopped")
	}

	if _, aerr := h.startDBClusterTyped(ctx, &startDBClusterReq{DBClusterIdentifier: id}); aerr != nil {
		t.Fatalf("StartDBCluster: %s: %s", aerr.Code, aerr.Message)
	}
	clk.Add(time.Second)
	if got := clusterStatus(); got != "available" {
		t.Fatalf("after start: status = %q, want %q (starting → available no-oped)", got, "available")
	}

	if _, aerr := h.deleteDBClusterTyped(ctx, &deleteDBClusterReq{DBClusterIdentifier: id}); aerr != nil {
		t.Fatalf("DeleteDBCluster: %s: %s", aerr.Code, aerr.Message)
	}
	clk.Add(time.Second)
	if _, aerr := h.store.getDBCluster(ctx, id); aerr == nil {
		t.Fatal("after delete: cluster record still present (deferred delete no-oped)")
	}
}

// TestHandleContainerEvent_nonDefaultRegion asserts the Docker died/stopped
// event handler — which only receives a resource ID — finds the instance in
// its actual region and transitions it to "stopped".
func TestHandleContainerEvent_nonDefaultRegion(t *testing.T) {
	h, _ := newRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), nonDefaultRegion)
	const id = "eu-docker-instance"

	inst := &DBInstance{
		DBInstanceIdentifier: id,
		Engine:               "postgres",
		DBInstanceStatus:     "available",
		DockerContainerID:    "cafecafecafe",
		Endpoint:             &Endpoint{Address: "127.0.0.1", Port: 5432},
	}
	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		t.Fatalf("putDBInstance: %s", aerr.Message)
	}

	h.handleContainerEvent(context.Background(), events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: "cafecafecafe",
			Action:      "die",
			Service:     "rds",
			ResourceID:  id,
		},
	})

	if got := instanceStatus(t, h, ctx, id); got != "stopped" {
		t.Fatalf("after container die event: status = %q, want %q (cross-region lookup failed)", got, "stopped")
	}
}

// TestLegacyHandlers_nonDefaultRegion exercises the legacy Query-protocol
// handlers (the typed twins are covered above) with a region on the request
// context, asserting the scheduled transitions land in the right region.
func TestLegacyHandlers_nonDefaultRegion(t *testing.T) {
	h, clk := newRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), nonDefaultRegion)
	const id = "eu-legacy-instance"

	post := func(action string, params map[string]string) int {
		t.Helper()
		form := url.Values{"Action": []string{action}}
		for k, v := range params {
			form.Set(k, v)
		}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		h.dispatch(w, req)
		return w.Code
	}

	if code := post("CreateDBInstance", map[string]string{
		"DBInstanceIdentifier": id,
		"Engine":               "postgres",
		"MasterUsername":       "admin",
		"MasterUserPassword":   "password123",
	}); code != 200 {
		t.Fatalf("CreateDBInstance: HTTP %d", code)
	}
	clk.Add(time.Millisecond)
	if got := instanceStatus(t, h, ctx, id); got != "available" {
		t.Fatalf("after create: status = %q, want %q", got, "available")
	}

	if code := post("StopDBInstance", map[string]string{"DBInstanceIdentifier": id}); code != 200 {
		t.Fatalf("StopDBInstance: HTTP %d", code)
	}
	clk.Add(time.Millisecond)
	if got := instanceStatus(t, h, ctx, id); got != "stopped" {
		t.Fatalf("after stop: status = %q, want %q", got, "stopped")
	}

	if code := post("StartDBInstance", map[string]string{"DBInstanceIdentifier": id}); code != 200 {
		t.Fatalf("StartDBInstance: HTTP %d", code)
	}
	clk.Add(time.Millisecond)
	if got := instanceStatus(t, h, ctx, id); got != "available" {
		t.Fatalf("after start: status = %q, want %q", got, "available")
	}

	if code := post("DeleteDBInstance", map[string]string{"DBInstanceIdentifier": id}); code != 200 {
		t.Fatalf("DeleteDBInstance: HTTP %d", code)
	}
	clk.Add(time.Second)
	if _, aerr := h.store.getDBInstance(ctx, id); aerr == nil {
		t.Fatal("after delete: instance record still present")
	}
}
