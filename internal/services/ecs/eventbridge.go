package ecs

// eventbridge.go — bridges ECS task state transitions onto the default
// EventBridge bus, the way real ECS publishes an `ECS Task State Change`
// event every time a task's lastStatus changes.
//
// The hook lives at the store layer (ecsStore.putTask, in store.go) so it
// fires for every call site that commits a task record — RunTask's initial
// PROVISIONING write, the RUNNING transition, StopTask, the exit notifier's
// STOPPED write when a container dies on its own — with no per-handler
// wiring and no risk of a new call site forgetting to notify.
//
// Reference: https://docs.aws.amazon.com/AmazonECS/latest/developerguide/ecs_task_events.html
// AWS's own real detail carries substantially more than what is built below
// (capacityProviderName, attributes, ephemeralStorage, per-container network
// interfaces, the per-resource optimistic-concurrency "version" counter, …).
// Overcast has no truthful value for those, so — per docs/services/ecs.md's
// convention for the same trade-off in DescribeTasks — they are omitted
// rather than invented. AWS's own docs tell consumers de-serializing this
// event to tolerate unknown or absent properties, which is exactly what an
// omission is from the other side.
import (
	"context"
	"encoding/json"
	"time"

	"github.com/overcast-sh/overcast/internal/events"
)

const (
	eventBridgeECSSource              = "aws.ecs"
	eventBridgeECSTaskStateChangeType = "ECS Task State Change"
)

// ecsEventContainer is one container entry in a task state-change event's
// detail.containers, mirroring the fields Overcast's own Container tracks.
// AWS repeats the owning task's ARN on every container entry.
type ecsEventContainer struct {
	ContainerArn string `json:"containerArn"`
	TaskArn      string `json:"taskArn"`
	Name         string `json:"name"`
	Image        string `json:"image,omitempty"`
	LastStatus   string `json:"lastStatus"`
	ExitCode     *int   `json:"exitCode,omitempty"`
	Reason       string `json:"reason,omitempty"`
	RuntimeId    string `json:"runtimeId,omitempty"`
}

// ecsTaskStateChangeDetail is the "detail" object of an ECS Task State
// Change event, restricted to the fields Overcast actually tracks (see the
// package doc comment above for why the rest are omitted rather than
// fabricated). AWS documents createdAt/startedAt/stoppingAt/stoppedAt/
// updatedAt as ISO-8601 string timestamps in this event, unlike the epoch
// numbers DescribeTasks itself returns.
type ecsTaskStateChangeDetail struct {
	ClusterArn        string              `json:"clusterArn"`
	TaskArn           string              `json:"taskArn"`
	TaskDefinitionArn string              `json:"taskDefinitionArn"`
	LastStatus        string              `json:"lastStatus"`
	DesiredStatus     string              `json:"desiredStatus"`
	LaunchType        string              `json:"launchType,omitempty"`
	Cpu               string              `json:"cpu,omitempty"`
	Memory            string              `json:"memory,omitempty"`
	Group             string              `json:"group,omitempty"`
	CreatedAt         string              `json:"createdAt,omitempty"`
	StartedAt         string              `json:"startedAt,omitempty"`
	StoppingAt        string              `json:"stoppingAt,omitempty"`
	StoppedAt         string              `json:"stoppedAt,omitempty"`
	StoppedReason     string              `json:"stoppedReason,omitempty"`
	StopCode          string              `json:"stopCode,omitempty"`
	UpdatedAt         string              `json:"updatedAt,omitempty"`
	Attachments       []Attachment        `json:"attachments,omitempty"`
	Overrides         *TaskOverride       `json:"overrides,omitempty"`
	Containers        []ecsEventContainer `json:"containers,omitempty"`
}

// InitEventBridge wires the EventBridge bus publisher so ECS task state
// transitions emit onto the default bus the way real ECS does. Called once
// during router construction, after both services exist; nil (untested/wired)
// leaves notifyTaskStateChange a no-op.
func (s *Service) InitEventBridge(bus events.BusPublisher) {
	s.handler.store.eventBridge = bus
}

