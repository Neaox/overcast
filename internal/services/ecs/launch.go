package ecs

// launch.go — the single path by which a task is placed.
//
// RunTask and the service reconciler both come through here, so a task started
// by a service is the same object, started the same way, as one started
// directly: real containers when Docker is wired, the same awsvpc ENI
// attachment, and the same failure shape when it cannot be placed.

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// awsvpcPlacement is the resolved result of an awsvpc networkConfiguration:
// which subnet the task lands in and which Docker network backs its VPC.
type awsvpcPlacement struct {
	subnetID       string
	networkID      string
	subnetResolved bool
}

// taskLaunchSpec describes one task about to be placed.
type taskLaunchSpec struct {
	clusterName     string
	clusterArn      string
	td              *TaskDefinition
	launchType      string
	platformVersion string
	group           string
	startedBy       string
	overrides       *TaskOverride
	netCfg          *NetworkConfiguration
	placement       awsvpcPlacement
	// ordinal distinguishes tasks placed in the same batch when EC2 cannot
	// allocate a private IP and the synthetic fallback is used.
	ordinal int
}

// launchTask builds one task from a task definition and places it, starting
// real containers whenever Docker is wired. The task is persisted either way,
// and is returned even when the launch failed so the caller can report it.
//
// A task whose containers cannot be started is persisted STOPPED with stopCode
// TaskFailedToStart, which is what AWS does — it is never reported RUNNING.
// The metadata-only path, where a task goes RUNNING with nothing behind it, is
// reserved for Overcast running without a container runtime at all: that is a
// deployment mode, not a task that failed.
func (h *Handler) launchTask(ctx context.Context, spec taskLaunchSpec) (*Task, *protocol.AWSError, error) {
	taskID := uuid.New().String()
	td := spec.td

	containers := make([]Container, 0, len(td.ContainerDefinitions))
	for _, cd := range td.ContainerDefinitions {
		containers = append(containers, Container{
			ContainerArn: h.containerARN(ctx, uuid.New().String()),
			Name:         cd.Name,
			Image:        cd.Image,
			LastStatus:   "PENDING",
		})
	}

	task := &Task{
		TaskArn:              h.taskARN(ctx, spec.clusterName, taskID),
		TaskDefinitionArn:    td.TaskDefinitionArn,
		ClusterArn:           spec.clusterArn,
		LastStatus:           "PROVISIONING",
		DesiredStatus:        "RUNNING",
		LaunchType:           spec.launchType,
		Cpu:                  td.Cpu,
		Memory:               td.Memory,
		PlatformVersion:      spec.platformVersion,
		CreatedAt:            h.clk.Now().Unix(),
		Group:                spec.group,
		StartedBy:            spec.startedBy,
		Containers:           containers,
		Overrides:            spec.overrides,
		NetworkConfiguration: spec.netCfg,
		Attachments:          h.awsvpcAttachment(ctx, spec, taskID),
	}

	startErr := error(nil)
	if h.dockerReady.Load() {
		startErr = h.startTaskContainers(ctx, task, td, spec.clusterName, taskID, spec.placement.networkID)
		if startErr != nil {
			h.log.Warn("ecs: task failed to start",
				zap.String("cluster", spec.clusterName),
				zap.String("task", taskID),
				zap.Error(startErr))
			markTaskFailedToStart(task, h.clk.Now().Unix(), startErr)
		}
	} else {
		h.scheduleRunningTransition(h.store.region(ctx), spec.clusterName, taskID)
	}

	if aerr := h.store.putTask(ctx, task); aerr != nil {
		return nil, aerr, nil
	}
	if startErr != nil {
		if h.bus != nil {
			h.bus.Publish(ctx, events.Event{
				Type:    events.ECSTaskStartFailed,
				Payload: events.ResourcePayload{Name: taskID},
			})
		}
		return task, nil, startErr
	}
	return task, nil, nil
}

// awsvpcAttachment builds the ENI attachment AWS reports on an awsvpc task.
func (h *Handler) awsvpcAttachment(ctx context.Context, spec taskLaunchSpec, taskID string) []Attachment {
	if spec.netCfg == nil {
		return nil
	}
	i := spec.ordinal
	privateIP := "10.0." + fmt.Sprintf("%d.%d", (i+1)/256, (i+1)%256)
	if spec.placement.subnetResolved && h.vpcResolver != nil {
		if translated := h.vpcResolver.AllocatePrivateIPForSubnet(ctx, spec.placement.subnetID); translated != "" {
			privateIP = translated
		}
	}
	return []Attachment{{
		Id:     uuid.New().String(),
		Type:   "ElasticNetworkInterface",
		Status: "ATTACHING",
		Details: []KeyValuePair{
			{Name: "networkInterfaceId", Value: "eni-" + taskID[:8]},
			{Name: "subnetId", Value: spec.placement.subnetID},
			{Name: "privateIPv4Address", Value: privateIP},
		},
	}}
}

// markTaskFailedToStart records a task that could not be placed in the shape
// AWS uses for one: STOPPED, never RUNNING, carrying the stopCode a caller
// would switch on and a reason naming what actually went wrong.
func markTaskFailedToStart(task *Task, now int64, err error) {
	reason := stoppedReasonFor(err)
	task.LastStatus = "STOPPED"
	task.DesiredStatus = "STOPPED"
	task.StopCode = "TaskFailedToStart"
	task.StoppedReason = reason
	task.StoppedAt = &now
	task.StoppingAt = &now
	for i := range task.Containers {
		task.Containers[i].LastStatus = "STOPPED"
		task.Containers[i].Reason = reason
	}
}

// stoppedReasonFor prefixes a container start failure with the AWS stopped-task
// error code covering it, so the reason a user reads here names the same
// condition it would on real ECS.
func stoppedReasonFor(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "pull image"):
		return "CannotPullContainerError: " + msg
	case strings.Contains(msg, "create container"), strings.Contains(msg, "start container"):
		return "CannotStartContainerError: " + msg
	case strings.Contains(msg, "network"):
		return "ResourceInitializationError: " + msg
	default:
		return msg
	}
}
