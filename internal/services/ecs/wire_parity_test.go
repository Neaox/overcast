package ecs

// wire_parity_test.go — the JSON 1.1 and RPC v2 CBOR paths take the same
// fields and do the same things with them.
//
// CreateService, RunTask and RegisterTaskDefinition are each implemented twice,
// and the two drifted before: a field added to one path was silently dropped on
// the other, so the same call behaved differently depending on which protocol
// the caller's SDK happened to speak. Newer SDK releases move services onto
// CBOR, which makes that a bug users hit by upgrading rather than by changing
// their code.
//
// Field parity is checked by reflection below. Behaviour parity is not
// reflectable, so the one operation whose ordering has teeth — StopTask, where
// the order of the record and the teardown decides what a stopped task is
// reported as — is pinned by running both paths through the same scenario.

import (
	"context"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/Neaox/overcast/internal/middleware"
)

// jsonFields returns the json tag names a request struct accepts.
func jsonFields(t *testing.T, v any) []string {
	t.Helper()
	rt := reflect.TypeOf(v)
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		t.Fatalf("expected a struct, got %s", rt.Kind())
	}
	names := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		tag := rt.Field(i).Tag.Get("json")
		for j := 0; j < len(tag); j++ {
			if tag[j] == ',' {
				tag = tag[:j]
				break
			}
		}
		if tag != "" && tag != "-" {
			names = append(names, tag)
		}
	}
	sort.Strings(names)
	return names
}

// assertAccepts fails when the typed request is missing a field the legacy one
// accepts. Extra fields on the typed side are fine — it may model more.
func assertAccepts(t *testing.T, operation string, legacy, typed []string) {
	t.Helper()
	have := make(map[string]bool, len(typed))
	for _, f := range typed {
		have[f] = true
	}
	var missing []string
	for _, f := range legacy {
		if !have[f] {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%s: the CBOR path drops fields the JSON path accepts: %v\n"+
			"Add them to the typed request struct in typed_logic.go, or the same call "+
			"behaves differently depending on the caller's SDK.", operation, missing)
	}
}

func TestWireParity_createService(t *testing.T) {
	// The legacy handler declares its request inline, so the field list is
	// restated here; the test's value is catching a field added to one path and
	// not the other, which this comparison still does.
	legacy := []string{
		"cluster", "serviceName", "taskDefinition", "desiredCount", "launchType",
		"schedulingStrategy", "networkConfiguration", "deploymentController",
		"deploymentConfiguration", "capacityProviderStrategy", "loadBalancers",
		"platformVersion", "healthCheckGracePeriodSeconds", "enableExecuteCommand",
		"propagateTags", "serviceRegistries", "placementStrategy", "placementConstraints",
	}
	sort.Strings(legacy)
	assertAccepts(t, "CreateService", legacy, jsonFields(t, createServiceRequest{}))
}

func TestWireParity_runTask(t *testing.T) {
	legacy := []string{
		"cluster", "taskDefinition", "count", "launchType", "networkConfiguration",
		"platformVersion", "overrides", "group", "startedBy",
	}
	sort.Strings(legacy)
	assertAccepts(t, "RunTask", legacy, jsonFields(t, runTaskRequest{}))
}

func TestWireParity_registerTaskDefinition(t *testing.T) {
	legacy := []string{
		"family", "containerDefinitions", "networkMode", "requiresCompatibilities",
		"cpu", "memory", "volumes", "taskRoleArn", "executionRoleArn",
		"runtimePlatform", "ephemeralStorage", "pidMode", "ipcMode",
	}
	sort.Strings(legacy)
	assertAccepts(t, "RegisterTaskDefinition", legacy, jsonFields(t, registerTaskDefinitionRequest{}))
}

