package ecs

import (
	"context"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// reconcileContainers brings stored task state back in line with Docker after
// startup or an event-stream reconnect. It deliberately feeds the same exit
// handler as a live die event so stop metadata, service failure accounting,
// replacement scheduling, log retention, and cleanup have one owner.
func (h *Handler) reconcileContainers(ctx context.Context, containers []docker.ContainerSummary) {
	tasks, err := serviceutil.ScanRegions[Task](ctx, h.store.store, nsTasks, h.store.defaultRegion)
	if err != nil {
		h.log.Warn("ecs: reconcile containers: list tasks", zap.Error(err))
		return
	}
	byResource := docker.ContainersByResource(containers)
	for _, regioned := range tasks {
		task := regioned.Value
		if task.LastStatus == "STOPPED" {
			continue
		}
		rctx := middleware.ContextWithRegion(ctx, regioned.Region)
		resourceID := extractClusterName(task.ClusterArn) + "/" + extractTaskID(task.TaskArn)
		h.reconcileTaskContainers(rctx, task, resourceID, byResource[resourceID])
	}
}

func (h *Handler) reconcileTaskContainers(ctx context.Context, task *Task, resourceID string, candidates []*docker.ContainerSummary) {
	for _, container := range task.Containers {
		if container.DockerID == "" || container.LastStatus == "STOPPED" {
			continue
		}
		actual := h.instances.OwnContainer(ctx, candidates, container.DockerID)
		if actual != nil && !containerHasExited(actual.State) {
			continue
		}
		payload, exitTime := h.reconciledContainerExit(ctx, container.DockerID, resourceID, actual)
		h.handleContainerDied(ctx, events.Event{Type: events.DockerContainerDied, Time: exitTime, Payload: payload})
	}
}

func (h *Handler) reconciledContainerExit(ctx context.Context, recordedID, resourceID string, actual *docker.ContainerSummary) (events.DockerContainerPayload, time.Time) {
	payload := events.DockerContainerPayload{
		ContainerID: recordedID,
		Action:      "die",
		Service:     serviceName,
		ResourceID:  resourceID,
	}
	if actual == nil {
		return payload, time.Time{}
	}
	payload.ContainerID = actual.ID
	payload.Instance = actual.Instance()
	if h.docker == nil {
		return payload, time.Time{}
	}
	info, err := h.docker.InspectContainer(ctx, actual.ID)
	if err != nil {
		h.log.Debug("ecs: reconcile containers: inspect exited container",
			zap.String("container", actual.ID), zap.Error(err))
		return payload, time.Time{}
	}
	payload.ExitCode = strconv.Itoa(info.State.ExitCode)
	payload.Reason = info.ExitReason()
	return payload, info.ExitTime()
}

func containerHasExited(state string) bool {
	switch strings.ToLower(state) {
	case "dead", "exited", "removing":
		return true
	default:
		return false
	}
}

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

	region, task, allStopped := h.recordContainerExit(clusterName, taskID, p, e.Time)
	if task == nil {
		return
	}
	ctx := middleware.ContextWithRegion(context.Background(), region)
	h.retainContainerLogs(ctx, task, p.ContainerID)
	if !allStopped {
		return
	}

	// The task is fully stopped, so its task-lifetime volumes have no reader
	// left. Shared-scope volumes are left alone; see removeTaskVolumes.
	h.removeTaskVolumes(ctx, clusterName, taskID)

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.ECSTaskStopped,
			Time:    e.Time,
			Payload: events.ResourcePayload{Name: taskID},
		})
	}

	// A service keeps its desired count: a task whose containers exited is
	// replaced. Without this a service drains to zero the first time a task
	// finishes and stays there, which is not what ECS does with one.
	serviceName, ok := serviceNameFromGroup(task.Group)
	if !ok {
		return
	}
	h.recordServiceTaskDeath(ctx, clusterName, serviceName, task, e.Time)
	h.scheduleServiceReplacement(ctx, region, clusterName, serviceName)
}

// retainContainerLogs snapshots the last 200 lines before the exited Docker
// container is removed. The snapshot is deliberately bounded by ContainerLogs
// and stored outside the AWS task model for emulator-only diagnostics.
func (h *Handler) retainContainerLogs(ctx context.Context, task *Task, dockerID string) {
	if !h.dockerReady.Load() || dockerID == "" {
		return
	}

	containerName := ""
	for _, container := range task.Containers {
		if container.DockerID == dockerID {
			containerName = container.Name
			break
		}
	}
	if containerName == "" {
		return
	}

	logCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	raw, err := h.docker.ContainerLogs(logCtx, dockerID, "200")
	cancel()
	if err != nil {
		h.log.Debug("ecs: capture stopped container logs",
			zap.String("container", dockerID), zap.Error(err))
	} else if aerr := h.store.putTaskContainerLogs(ctx,
		extractClusterName(task.ClusterArn), extractTaskID(task.TaskArn), containerName,
		string(stripDockerLogHeaders(raw))); aerr != nil {
		h.log.Warn("ecs: persist stopped container logs",
			zap.String("container", dockerID), zap.String("error", aerr.Message))
	}

	if h.gc != nil {
		h.gc.ScheduleRemove(dockerID)
		return
	}
	if h.cfg == nil || !h.cfg.ECSKeepContainers {
		_ = h.docker.RemoveContainerForce(dockerID)
	}
}

