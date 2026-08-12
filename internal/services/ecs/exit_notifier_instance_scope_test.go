package ecs

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
)

// exit_notifier_instance_scope_test.go — a container exit is acted on only by
// the Overcast that created the container.
//
// The exit notifier matched a die event to a task record by the
// overcast.resource-id label alone. Two Overcasts sharing a Docker daemon keep
// separate state stores, so nothing about that label distinguishes theirs from
// ours: acting on a foreign one stops a task whose own containers are still
// running, counts the death against the service's deployment, and schedules a
// replacement for a task nothing is missing.
//
// An ECS resource ID is "cluster/taskID" and the task ID is a minted UUID, so
// two independent stores do not collide on one by accident the way
// ElastiCache's caller-chosen names do. What this check earns is the case
// where they are not independent — a state directory restored or copied
// between two running Overcasts, which leaves both holding the same task IDs
// while each creates its own containers for them.

const (
	scopeNeighbourInstance = "another-overcast-instance"
	scopeNeighbourDockerID = "d0d0d0d0d0d0"
)

// seedRunningTask stores a RUNNING task of the service with one RUNNING
// container this instance created.
func seedRunningTask(t *testing.T, h *Handler, ctx context.Context, cluster, service, taskID string) Task {
	t.Helper()
	task := Task{
		TaskArn:       h.taskARN(ctx, cluster, taskID),
		ClusterArn:    h.clusterARN(ctx, cluster),
		Group:         serviceGroupPrefix + service,
		LastStatus:    "RUNNING",
		DesiredStatus: "RUNNING",
		Containers: []Container{{
			Name:       "app",
			DockerID:   "docker-" + taskID,
			LastStatus: "RUNNING",
		}},
	}
	if aerr := h.store.putTask(ctx, &task); aerr != nil {
		t.Fatalf("putTask: %v", aerr)
	}
	return task
}

func taskStatus(t *testing.T, h *Handler, ctx context.Context, cluster, taskID string) string {
	t.Helper()
	got, aerr := h.store.getTask(ctx, cluster, taskID)
	if aerr != nil || got == nil {
		t.Fatalf("getTask(%s): %v", taskID, aerr)
	}
	return got.LastStatus
}

// seedPlacingTask stores a task in the window a placement leaves open: the
// record exists, but no container ID has been written onto it yet.
//
// This is where a foreign die event does its damage. recordContainerExit
// decides the task is finished by finding no container still running, and a
// task with none recorded yet satisfies that vacuously — so the event stops a
// task that is still being placed, tears down its volumes, counts the death
// against the deployment and schedules a replacement, all on the strength of a
// container another Overcast owns.
func seedPlacingTask(t *testing.T, h *Handler, ctx context.Context, cluster, service, taskID string) {
	t.Helper()
	if aerr := h.store.putTask(ctx, &Task{
		TaskArn:       h.taskARN(ctx, cluster, taskID),
		ClusterArn:    h.clusterARN(ctx, cluster),
		Group:         serviceGroupPrefix + service,
		LastStatus:    "PROVISIONING",
		DesiredStatus: "RUNNING",
	}); aerr != nil {
		t.Fatalf("putTask: %v", aerr)
	}
}

// A die event for another Overcast's container names a task this instance also
// holds, and stops it while it is still being placed.
func TestHandleContainerDied_ignoresAnotherOvercastsContainer(t *testing.T) {
	h, _ := newECSRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	const cluster, service, taskID = "scope-cluster", "scope-svc", "scope-task-1"
	createRaceService(t, h, ctx, cluster, service)
	seedPlacingTask(t, h, ctx, cluster, service, taskID)

	h.handleContainerDied(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			Service:     "ecs",
			ResourceID:  cluster + "/" + taskID,
			ContainerID: scopeNeighbourDockerID,
			Instance:    scopeNeighbourInstance,
			ExitCode:    "7",
		},
	})

	if got := taskStatus(t, h, ctx, cluster, taskID); got != "PROVISIONING" {
		t.Fatalf("another Overcast's container died and this instance's task went to %q; want it left PROVISIONING", got)
	}
}

// The same event for this instance's own container must still stop the task —
// the check has to reject a neighbour without deafening the notifier to its
// own containers.
func TestHandleContainerDied_stillActsOnOwnContainer(t *testing.T) {
	h, _ := newECSRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	const cluster, service, taskID = "scope-cluster", "scope-svc", "scope-task-2"
	createRaceService(t, h, ctx, cluster, service)
	task := seedRunningTask(t, h, ctx, cluster, service, taskID)

	h.handleContainerDied(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			Service:     "ecs",
			ResourceID:  cluster + "/" + taskID,
			ContainerID: task.Containers[0].DockerID,
			Instance:    h.instances.Resolve(ctx),
			ExitCode:    "7",
		},
	})

	if got := taskStatus(t, h, ctx, cluster, taskID); got != "STOPPED" {
		t.Fatalf("this instance's own container died and the task is %q; want STOPPED", got)
	}
}

// A container created before overcast.instance existed carries no identity, so
// the task's own record of the container ID vouches for it. Refusing it would
// leave a task RUNNING whose containers have all exited.
func TestHandleContainerDied_unlabelledOwnContainerStillMatches(t *testing.T) {
	h, _ := newECSRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	const cluster, service, taskID = "scope-cluster", "scope-svc", "scope-task-3"
	createRaceService(t, h, ctx, cluster, service)
	task := seedRunningTask(t, h, ctx, cluster, service, taskID)

	h.handleContainerDied(ctx, containerDiedEvent(cluster, taskID, task.Containers[0].DockerID))

	if got := taskStatus(t, h, ctx, cluster, taskID); got != "STOPPED" {
		t.Fatalf("an unlabelled container this task records died and the task is %q; want STOPPED", got)
	}
}

// An unlabelled container the task does not record is not this instance's
// either — nothing vouches for it, so it is left alone.
func TestHandleContainerDied_unlabelledForeignContainerDoesNotMatch(t *testing.T) {
	h, _ := newECSRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	const cluster, service, taskID = "scope-cluster", "scope-svc", "scope-task-4"
	createRaceService(t, h, ctx, cluster, service)
	seedPlacingTask(t, h, ctx, cluster, service, taskID)

	h.handleContainerDied(ctx, containerDiedEvent(cluster, taskID, scopeNeighbourDockerID))

	if got := taskStatus(t, h, ctx, cluster, taskID); got != "PROVISIONING" {
		t.Fatalf("an unlabelled container this task never recorded died and the task went to %q; want it left PROVISIONING", got)
	}
}
