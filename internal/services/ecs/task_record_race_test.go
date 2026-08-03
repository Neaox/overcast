package ecs

// task_record_race_test.go — a task that has been stopped stays stopped, even
// when its PROVISIONING → RUNNING transition fires at the same moment.
//
// A task is stored as one JSON blob, so every writer reads the whole record,
// edits it and writes the whole record back. Cancelling the pending transition
// before writing STOPPED is not enough on its own: cancelling only stops a
// timer that has not fired, and a transition already inside its callback has
// read the task as PROVISIONING. Its write of RUNNING then lands after the
// STOPPED one and brings the task back to life — a scale-in that reports no
// stopped task, a superseded deployment that never drains.
//
// The LastStatus guard in the transition narrows the window; it cannot close
// it, because the read and the write are not one step.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
)

// stopper is one of the ways a task is taken to STOPPED while it is still
// coming up. Every one of them races the same transition.
//
// The service scheduler and the exit notifier lose that race on the first round
// without the lock. StopTask has more to do before it writes — a describe, the
// service's target groups — so in practice the transition is nearly always past
// its own write by then, and this asserts the invariant on that path rather
// than reproducing a failure on it. It is the same read-modify-write, and it
// takes the same lock.
type stopper struct {
	name string
	stop func(t *testing.T, h *Handler, ctx context.Context, svc *ecsService, cluster string, task Task)
}

func TestTaskStopOutlastsARunningTransition(t *testing.T) {
	stoppers := []stopper{{
		name: "service scheduler",
		stop: func(_ *testing.T, h *Handler, ctx context.Context, svc *ecsService, cluster string, task Task) {
			h.stopServiceTasks(ctx, cluster, svc, []Task{task}, stopReasonScaling)
		},
	}, {
		name: "StopTask",
		stop: func(t *testing.T, h *Handler, ctx context.Context, _ *ecsService, cluster string, task Task) {
			w := postJSON(t, ctx, h.StopTask, map[string]any{
				"cluster": cluster, "task": task.TaskArn, "reason": "by hand",
			})
			if w.Code != 200 {
				t.Errorf("StopTask: HTTP %d: %s", w.Code, w.Body.String())
			}
		},
	}, {
		name: "container exit",
		stop: func(_ *testing.T, h *Handler, ctx context.Context, _ *ecsService, cluster string, task Task) {
			h.handleContainerDied(ctx, containerDiedEvent(cluster, extractTaskID(task.TaskArn), task.Containers[0].DockerID))
		},
	}}

	for _, s := range stoppers {
		t.Run(s.name, func(t *testing.T) {
			// Given: a service, and tasks of it that are still PROVISIONING
			h, _ := newECSRegionTestHandler(t)
			ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
			const cluster, service = "race-cluster", "race-svc"
			svc := createRaceService(t, h, ctx, cluster, service)

			// When: each task is stopped while its transition to RUNNING fires
			//
			// One round is a coin toss, so this takes enough of them that
			// losing the race even once is overwhelmingly likely.
			const rounds = 200
			for i := range rounds {
				task := putProvisioningTask(t, h, ctx, cluster, service, fmt.Sprintf("race-task-%03d", i))

				var wg sync.WaitGroup
				start := make(chan struct{})
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					h.applyRunningTransition(ctx, cluster, extractTaskID(task.TaskArn))
				}()
				go func() {
					defer wg.Done()
					<-start
					s.stop(t, h, ctx, svc, cluster, task)
				}()
				close(start)
				wg.Wait()

				// Then: the stop stands — none of them was overwritten by the
				// transition's own write
				got, aerr := h.store.getTask(ctx, cluster, extractTaskID(task.TaskArn))
				if aerr != nil || got == nil {
					t.Fatalf("getTask: %v", aerr)
				}
				if got.LastStatus != "STOPPED" {
					t.Fatalf("round %d: task is %s, want STOPPED — the transition's write landed on top of the stop",
						i, got.LastStatus)
				}
			}
		})
	}
}

// ---- Test helpers ----------------------------------------------------------

// createRaceService creates the cluster, task definition and service the raced
// tasks belong to, at desiredCount 0 so the scheduler places nothing of its own.
func createRaceService(t *testing.T, h *Handler, ctx context.Context, cluster, service string) *ecsService {
	t.Helper()
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
		"taskDefinition": service + "-td:1", "desiredCount": 0,
	}); w.Code != 200 {
		t.Fatalf("CreateService: HTTP %d: %s", w.Code, w.Body.String())
	}
	svc, aerr := h.store.getService(ctx, cluster, service)
	if aerr != nil || svc == nil {
		t.Fatalf("getService: %v", aerr)
	}
	return svc
}

// containerDiedEvent is the Docker die event the exit notifier subscribes to.
func containerDiedEvent(cluster, taskID, dockerID string) events.Event {
	return events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			Service:     "ecs",
			ResourceID:  cluster + "/" + taskID,
			ContainerID: dockerID,
			ExitCode:    "7",
		},
	}
}

// putProvisioningTask stores a task of the service in the state a placement
// leaves it in: PROVISIONING, with its transition to RUNNING still to come.
func putProvisioningTask(t *testing.T, h *Handler, ctx context.Context, cluster, service, taskID string) Task {
	t.Helper()
	task := Task{
		TaskArn:       h.taskARN(ctx, cluster, taskID),
		ClusterArn:    h.clusterARN(ctx, cluster),
		Group:         serviceGroupPrefix + service,
		LastStatus:    "PROVISIONING",
		DesiredStatus: "RUNNING",
		Containers: []Container{{
			Name:       "app",
			DockerID:   "docker-" + taskID,
			LastStatus: "PENDING",
		}},
	}
	if aerr := h.store.putTask(ctx, &task); aerr != nil {
		t.Fatalf("putTask: %v", aerr)
	}
	return task
}
