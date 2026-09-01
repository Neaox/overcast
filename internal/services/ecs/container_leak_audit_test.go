package ecs

// container_leak_audit_test.go — the ECS instance of #460's audit.
//
// RDS (#412), ElastiCache (#459) and Lambda's proactive-init pool
// (runtime_pool.go's `p.evicted` check, see lambda/proactive_test.go's
// TestProactiveInit_deletedMidStartDoesNotLeakContainer) all guard the same
// shape of race: a background goroutine starts a container after its
// resource record already exists and is visible to a delete, so the delete
// finds nothing to stop and the goroutine has to notice for itself, once it
// finishes, that the record it would write to is gone.
//
// ECS's task placement has no such goroutine to guard. RunTask and the
// service reconciler both funnel through launchTask (launch.go), which runs
// startTaskContainers to completion — success, or already unwound on failure
// (#1084) — and only then makes one call to h.store.putTask. A task is
// therefore never visible to StopTask, DescribeTasks or DeleteCluster until
// its containers are already fully placed and recorded in that same write:
// there is no window where a container's ID exists without a record naming
// it for a delete to find nothing to stop.
//
// (The two ECS-shaped versions of "a container that came up got left
// behind" that this synchronous shape *can* produce — a multi-container
// task partly placed when a later container fails, and the CBOR StopTask
// path killing a container before recording the stop — were the subject of
// #1084 and #1083 respectively, both already fixed.)
//
// This test pins the invisibility invariant directly, so a change that
// started persisting a task before its containers are up — the shape every
// other site had to be guarded against — would fail it rather than silently
// opening the same race here.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/middleware"
)

func TestRunTask_taskIsInvisibleUntilItsContainersAreFullyPlaced(t *testing.T) {
	// Given: a task whose one container's start is blocked mid-flight, the
	// way a slow image pull holds a real one open.
	h, _, fd := newECSDockerTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")

	block := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	fd.setOnStart(func(string) {
		once.Do(func() { close(started) })
		<-block
	})

	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "demo"}); w.Code != http.StatusOK {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family":               "web",
		"containerDefinitions": []map[string]any{{"name": "app", "image": "app:1"}},
	}); w.Code != http.StatusOK {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}

	// When: RunTask is placing the task, blocked on its container's start —
	// the container the daemon is about to run has no record naming it yet.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- postJSON(t, ctx, h.RunTask, map[string]any{
			"cluster":        "demo",
			"taskDefinition": "web:1",
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("container start was never reached")
	}

	// Then: nothing is there yet for a concurrent StopTask or DeleteCluster to
	// race against.
	tasks, aerr := h.store.listTasks(ctx, "demo")
	if aerr != nil {
		t.Fatalf("listTasks: %s", aerr.Message)
	}
	if len(tasks) != 0 {
		t.Fatalf("listTasks mid-start = %d tasks, want 0 — a task became visible before its container was placed", len(tasks))
	}

	// When: the start completes.
	close(block)
	w := <-done
	if w.Code != http.StatusOK {
		t.Fatalf("RunTask: HTTP %d: %s", w.Code, w.Body.String())
	}

	// Then: the task and its container ID land together, in the one write —
	// there was never a moment where a caller could observe one without the
	// other.
	tasks, aerr = h.store.listTasks(ctx, "demo")
	if aerr != nil {
		t.Fatalf("listTasks: %s", aerr.Message)
	}
	if len(tasks) != 1 {
		t.Fatalf("listTasks after RunTask = %d tasks, want 1", len(tasks))
	}
	if len(tasks[0].Containers) == 0 || tasks[0].Containers[0].DockerID == "" {
		t.Fatal("task recorded with no container ID — the one write that makes it visible didn't carry its container")
	}
}
