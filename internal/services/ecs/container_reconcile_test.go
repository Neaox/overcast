package ecs

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
)

func TestLaunchTask_containerExitCannotRaceTaskPublication(t *testing.T) {
	// Given: a Docker-backed task whose container may exit as soon as Docker
	// accepts the start request.
	h, _, daemon := newECSDockerTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "demo"}); w.Code != 200 {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family":               "fast-exit",
		"containerDefinitions": []map[string]any{{"name": "app", "image": "busybox"}},
	}); w.Code != 200 {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}

	var exitCanRace atomic.Bool
	daemon.setOnStart(func(_ string) {
		resourceID := daemon.latestResourceID()
		parts := strings.SplitN(resourceID, "/", 2)
		if len(parts) != 2 {
			exitCanRace.Store(true)
			return
		}
		mu := &h.taskLocks[taskLockStripe(resourceID)]
		if mu.TryLock() {
			exitCanRace.Store(true)
			mu.Unlock()
		}
	})

	// When: the task starts a container.
	w := postJSON(t, ctx, h.RunTask, map[string]any{
		"cluster":        "demo",
		"taskDefinition": "fast-exit",
	})
	if w.Code != 200 {
		t.Fatalf("RunTask: HTTP %d: %s", w.Code, w.Body.String())
	}

	// Then: the exit notifier cannot enter its task-record critical section
	// until launch has published the task and its Docker container IDs.
	if exitCanRace.Load() {
		t.Fatal("Docker start was not atomic with task publication; an immediate exit can be dropped")
	}
}

func TestServiceTask_fastExitOneIsStoppedAndReplaced(t *testing.T) {
	// Given: a service whose first container exits with status 1 immediately
	// after Docker accepts the start request.
	h, clk, daemon := newECSDockerTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "demo"}); w.Code != 200 {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family":               "fast-exit",
		"containerDefinitions": []map[string]any{{"name": "app", "image": "busybox"}},
	}); w.Code != 200 {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}

	exitHandled := make(chan struct{})
	var first atomic.Bool
	daemon.setOnStart(func(containerID string) {
		if !first.CompareAndSwap(false, true) {
			return
		}
		resourceID := daemon.latestResourceID()
		go func() {
			defer close(exitHandled)
			h.handleContainerDied(ctx, events.Event{
				Type: events.DockerContainerDied,
				Payload: events.DockerContainerPayload{
					ContainerID: containerID,
					Action:      "die",
					ExitCode:    "1",
					Reason:      "exit 1",
					Service:     serviceName,
					ResourceID:  resourceID,
				},
			})
		}()
	})

	// When: ECS creates the service and receives the immediate die event.
	w := postJSON(t, ctx, h.CreateService, map[string]any{
		"cluster":        "demo",
		"serviceName":    "worker",
		"taskDefinition": "fast-exit",
		"desiredCount":   1,
	})
	if w.Code != 200 {
		t.Fatalf("CreateService: HTTP %d: %s", w.Code, w.Body.String())
	}
	select {
	case <-exitHandled:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the immediate container exit to be handled")
	}

	// Then: the failed task preserves exit 1 instead of transitioning to
	// RUNNING, and the service scheduler launches a fresh replacement.
	tasks, aerr := h.store.listTasks(ctx, "demo")
	if aerr != nil || len(tasks) != 1 {
		t.Fatalf("listTasks after exit: got %d, error %v", len(tasks), aerr)
	}
	failed := tasks[0]
	if failed.LastStatus != "STOPPED" || failed.StopCode != "EssentialContainerExited" {
		t.Fatalf("failed task = status %q, stopCode %q; want STOPPED/EssentialContainerExited",
			failed.LastStatus, failed.StopCode)
	}
	if failed.Containers[0].ExitCode == nil || *failed.Containers[0].ExitCode != 1 {
		t.Fatalf("failed container exitCode = %v, want 1", failed.Containers[0].ExitCode)
	}
	service, aerr := h.store.getService(ctx, "demo", "worker")
	if aerr != nil || service == nil {
		t.Fatalf("getService after exit: %v", aerr)
	}
	if deployment := primaryDeployment(service); deployment == nil || deployment.RolloutState == rolloutCompleted {
		t.Fatalf("deployment after exit = %#v, want a non-completed PRIMARY deployment", deployment)
	}

	h.scheduler.AdvanceAndSettle(clk, replacementMinDelay)
	h.scheduler.AdvanceAndSettle(clk, 200*time.Millisecond)
	h.scheduler.AdvanceAndSettle(clk, deploymentRecoveryWindow)
	tasks, aerr = h.store.listTasks(ctx, "demo")
	if aerr != nil {
		t.Fatalf("listTasks after replacement: %s", aerr.Message)
	}
	if len(tasks) != 2 {
		t.Fatalf("task count after replacement = %d, want 2", len(tasks))
	}
	running := 0
	for _, task := range tasks {
		if task.LastStatus == "RUNNING" {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("running replacements = %d, want 1", running)
	}
	service, aerr = h.store.getService(ctx, "demo", "worker")
	if aerr != nil || service == nil {
		t.Fatalf("getService after replacement: %v", aerr)
	}
	deployment := primaryDeployment(service)
	if len(service.Deployments) != 1 || deployment == nil || deployment.RolloutState != rolloutCompleted {
		t.Fatalf("deployment after replacement = %#v, want one completed PRIMARY deployment", service.Deployments)
	}
}

func TestReconcileContainers_exitedContainerStopsTask(t *testing.T) {
	// Given: ECS still reports a task as running after its managed container
	// exited while the Docker event stream was unavailable.
	h, _ := newECSRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	task := &Task{
		TaskArn:       h.taskARN(ctx, "demo", "task-1"),
		ClusterArn:    h.clusterARN(ctx, "demo"),
		LastStatus:    "RUNNING",
		DesiredStatus: "RUNNING",
		Containers: []Container{{
			Name:       "app",
			DockerID:   "container-1",
			RuntimeId:  "container-1",
			LastStatus: "RUNNING",
		}},
	}
	if aerr := h.store.putTask(ctx, task); aerr != nil {
		t.Fatalf("putTask: %s", aerr.Message)
	}

	// When: the central Docker probe reconciles the exited container.
	h.reconcileContainers(ctx, []docker.ContainerSummary{{
		ID:    "container-1",
		State: "exited",
		Labels: map[string]string{
			docker.LabelManaged:    "true",
			docker.LabelService:    serviceName,
			docker.LabelResourceID: "demo/task-1",
		},
	}})

	// Then: ECS no longer advertises a dead container as a running task.
	got, aerr := h.store.getTask(ctx, "demo", "task-1")
	if aerr != nil || got == nil {
		t.Fatalf("getTask: %v", aerr)
	}
	if got.LastStatus != "STOPPED" {
		t.Fatalf("task status = %q, want STOPPED", got.LastStatus)
	}
	if got.StopCode != "EssentialContainerExited" {
		t.Errorf("stopCode = %q, want EssentialContainerExited", got.StopCode)
	}
}
