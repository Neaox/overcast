package ecs

// launch_unwind_test.go — a task that could not be placed leaves nothing
// running behind it.
//
// startTaskContainers used to remove only the container it was working on when
// a step failed; the ones it had already created and started in earlier
// iterations stayed. Nothing downstream picked them up — launchTask persists
// the task STOPPED, so no later pass walks its containers — and they held their
// ports and their memory until the GC's shutdown sweep, all while DescribeTasks
// reported every container in the task STOPPED. A four-container task whose
// third image will not pull left the first two running; a service crash-looping
// on that image leaked a fresh pair on every replacement attempt.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/middleware"
)

func TestRunTask_partialPlacementUnwindsTheContainersAlreadyStarted(t *testing.T) {
	// The output the container that did come up wrote before the placement
	// failed around it — the reason the unwind captures before it removes.
	const appOutput = "server listening on :8080\n"

	// Given: a three-container task on a daemon that refuses to start the
	// second one, the way it refuses a container whose port is already taken.
	h, _, fd := newECSDockerTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	fd.failStartOf("sidecar")
	fd.setOnStart(func(id string) { fd.setContainerLogs(id, framed(1, appOutput)) })

	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "demo"}); w.Code != http.StatusOK {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family": "web",
		"containerDefinitions": []map[string]any{
			{"name": "app", "image": "app:1"},
			{"name": "sidecar", "image": "sidecar:1"},
			{"name": "metrics", "image": "metrics:1"},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}

	// When: the task is placed.
	w := postJSON(t, ctx, h.RunTask, map[string]any{
		"cluster":        "demo",
		"taskDefinition": "web:1",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("RunTask: HTTP %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode RunTask response: %v", err)
	}
	if len(out.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(out.Tasks))
	}
	task := out.Tasks[0]

	// Then: the task failed to start, blaming the container that could not.
	if task.LastStatus != "STOPPED" || task.StopCode != "TaskFailedToStart" {
		t.Fatalf("task lastStatus=%q stopCode=%q, want STOPPED/TaskFailedToStart", task.LastStatus, task.StopCode)
	}

	// And: placement stopped at the container that failed — the one after it
	// was never created, so there is a partial placement to unwind at all.
	appID, sidecarID := fd.containerIDOf("app"), fd.containerIDOf("sidecar")
	if appID == "" || sidecarID == "" {
		t.Fatalf("app=%q sidecar=%q; both should have been created", appID, sidecarID)
	}
	if id := fd.containerIDOf("metrics"); id != "" {
		t.Fatalf("metrics container %q was created after the placement had already failed", id)
	}
	if n := fd.startedCount(); n != 1 {
		t.Fatalf("started %d containers, want 1 (only app)", n)
	}

	// And: neither is still on the daemon. The app container is the regression:
	// it started, and nothing but this unwind is ever going to remove it.
	if !fd.wasRemoved(appID) {
		t.Errorf("container app (%s) started and was left behind by a task that failed to start", appID)
	}
	if !fd.wasRemoved(sidecarID) {
		t.Errorf("container sidecar (%s) was left behind", sidecarID)
	}

	// And: the app container's last words were captured before it went, which
	// is the whole reason the unwind cannot simply force-remove.
	taskID := extractTaskID(task.TaskArn)
	logs, found, aerr := h.store.getTaskContainerLogs(ctx, "demo", taskID, "app")
	if aerr != nil {
		t.Fatalf("get retained logs: %s", aerr.Message)
	}
	if !found || logs.Logs != appOutput {
		t.Errorf("retained app logs = %q, found %v; want %q", logs.Logs, found, appOutput)
	}

	// And: it is readable where somebody would go looking for it. The endpoint
	// will not serve a container it cannot see an ID for, retained copy
	// included, so the unwind has to leave the ID on the record even though the
	// container it names is gone.
	if body := readTaskContainerLogs(t, h, ctx, taskID, "app"); body.Logs != appOutput {
		t.Errorf("task log endpoint served %q from %q, want %q", body.Logs, body.LogSource, appOutput)
	}
}

// An awsvpc task has one container more than it declares — the namespace the
// rest run inside — and it is retired by its own defer in startTaskContainers
// rather than by the unwind. The two have to compose: everything the placement
// put on the daemon goes, the application containers before the namespace they
// were running in.
func TestRunTask_partialPlacementUnwindsAnAwsvpcTaskIncludingItsNamespace(t *testing.T) {
	// Given: a two-container Fargate task whose second container will not start.
	h, _, fd := newECSDockerTestHandler(t)
	fd.failStartOf("php-fpm")

	// When: it is placed.
	created := runFargateTask(t, h, fd)

	// Then: all three containers were created — the namespace first, as always.
	if len(created) != 3 {
		t.Fatalf("created %d containers, want 3 (the namespace container and two application containers)", len(created))
	}
	if !strings.HasSuffix(created[0].name, taskNamespaceContainerSuffix) {
		t.Fatalf("first container created is %q, want the task's namespace container", created[0].name)
	}

	// And: none of them is still on the daemon. The namespace is the one no
	// application container's exit would ever have taken down.
	for _, c := range created {
		if !fd.wasRemoved(c.id) {
			t.Errorf("container %q (%s) was left behind by a task that failed to start", c.name, c.id)
		}
	}
}

// A daemon kept for post-mortem inspection is the one case where the containers
// of a failed placement are meant to survive it: removing them takes away the
// only thing ECS_KEEP_CONTAINERS was set to look at. Every other teardown path
// honours the flag, and a launch failure is the post-mortem it is usually set
// for.
func TestRunTask_partialPlacementKeepsContainersWhenAskedTo(t *testing.T) {
	h, _, fd := newECSDockerTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	h.cfg.ECSKeepContainers = true
	fd.failStartOf("sidecar")

	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "demo"}); w.Code != http.StatusOK {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family": "web",
		"containerDefinitions": []map[string]any{
			{"name": "app", "image": "app:1"},
			{"name": "sidecar", "image": "sidecar:1"},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}

	if w := postJSON(t, ctx, h.RunTask, map[string]any{
		"cluster":        "demo",
		"taskDefinition": "web:1",
	}); w.Code != http.StatusOK {
		t.Fatalf("RunTask: HTTP %d: %s", w.Code, w.Body.String())
	}

	appID, sidecarID := fd.containerIDOf("app"), fd.containerIDOf("sidecar")
	if appID == "" || sidecarID == "" {
		t.Fatalf("app=%q sidecar=%q; both should have been created", appID, sidecarID)
	}
	if fd.wasRemoved(appID) {
		t.Errorf("container app (%s) was removed despite ECS_KEEP_CONTAINERS", appID)
	}
	// The container that failed too: it is the one the reader came for, and the
	// per-failure removal it used to get took it away regardless of the flag.
	if fd.wasRemoved(sidecarID) {
		t.Errorf("container sidecar (%s) was removed despite ECS_KEEP_CONTAINERS", sidecarID)
	}
}
