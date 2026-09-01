package ecs

// service_teardown_leak_test.go — a drained service leaves no container behind,
// namespace container included, by the time the drain returns.
//
// This is the leak the compat runner's post-run audit kept reporting: every
// suite's ecs-services scenario left exactly its tasks' `internal.ecs.pause`
// containers on the daemon, and never their application containers. The
// asymmetry is the bug's shape. The scale-down path (stopServiceTasks →
// retireTaskContainers) stops and removes each application container inline,
// before the API call that triggered it returns — but handed the namespace
// container to the container GC's background queue. In a compat run the whole
// suite finishes in seconds, so the runner's `docker ps --all` fired before the
// GC's remove loop got there, and called the still-present pause containers
// leaked.
//
// The teardown paths that are already synchronous for the application
// containers must be synchronous for the namespace container too: it is one
// more Docker call on a path that already makes two per application container,
// and it makes "the drain returned" mean the task is actually gone.

import (
	"context"
	"strings"
	"testing"
)

// placeOneTaskService creates an awsvpc service of one single-container task
// and returns the Docker IDs of the task's namespace and application
// containers, placed inline by CreateService's own reconcile.
func placeOneTaskService(t *testing.T, h *Handler, fd *fakeECSDockerDaemon) (namespaceID, appID string) {
	t.Helper()
	ctx := context.Background()
	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "c1"}); w.Code != 200 {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family":                  "web",
		"networkMode":             "awsvpc",
		"requiresCompatibilities": []string{"FARGATE"},
		"cpu":                     "256",
		"memory":                  "512",
		"containerDefinitions":    []map[string]any{{"name": "app", "image": "nginx"}},
	}); w.Code != 200 {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.CreateService, map[string]any{
		"cluster":        "c1",
		"serviceName":    "svc1",
		"taskDefinition": "web",
		"desiredCount":   1,
		"launchType":     "FARGATE",
		"networkConfiguration": map[string]any{
			"awsvpcConfiguration": map[string]any{"subnets": []string{"subnet-1"}},
		},
	}); w.Code != 200 {
		t.Fatalf("CreateService: HTTP %d: %s", w.Code, w.Body.String())
	}

	created := fd.createdContainers()
	if len(created) != 2 {
		t.Fatalf("created %d containers, want 2 (the namespace container and the application container)", len(created))
	}
	if !strings.HasSuffix(created[0].name, taskNamespaceContainerSuffix) {
		t.Fatalf("first container created is %q, want the task's namespace container", created[0].name)
	}
	return created[0].id, created[1].id
}

func TestServiceScaleDown_removesTheNamespaceContainerBeforeReturning(t *testing.T) {
	// Given: a service of one placed awsvpc task, on a handler whose container
	// GC is wired but has not reached its queue — the state of a busy emulator,
	// and of every emulator in the seconds after a delete.
	h, _, fd := newECSDockerTestHandler(t)
	wireStalledGC(t, h, fd)
	namespaceID, appID := placeOneTaskService(t, h, fd)

	// When: the service drains to zero — the first half of the compat suites'
	// teardown — and the call returns.
	if w := postJSON(t, context.Background(), h.UpdateService, map[string]any{
		"cluster": "c1", "service": "svc1", "desiredCount": 0,
	}); w.Code != 200 {
		t.Fatalf("UpdateService: HTTP %d: %s", w.Code, w.Body.String())
	}

	// Then: the task's containers are gone from the daemon — both of them. The
	// scale-down already removes the application container before returning;
	// leaving the namespace container to the GC's background queue is how every
	// compat run ended with each service task's pause container reported leaked.
	if !fd.wasRemoved(appID) {
		t.Error("the task's application container is still on the daemon after the scale-down returned")
	}
	if !fd.wasRemoved(namespaceID) {
		t.Error("the task's network namespace container is still on the daemon after the scale-down returned — it must come down with the application containers, not wait for the GC")
	}
}

func TestDeleteService_removesTheNamespaceContainerBeforeReturning(t *testing.T) {
	// Given: the same placed service, drained the way the compat post-run sweep
	// does it — DeleteService directly, without a scale-down first.
	h, _, fd := newECSDockerTestHandler(t)
	wireStalledGC(t, h, fd)
	namespaceID, _ := placeOneTaskService(t, h, fd)

	// When: the service is deleted.
	if w := postJSON(t, context.Background(), h.DeleteService, map[string]any{
		"cluster": "c1", "service": "svc1",
	}); w.Code != 200 {
		t.Fatalf("DeleteService: HTTP %d: %s", w.Code, w.Body.String())
	}

	// Then: nothing of the task is left on the daemon when the delete returns.
	if !fd.wasRemoved(namespaceID) {
		t.Error("the task's network namespace container outlived DeleteService")
	}
}

func TestFailedLaunch_removesTheNamespaceContainerBeforeReturning(t *testing.T) {
	// Given: an awsvpc task whose application container will not start — the
	// launch that has to unwind. Its unwind removes the application containers
	// inline; the namespace container they were placed in must not outlive the
	// RunTask response either.
	h, _, fd := newECSDockerTestHandler(t)
	wireStalledGC(t, h, fd)
	fd.failStartOf("app")
	ctx := context.Background()
	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "c1"}); w.Code != 200 {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family":                  "web",
		"networkMode":             "awsvpc",
		"requiresCompatibilities": []string{"FARGATE"},
		"cpu":                     "256",
		"memory":                  "512",
		"containerDefinitions":    []map[string]any{{"name": "app", "image": "nginx"}},
	}); w.Code != 200 {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}

	// When: the launch fails and RunTask returns the STOPPED task.
	if w := postJSON(t, ctx, h.RunTask, map[string]any{
		"cluster":        "c1",
		"taskDefinition": "web",
		"launchType":     "FARGATE",
		"networkConfiguration": map[string]any{
			"awsvpcConfiguration": map[string]any{"subnets": []string{"subnet-1"}},
		},
	}); w.Code != 200 {
		t.Fatalf("RunTask: HTTP %d: %s", w.Code, w.Body.String())
	}

	// Then: the namespace container the failed task was being placed into is
	// already gone.
	created := fd.createdContainers()
	if len(created) == 0 || !strings.HasSuffix(created[0].name, taskNamespaceContainerSuffix) {
		t.Fatalf("no namespace container was created; created = %d", len(created))
	}
	if !fd.wasRemoved(created[0].id) {
		t.Error("the failed task's network namespace container outlived the RunTask response")
	}
}