// containerIsOurs reports whether the container a die event is describing is
// one this Overcast created for task.
//
// A container is matched to a task by the overcast.resource-id label, and that
// label says nothing about which Overcast created it: two of them sharing a
// Docker daemon keep separate state stores, and a store restored or copied
// between them leaves both holding the same task IDs while each creates its
// own containers. The damage is not in the per-container update below, which
// already compares Docker IDs — it is in the stop decision, which asks only
// whether any recorded container is still running. A task still being placed
// has none recorded yet and satisfies that vacuously, so a foreign exit stops
// it, tears down its volumes, counts the death against the deployment and
// schedules a replacement.
//
// A task tracks a container ID per container rather than one on the record, so
// all of them are offered as the fallback ownership evidence — see
// InstanceDomain.ContainerIsOurs for what settles it.
func (h *Handler) containerIsOurs(ctx context.Context, containerID, owner string, task *Task) bool {
	recorded := make([]string, 0, len(task.Containers))
	for _, c := range task.Containers {
		recorded = append(recorded, c.DockerID)
	}
	return h.instances.ContainerIsOurs(ctx, containerID, owner, recorded...)
}

// recordContainerExit records one container's exit on its task's record, and
// stops the task when that was the last container still running. It returns the
// region the task is stored under, the stored task (nil when there is no such
// task), and whether the task itself stopped.
//
// Under lockTask, which is why it takes the lock before it knows the region:
// the key is cluster and task ID alone, so the cross-region scan that finds the
// task happens inside the lock rather than racing ahead of it. The lock is
// released before the caller touches the service, keeping the order
// service-then-task. See lockTask.
func (h *Handler) recordContainerExit(clusterName, taskID string, p events.DockerContainerPayload, occurredAt time.Time) (string, *Task, bool) {
	defer h.lockTask(clusterName, taskID)()

	// The event only carries "cluster/task" — not the region the task was
	// stored under — so locate it with a cross-region scan and pin that
	// region on the context for the write-back.
	task, region, found, err := serviceutil.FindRegioned[Task](context.Background(), h.store.store, nsTasks, p.ResourceID, h.store.defaultRegion)
	if err != nil || !found {
		return "", nil, false
	}
	ctx := middleware.ContextWithRegion(context.Background(), region)
	if !h.containerIsOurs(ctx, p.ContainerID, p.Instance, task) {
		return "", nil, false
	}

	// Update the container that died.
	for i := range task.Containers {
		if task.Containers[i].DockerID == p.ContainerID {
			task.Containers[i].LastStatus = "STOPPED"
			if exitCode, err := strconv.Atoi(p.ExitCode); err == nil {
				task.Containers[i].ExitCode = &exitCode
			}
			if p.Reason != "" {
				task.Containers[i].Reason = p.Reason
			}
			break
		}
	}

	// A task the scheduler or the caller already stopped keeps the stop code and
	// reason it was given: its container exiting is the *consequence* of that
	// decision, not a task dying on its own. Recording the exit code is all
	// that is left to do — rewriting it to EssentialContainerExited would lose
	// why it stopped, count a deliberate scale-in as a deployment failure, and
	// schedule a replacement for a task nothing is missing.
	if task.LastStatus == "STOPPED" {
		h.store.putTask(ctx, task) //nolint:errcheck
		return region, task, false
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
		if occurredAt.IsZero() {
			occurredAt = h.clk.Now()
		}
		stoppedAt := occurredAt.Unix()
		task.StoppedAt = &stoppedAt
		task.StoppingAt = &stoppedAt

		// Cancel any pending scheduler transition.
		h.scheduler.CancelScoped(region, taskID, "pending")
	}

	h.store.putTask(ctx, task) //nolint:errcheck
	return region, task, allStopped
}

// recordServiceTaskDeath takes a dead task out of its service's target groups
// and counts it against the deployment that placed it.
//
// Under lockService, because this is a read-modify-write of the whole service
// record and a reconcile of the same service runs concurrently — on the
// scheduler, on the very replacement this death is about to trigger. Unguarded,
// the reconcile's write lands on top of the increment and the failure is lost:
// the count a crash loop is measured by then undercounts, and with it the
// "unable to consistently start tasks" event and the circuit breaker.
func (h *Handler) recordServiceTaskDeath(ctx context.Context, clusterName, serviceName string, task *Task, occurredAt time.Time) {
	defer h.lockService(ctx, clusterName, serviceName)()

	svc, aerr := h.store.getService(ctx, clusterName, serviceName)
	if aerr != nil || svc == nil {
		return
	}
	// Out of the load balancer's rotation first, then replaced.
	h.deregisterTaskTargets(ctx, svc, task)
	// The deployment counts it as a failed task, which is what makes a
	// crash loop visible: failedTasks climbs, the deployment stops
	// reporting a steady state it is not in, and a circuit breaker trips.
	h.recordTaskStopFailureAt(svc, task, occurredAt)
	if aerr := h.store.putService(ctx, clusterName, svc); aerr != nil {
		h.log.Warn("ecs: failed to persist service after a task stopped",
			zap.String("cluster", clusterName),
			zap.String("service", serviceName),
			zap.String("error", aerr.Message))
	}
}
