package ecs

import (
	"context"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// handleContainerDied is a bus handler for DockerContainerDied events targeting
// ECS containers. When a task container exits, it updates the container status,
// and if all containers in the task are stopped, transitions the task to STOPPED.
func (h *Handler) handleContainerDied(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != "ecs" {
		return
	}

	// ResourceID format: "clusterName/taskID"
	parts := strings.SplitN(p.ResourceID, "/", 2)
	if len(parts) != 2 {
		h.log.Warn("ecs: container died with invalid resource ID",
			zap.String("resourceId", p.ResourceID),
			zap.String("containerId", p.ContainerID))
		return
	}
	taskID := parts[1]

	// The event only carries "cluster/task" — not the region the task was
	// stored under — so locate it with a cross-region scan and pin that
	// region on the context for the write-back.
	task, region, found, err := serviceutil.FindRegioned[Task](context.Background(), h.store.store, nsTasks, p.ResourceID, h.store.defaultRegion)
	if err != nil || !found {
		return
	}
	ctx := middleware.ContextWithRegion(context.Background(), region)

	exitCode, _ := strconv.Atoi(p.ExitCode)

	// Update the container that died.
	for i := range task.Containers {
		if task.Containers[i].DockerID == p.ContainerID {
			task.Containers[i].LastStatus = "STOPPED"
			task.Containers[i].ExitCode = &exitCode
			break
		}
	}

	// Check if all containers are stopped.
	allStopped := true
	for _, c := range task.Containers {
		if c.LastStatus != "STOPPED" {
			allStopped = false
			break
		}
	}

	if allStopped {
		task.LastStatus = "STOPPED"
		task.DesiredStatus = "STOPPED"
		task.StoppedReason = "Essential container in task exited"
		task.StopCode = "EssentialContainerExited"
		stoppedAt := h.clk.Now().Unix()
		task.StoppedAt = &stoppedAt
		task.StoppingAt = &stoppedAt

		// Cancel any pending scheduler transition.
		h.scheduler.CancelScoped(region, taskID, "pending")
	}

	h.store.putTask(ctx, task) //nolint:errcheck

	if allStopped && h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.ECSTaskStopped,
			Payload: events.ResourcePayload{Name: taskID},
		})
	}

	// A service keeps its desired count: a task whose containers exited is
	// replaced. Without this a service drains to zero the first time a task
	// finishes and stays there, which is not what ECS does with one.
	if allStopped {
		if serviceName, ok := serviceNameFromGroup(task.Group); ok {
			// Out of the load balancer's rotation first, then replaced.
			if svc, aerr := h.store.getService(ctx, parts[0], serviceName); aerr == nil && svc != nil {
				h.deregisterTaskTargets(ctx, svc, task)
			}
			h.scheduleServiceReplacement(ctx, region, parts[0], serviceName)
		}
	}
}
