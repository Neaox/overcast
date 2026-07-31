package ec2

// Regression test: the EC2 store keys instances per region, but state
// transitions run on scheduler callbacks outside any request context. Before
// the region-scoped scheduler API those callbacks resolved the DEFAULT
// region, so instances launched elsewhere stayed pending/stopping/
// shutting-down forever.

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
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
)

func TestInstanceLifecycle_nonDefaultRegion(t *testing.T) {
	clk := clock.NewMock()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "123456789012"}, state.NewMemoryStore(), zap.NewNop(), clk)
	h := svc.handler
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
	})
	ctx := middleware.ContextWithRegion(context.Background(), "eu-west-1")

	post := func(fn http.HandlerFunc, params url.Values) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		fn(w, req)
		return w.Code
	}

	if code := post(h.RunInstances, url.Values{"ImageId": []string{"ami-12345678"}, "MinCount": []string{"1"}, "MaxCount": []string{"1"}}); code != 200 {
		t.Fatalf("RunInstances: HTTP %d", code)
	}

	instances, aerr := h.store.listInstances(ctx)
	if aerr != nil || len(instances) != 1 {
		t.Fatalf("listInstances(eu-west-1) = %d instances (err %v), want 1", len(instances), aerr)
	}
	id := instances[0].InstanceID

	// The instance must be invisible under the default region.
	if defInst, _ := h.store.listInstances(context.Background()); len(defInst) != 0 {
		t.Fatalf("instance leaked into the default region (%d found)", len(defInst))
	}

	state := func() string {
		t.Helper()
		got, aerr := h.store.getInstance(ctx, id)
		if aerr != nil {
			t.Fatalf("getInstance(%s): %s", id, aerr.Message)
		}
		return got.State.Name
	}

	clk.Add(time.Millisecond)
	if got := state(); got != "running" {
		t.Fatalf("after launch: state = %q, want %q (pending → running no-oped)", got, "running")
	}

	if code := post(h.StopInstances, url.Values{"InstanceId.1": []string{id}}); code != 200 {
		t.Fatalf("StopInstances: HTTP %d", code)
	}
	clk.Add(time.Millisecond)
	if got := state(); got != "stopped" {
		t.Fatalf("after stop: state = %q, want %q (stopping → stopped no-oped)", got, "stopped")
	}

	if code := post(h.StartInstances, url.Values{"InstanceId.1": []string{id}}); code != 200 {
		t.Fatalf("StartInstances: HTTP %d", code)
	}
	clk.Add(time.Millisecond)
	if got := state(); got != "running" {
		t.Fatalf("after start: state = %q, want %q (pending → running no-oped)", got, "running")
	}

	if code := post(h.TerminateInstances, url.Values{"InstanceId.1": []string{id}}); code != 200 {
		t.Fatalf("TerminateInstances: HTTP %d", code)
	}
	clk.Add(time.Second)
	if got := state(); got != "terminated" {
		t.Fatalf("after terminate: state = %q, want %q (shutting-down → terminated no-oped)", got, "terminated")
	}
}