// epochToISO renders a stored Unix-seconds pointer as the ISO-8601 string
// AWS's own event uses, or "" when the task has not reached that point in its
// lifecycle yet (e.g. startedAt before the task has started).
func epochToISO(sec *int64) string {
	if sec == nil {
		return ""
	}
	return time.Unix(*sec, 0).UTC().Format(time.RFC3339)
}

// taskUpdatedAt reports the timestamp of the transition a task record most
// recently reflects: the moment it stopped, started, or (for a task that has
// done neither yet) was created. Overcast does not keep a dedicated
// "updatedAt" field on Task, so this recovers the same value from whichever
// stage-specific timestamp the current lastStatus corresponds to.
func taskUpdatedAt(task *Task) *int64 {
	if task.StoppedAt != nil {
		return task.StoppedAt
	}
	if task.StartedAt != nil {
		return task.StartedAt
	}
	createdAt := task.CreatedAt
	return &createdAt
}

// buildTaskStateChangeEntry renders one task as an EventBridge entry, AWS-shaped
// per the reference at the top of this file.
func buildTaskStateChangeEntry(task *Task) (events.BusEntry, bool) {
	if task == nil {
		return events.BusEntry{}, false
	}

	containers := make([]ecsEventContainer, 0, len(task.Containers))
	for _, c := range task.Containers {
		containers = append(containers, ecsEventContainer{
			ContainerArn: c.ContainerArn,
			TaskArn:      task.TaskArn,
			Name:         c.Name,
			Image:        c.Image,
			LastStatus:   c.LastStatus,
			ExitCode:     c.ExitCode,
			Reason:       c.Reason,
			RuntimeId:    c.RuntimeId,
		})
	}

	createdAt := task.CreatedAt
	detail := ecsTaskStateChangeDetail{
		ClusterArn:        task.ClusterArn,
		TaskArn:           task.TaskArn,
		TaskDefinitionArn: task.TaskDefinitionArn,
		LastStatus:        task.LastStatus,
		DesiredStatus:     task.DesiredStatus,
		LaunchType:        task.LaunchType,
		Cpu:               task.Cpu,
		Memory:            task.Memory,
		Group:             task.Group,
		CreatedAt:         epochToISO(&createdAt),
		StartedAt:         epochToISO(task.StartedAt),
		StoppingAt:        epochToISO(task.StoppingAt),
		StoppedAt:         epochToISO(task.StoppedAt),
		StoppedReason:     task.StoppedReason,
		StopCode:          task.StopCode,
		UpdatedAt:         epochToISO(taskUpdatedAt(task)),
		Attachments:       task.Attachments,
		Overrides:         task.Overrides,
		Containers:        containers,
	}

	raw, err := json.Marshal(detail)
	if err != nil {
		return events.BusEntry{}, false
	}
	return events.BusEntry{
		Source:     eventBridgeECSSource,
		DetailType: eventBridgeECSTaskStateChangeType,
		Detail:     string(raw),
		Resources:  []string{task.TaskArn},
	}, true
}

// notifyTaskStateChange emits the ECS Task State Change event whenever a
// task's lastStatus actually changed between prev and cur. prev is nil for
// the first write of a given task ARN, which is itself a real transition AWS
// notifies on — the task entering PROVISIONING for the first time — so it is
// treated as a change rather than skipped.
//
// It is a no-op when EventBridge is not wired (most unit tests) or lastStatus
// did not change.
func (s *ecsStore) notifyTaskStateChange(ctx context.Context, prev, cur *Task) {
	if s.eventBridge == nil || cur == nil {
		return
	}
	if prev != nil && prev.LastStatus == cur.LastStatus {
		return
	}

	entry, ok := buildTaskStateChangeEntry(cur)
	if !ok {
		return
	}
	// PublishBusEvent is best-effort by contract (internal/events/sink.go):
	// an error means EventBridge itself could not accept the entry, not that
	// a matching rule failed.
	_ = s.eventBridge.PublishBusEvent(ctx, entry)
}
