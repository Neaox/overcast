package ecs

// deployment_failure_race_test.go — a task death counts against the deployment
// even when the service scheduler is reconciling at the same time.
//
// A service is stored as one JSON blob, so counting a failure is a read of the
// whole record, an edit and a write of the whole record. reconcile is the same
// shape, and the two run concurrently by design: the Docker event stream
// reports the container that died while the scheduler is placing its
// replacement. Whichever writes second wins outright, and when that is the
// reconcile the increment is gone — failedTasks undercounts, and with it the
// "unable to consistently start tasks" event and the circuit breaker, both of
// which are thresholds on that number.
//
// The window is real rather than theoretical: reconcile holds its read across
// listing tasks and placing containers before it writes.

import (
	"context"
	"sync"
	"testing"

	"github.com/Neaox/overcast/internal/middleware"
)

// TestTaskDeathsCountedWhileReconciling — every death counts, even against a
// concurrent reconcile of the same service.
func TestTaskDeathsCountedWhileReconciling(t *testing.T) {
	// Given: a service with tasks of its own, so reconcile has work to read
	h, _ := newECSRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	const cluster, service = "race-cluster", "race-svc"

	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": cluster}); w.Code != 200 {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family":               service + "-td",
		"containerDefinitions": []map[string]any{{"name": "app", "image": "busybox"}},
	}); w.Code != 200 {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.CreateService, map[string]any{
		"cluster": cluster, "serviceName": service,
		"taskDefinition": service + "-td:1", "desiredCount": 1,
	}); w.Code != 200 {
		t.Fatalf("CreateService: HTTP %d: %s", w.Code, w.Body.String())
	}

	svc, aerr := h.store.getService(ctx, cluster, service)
	if aerr != nil || svc == nil {
		t.Fatalf("getService: %v", aerr)
	}
	d := primaryDeployment(svc)
	if d == nil {
		t.Fatal("expected a PRIMARY deployment")
	}
	// A task of that deployment, of the shape the exit notifier hands over.
	dead := &Task{
		TaskArn:   "arn:aws:ecs:us-east-1:123456789012:task/" + cluster + "/dead",
		Group:     serviceGroupPrefix + service,
		StartedBy: d.ID,
	}

	// When: deaths are recorded while the service is being reconciled
	const deaths = 40
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range deaths {
			h.recordServiceTaskDeath(ctx, cluster, service, dead)
		}
	}()
	go func() {
		defer wg.Done()
		for range deaths {
			h.reconcile(ctx, cluster, service)
		}
	}()
	wg.Wait()

	// Then: not one of them was overwritten by the reconcile's own write
	svc, aerr = h.store.getService(ctx, cluster, service)
	if aerr != nil || svc == nil {
		t.Fatalf("getService: %v", aerr)
	}
	d = primaryDeployment(svc)
	if d == nil {
		t.Fatal("expected a PRIMARY deployment")
	}
	if d.FailedTasks != deaths {
		t.Errorf("failedTasks = %d, want %d — a concurrent reconcile discarded %d of them",
			d.FailedTasks, deaths, deaths-d.FailedTasks)
	}
}