// StopTask records the stop before it kills the containers, on both paths.
//
// Killing a container raises a Docker die event, and the handler for it stops
// the task itself with the code for a task that died on its own. Whichever of
// the two writes lands second wins, so a stop recorded after the kill is a stop
// that can be lost: the task comes back as EssentialContainerExited rather than
// UserInitiated, and because that is what a task dying unbidden looks like, the
// deployment that placed it is charged a failed task and a replacement nobody
// asked for is scheduled.
//
// The CBOR path had exactly that order. It is the ordering, not the wording,
// that is the subject here — an implementation that stops the containers first
// passes no version of this test, however carefully it sets the fields
// afterwards.
func TestWireParity_stopTaskRecordsTheStopBeforeTheKill(t *testing.T) {
	const cluster, service = "parity-cluster", "parity-svc"
	const taskID, containerID = "parity-task", "parity-container"

	paths := []struct {
		name string
		stop func(t *testing.T, h *Handler, ctx context.Context, task *Task)
	}{{
		name: "JSON",
		stop: func(t *testing.T, h *Handler, ctx context.Context, task *Task) {
			w := postJSON(t, ctx, h.StopTask, map[string]any{
				"cluster": cluster, "task": task.TaskArn, "reason": "by hand",
			})
			if w.Code != http.StatusOK {
				t.Fatalf("StopTask: HTTP %d: %s", w.Code, w.Body.String())
			}
		},
	}, {
		name: "CBOR",
		stop: func(t *testing.T, h *Handler, ctx context.Context, task *Task) {
			if _, aerr := h.stopTaskTyped(ctx, &stopTaskRequest{
				Cluster: cluster, Task: task.TaskArn, Reason: "by hand",
			}); aerr != nil {
				t.Fatalf("stopTaskTyped: %s", aerr.Message)
			}
		},
	}}

	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			// Given: a running task of a service, and a daemon that raises the
			// die event the instant the container is killed
			h, _, fd := newECSDockerTestHandler(t)
			ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
			svc := createRaceService(t, h, ctx, cluster, service)
			task := putRunningServiceTask(t, h, ctx, cluster, service, taskID, containerID, primaryDeploymentID(t, svc))
			fd.setOnStop(func(id string) {
				h.handleContainerDied(ctx, containerDiedEvent(cluster, taskID, id))
			})

			// When: the caller stops it
			p.stop(t, h, ctx, task)

			// Then: it is recorded as the caller's doing, not the container's
			got, aerr := h.store.getTask(ctx, cluster, taskID)
			if aerr != nil || got == nil {
				t.Fatalf("getTask: %v", aerr)
			}
			if got.StopCode != "UserInitiated" || got.StoppedReason != "by hand" {
				t.Errorf("task stopped as %s/%q, want UserInitiated/%q — the die event's "+
					"write landed on top of the stop, so the containers were killed before "+
					"the stop was recorded",
					got.StopCode, got.StoppedReason, "by hand")
			}

			// And: the deployment was not charged for a task the caller retired
			after, aerr := h.store.getService(ctx, cluster, service)
			if aerr != nil || after == nil {
				t.Fatalf("getService: %v", aerr)
			}
			if d := primaryDeployment(after); d != nil && d.FailedTasks != 0 {
				t.Errorf("deployment failedTasks = %d, want 0 — a stop the caller asked "+
					"for was counted as a task that died, which trips the circuit breaker "+
					"on a service nothing is wrong with", d.FailedTasks)
			}
		})
	}
}

// primaryDeploymentID returns the service's primary deployment ID, which a task
// must carry as startedBy for its death to be counted against that deployment.
// A task without it would make the failure assertion above vacuous.
func primaryDeploymentID(t *testing.T, svc *ecsService) string {
	t.Helper()
	d := primaryDeployment(svc)
	if d == nil {
		t.Fatal("service has no primary deployment")
	}
	return d.ID
}

// putRunningServiceTask stores a RUNNING task of the service with one container
// the fake daemon will answer for.
func putRunningServiceTask(t *testing.T, h *Handler, ctx context.Context, cluster, service, taskID, containerID, startedBy string) *Task {
	t.Helper()
	task := &Task{
		TaskArn:       h.taskARN(ctx, cluster, taskID),
		ClusterArn:    h.clusterARN(ctx, cluster),
		Group:         serviceGroupPrefix + service,
		StartedBy:     startedBy,
		LastStatus:    "RUNNING",
		DesiredStatus: "RUNNING",
		Containers: []Container{{
			Name:       "app",
			DockerID:   containerID,
			LastStatus: "RUNNING",
		}},
	}
	if aerr := h.store.putTask(ctx, task); aerr != nil {
		t.Fatalf("putTask: %s", aerr.Message)
	}
	return task
}
