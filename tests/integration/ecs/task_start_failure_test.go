package ecs_test

// task_start_failure_test.go — a task that cannot start blames the container
// that could not start.
//
// ECS records CannotPullContainerError on the container whose image could not
// be pulled, and leaves every other container in the task STOPPED with no
// reason of its own. A CDK deployment that failed this way reported the same
// error and the same CDK container-asset image against all four containers,
// including an X-Ray sidecar running public.ecr.aws/xray/aws-xray-daemon —
// which made the sidecar look like the thing that was misconfigured.
//
// The failure is provoked against a registry on the loopback interface with
// nothing listening, so the pull is refused immediately without leaving the
// machine.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const (
	// unpullableImage names a registry that nothing serves, so the daemon's
	// pull fails at once rather than reaching a real one.
	unpullableImage  = "127.0.0.1:1/overcast-no-such-image:latest"
	xraySidecarImage = "public.ecr.aws/xray/aws-xray-daemon:latest"
)

func TestRunTask_withDocker_pullFailureIsNotCopiedToEveryContainer(t *testing.T) {
	skipWithoutDocker(t)

	// Given: a cluster and a two-container task definition whose first
	// container's image cannot be pulled and whose second is a sidecar.
	srv := helpers.NewTestServer(t, helpers.WithECSDocker())

	create := ecsCall(t, srv, "CreateCluster", map[string]any{"clusterName": "start-failure"})
	helpers.AssertStatus(t, create, http.StatusOK)
	create.Body.Close()

	reg := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family": "start-failure-task",
		"containerDefinitions": []map[string]any{
			{"name": "app", "image": unpullableImage},
			{"name": "xray-daemon", "image": xraySidecarImage},
		},
	})
	helpers.AssertStatus(t, reg, http.StatusOK)
	reg.Body.Close()

	// When: the task is placed.
	run := ecsCall(t, srv, "RunTask", map[string]any{
		"cluster":        "start-failure",
		"taskDefinition": "start-failure-task",
	})
	helpers.AssertStatus(t, run, http.StatusOK)
	var out struct {
		Tasks []struct {
			LastStatus    string `json:"lastStatus"`
			StopCode      string `json:"stopCode"`
			StoppedReason string `json:"stoppedReason"`
			Containers    []struct {
				Name       string `json:"name"`
				Image      string `json:"image"`
				LastStatus string `json:"lastStatus"`
				Reason     string `json:"reason"`
			} `json:"containers"`
		} `json:"tasks"`
	}
	helpers.DecodeJSON(t, run, &out)
	run.Body.Close()

	if len(out.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(out.Tasks))
	}
	task := out.Tasks[0]

	// Then: the task is stopped for a pull failure. A task that started means
	// Docker was never wired in, which this test cannot conclude anything from.
	if task.LastStatus != "STOPPED" {
		t.Fatalf("task lastStatus = %q, want STOPPED: the image should not have been pullable", task.LastStatus)
	}
	if task.StopCode != "TaskFailedToStart" {
		t.Errorf("task stopCode = %q, want TaskFailedToStart", task.StopCode)
	}
	if !strings.HasPrefix(task.StoppedReason, "CannotPullContainerError: ") {
		t.Errorf("task stoppedReason = %q, want a CannotPullContainerError", task.StoppedReason)
	}

	if len(task.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(task.Containers))
	}
	app, sidecar := task.Containers[0], task.Containers[1]

	// Then: the reason is on the container whose image could not be pulled.
	if !strings.HasPrefix(app.Reason, "CannotPullContainerError: ") || !strings.Contains(app.Reason, unpullableImage) {
		t.Errorf("app container reason = %q, want a CannotPullContainerError naming %s", app.Reason, unpullableImage)
	}

	// Then: the sidecar carries no reason and still reports its own image, so
	// nothing suggests it was the container that failed.
	if sidecar.Reason != "" {
		t.Errorf("xray-daemon container reason = %q, want none: it did not fail to start", sidecar.Reason)
	}
	if sidecar.LastStatus != "STOPPED" {
		t.Errorf("xray-daemon container lastStatus = %q, want STOPPED", sidecar.LastStatus)
	}
	if sidecar.Image != xraySidecarImage {
		t.Errorf("xray-daemon container image = %q, want %q", sidecar.Image, xraySidecarImage)
	}
}
