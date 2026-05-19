package ecs

import (
	"context"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
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
	clusterName, taskID := parts[0], parts[1]

	ctx := context.Background()
	task, aerr := h.store.getTask(ctx, clusterName, taskID)
	if aerr != nil || task == nil {
		return
	}

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
		stoppedAt := h.clk.Now().Unix()
		task.StoppedAt = &stoppedAt

		// Cancel any pending scheduler transition.
		h.scheduler.Cancel(taskID + ":pending")
	}

	h.store.putTask(ctx, task) //nolint:errcheck

	if allStopped && h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.ECSTaskStopped,
			Payload: events.ResourcePayload{Name: taskID},
		})
	}
}
