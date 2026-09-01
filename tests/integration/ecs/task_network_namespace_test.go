package ecs_test

// task_network_namespace_test.go — against a real daemon, `127.0.0.1` reaches
// across an awsvpc task.
//
// The unit tests pin the request shapes Overcast sends; only a real daemon can
// say whether the containers those shapes produce actually share a namespace.
// The task here proves it from the inside: one container serves on
// `127.0.0.1:9000` and the other fetches from it, exiting non-zero if it cannot
// — so the assertion is an exit code the ECS API itself reports, not something
// read out of Docker behind the emulator's back.
//
// This is the shape the ECS sidecar pattern relies on and that a task
// definition written for Fargate assumes: nginx to php-fpm on
// `127.0.0.1:9000`, an application to its X-Ray daemon on `127.0.0.1:2000`.

import (
	"net/http"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// loopbackProbeImage is the image both containers run. It is the same one the
// namespace container itself uses, so the task needs no pull the placement did
// not already make.
const loopbackProbeImage = "busybox:1.36"

func TestRunTask_withDocker_awsvpcContainersShareOneNetworkNamespace(t *testing.T) {
	skipWithoutDocker(t)

	// Given: an awsvpc task of two containers that talk to each other over
	// loopback, exactly as they would on Fargate.
	srv := helpers.NewTestServer(t, helpers.WithECSDocker())
	waitForECSDocker(t, srv)

	create := ecsCall(t, srv, "CreateCluster", map[string]any{"clusterName": "netns"})
	helpers.AssertStatus(t, create, http.StatusOK)
	create.Body.Close()

	reg := ecsCall(t, srv, "RegisterTaskDefinition", map[string]any{
		"family":                  "loopback-task",
		"networkMode":             "awsvpc",
		"requiresCompatibilities": []string{"FARGATE"},
		"cpu":                     "256",
		"memory":                  "512",
		"containerDefinitions": []map[string]any{
			{
				"name":         "backend",
				"image":        loopbackProbeImage,
				"portMappings": []map[string]any{{"containerPort": 9000, "hostPort": 9000}},
				"entryPoint":   []string{"/bin/sh", "-c"},
				"command":      []string{"mkdir -p /srv && echo pong > /srv/ping && httpd -f -p 9000 -h /srv"},
			},
			{
				// Fetches from its sibling and reports the answer as its exit
				// code: 0 only if loopback crossed the container boundary.
				"name":       "probe",
				"image":      loopbackProbeImage,
				"entryPoint": []string{"/bin/sh", "-c"},
				"command": []string{
					"for i in 1 2 3 4 5 6 7 8 9 10; do " +
						"wget -q -O - -T 2 http://127.0.0.1:9000/ping 2>/dev/null | grep -q pong && exit 0; " +
						"sleep 1; done; exit 1",
				},
			},
		},
	})
	helpers.AssertStatus(t, reg, http.StatusOK)
	reg.Body.Close()

	// When: the task is placed.
	run := ecsCall(t, srv, "RunTask", map[string]any{
		"cluster":        "netns",
		"taskDefinition": "loopback-task",
		"launchType":     "FARGATE",
		"networkConfiguration": map[string]any{
			"awsvpcConfiguration": map[string]any{"subnets": []string{"subnet-loopback"}},
		},
	})
	helpers.AssertStatus(t, run, http.StatusOK)
	var placed struct {
		Tasks []struct {
			TaskArn       string `json:"taskArn"`
			LastStatus    string `json:"lastStatus"`
			StoppedReason string `json:"stoppedReason"`
		} `json:"tasks"`
	}
	helpers.DecodeJSON(t, run, &placed)
	run.Body.Close()

	if len(placed.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(placed.Tasks))
	}
	if placed.Tasks[0].LastStatus == "STOPPED" {
		t.Fatalf("task failed to start: %s", placed.Tasks[0].StoppedReason)
	}
	taskArn := placed.Tasks[0].TaskArn

	// Then: the probe container exits 0, which it can only do by reaching its
	// sibling on 127.0.0.1. A task whose containers each held a namespace of
	// their own leaves it exiting 1 — nothing is listening on the probe's own
	// loopback, so the connection is refused.
	var exitCode *int
	var reason string
	helpers.Eventually(t, 90*time.Second, 250*time.Millisecond, func() bool {
		desc := ecsCall(t, srv, "DescribeTasks", map[string]any{
			"cluster": "netns", "tasks": []string{taskArn},
		})
		defer desc.Body.Close()
		var out struct {
			Tasks []struct {
				Containers []struct {
					Name     string `json:"name"`
					ExitCode *int   `json:"exitCode"`
					Reason   string `json:"reason"`
				} `json:"containers"`
			} `json:"tasks"`
		}
		helpers.DecodeJSON(t, desc, &out)
		if len(out.Tasks) != 1 {
			return false
		}
		for _, c := range out.Tasks[0].Containers {
			if c.Name == "probe" && c.ExitCode != nil {
				exitCode, reason = c.ExitCode, c.Reason
				return true
			}
		}
		return false
	}, "the probe container never finished, so nothing was learned about the task's network namespace")

	if *exitCode != 0 {
		t.Errorf("probe container exited %d (%s): 127.0.0.1:9000 did not reach the other container in the same task",
			*exitCode, reason)
	}
}
