package ecs

// awsvpc_namespace_test.go — every container in an awsvpc task runs in one
// network namespace, so `127.0.0.1` reaches the whole task.
//
// This is the contract the ECS sidecar pattern is built on: nginx proxying to
// php-fpm on `127.0.0.1:9000`, an application reaching its X-Ray daemon on
// `127.0.0.1:2000`. Docker gives every container a namespace of its own, so
// each container in a task used to get an address of its own and `127.0.0.1`
// that reached nothing but itself — nginx answered its own 502 because it could
// not connect to a FastCGI backend running two IP addresses away.

import (
	"context"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/dataplane"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// twoContainerFargateTaskDefinition is the shape the bug shows up in: a proxy
// and the application it proxies to, in one task, talking over loopback.
var twoContainerFargateTaskDefinition = map[string]any{
	"family":                  "web",
	"networkMode":             "awsvpc",
	"requiresCompatibilities": []string{"FARGATE"},
	"cpu":                     "256",
	"memory":                  "512",
	"containerDefinitions": []map[string]any{
		{
			"name":         "nginx",
			"image":        "nginx",
			"portMappings": []map[string]any{{"containerPort": 80, "hostPort": 80}},
		},
		{"name": "php-fpm", "image": "php:fpm"},
	},
}

// runFargateTask registers the two-container definition and runs it, returning
// the containers the daemon was asked to create.
func runFargateTask(t *testing.T, h *Handler, fd *fakeECSDockerDaemon) []createdContainer {
	t.Helper()
	ctx := context.Background()
	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "c1"}); w.Code != 200 {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, twoContainerFargateTaskDefinition); w.Code != 200 {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}
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
	return fd.createdContainers()
}

func TestAwsvpcTask_containersShareOneNetworkNamespace(t *testing.T) {
	// Given: a Fargate task of two containers that talk to each other over
	// loopback, as they would on AWS.
	h, _, fd := newECSDockerTestHandler(t)

	// When: it is placed.
	created := runFargateTask(t, h, fd)

	// Then: a namespace container was created first, and both application
	// containers were put inside its network namespace — which is what makes
	// 127.0.0.1 reach across the task.
	if len(created) != 3 {
		t.Fatalf("created %d containers, want 3 (the namespace container and two application containers)", len(created))
	}
	namespace := created[0]
	if !strings.HasSuffix(namespace.name, taskNamespaceContainerSuffix) {
		t.Fatalf("first container created is %q, want the task's namespace container", namespace.name)
	}
	if namespace.req.Image != docker.UtilityImage {
		t.Errorf("namespace container image = %q, want %q", namespace.req.Image, docker.UtilityImage)
	}
	for _, app := range created[1:] {
		if got, want := app.req.HostConfig.NetworkMode, "container:"+namespace.id; got != want {
			t.Errorf("container %q network mode = %q, want %q: it has a namespace of its own, so 127.0.0.1 reaches nothing else in the task",
				app.name, got, want)
		}
	}
}

func TestAwsvpcTask_theNamespaceOwnsTheTasksWholeNetworkSurface(t *testing.T) {
	// Given: the same task, whose one port mapping is declared on nginx.
	h, _, fd := newECSDockerTestHandler(t)

	// When: it is placed.
	created := runFargateTask(t, h, fd)
	namespace, apps := created[0], created[1:]

	// Then: the port is published on the namespace rather than on the container
	// that declared it — an awsvpc task listens on one namespace, so that is
	// where its ports are — and the namespace carries the resolvers and hosts
	// entries that point the task at Overcast.
	if _, ok := namespace.req.ExposedPorts["80/tcp"]; !ok {
		t.Errorf("namespace container exposes %v, want the task's 80/tcp", namespace.req.ExposedPorts)
	}
	if len(namespace.req.HostConfig.PortBindings["80/tcp"]) == 0 {
		t.Errorf("namespace container publishes %v, want the task's 80/tcp", namespace.req.HostConfig.PortBindings)
	}
	if len(namespace.req.HostConfig.ExtraHosts) == 0 && len(namespace.req.HostConfig.Dns) == 0 {
		t.Error("namespace container carries neither hosts entries nor resolvers; the task cannot reach Overcast's API")
	}
	if namespace.req.HostConfig.NetworkMode != dataplane.Primary(h.cfg) {
		t.Errorf("namespace container network mode = %q, want the control plane %q",
			namespace.req.HostConfig.NetworkMode, dataplane.Primary(h.cfg))
	}

	// And: no application container names any of it a second time. Docker
	// rejects a container in `container:` mode that declares its own ports,
	// resolvers, hosts entries or network attachments, so a task that tried
	// would not start at all.
	for _, app := range apps {
		if len(app.req.ExposedPorts) > 0 {
			t.Errorf("container %q exposes %v of its own", app.name, app.req.ExposedPorts)
		}
		if len(app.req.HostConfig.PortBindings) > 0 {
			t.Errorf("container %q publishes %v of its own", app.name, app.req.HostConfig.PortBindings)
		}
		if len(app.req.HostConfig.ExtraHosts) > 0 || len(app.req.HostConfig.Dns) > 0 {
			t.Errorf("container %q declares hosts entries or resolvers of its own", app.name)
		}
		if app.req.NetworkingConfig != nil && len(app.req.NetworkingConfig.EndpointsConfig) > 0 {
			t.Errorf("container %q asks to be attached to a network of its own", app.name)
		}
	}
}

func TestBridgeTask_containersKeepTheirOwnNamespace(t *testing.T) {
	// Given: a task definition in bridge mode, where AWS gives each container a
	// namespace of its own and loopback reaches only the calling container.
	h, _, fd := newECSDockerTestHandler(t)
	ctx := context.Background()
	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "c1"}); w.Code != 200 {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family":      "legacy",
		"networkMode": "bridge",
		"containerDefinitions": []map[string]any{
			{"name": "app", "image": "nginx", "portMappings": []map[string]any{{"containerPort": 80, "hostPort": 8080}}},
			{"name": "sidecar", "image": "busybox"},
		},
	}); w.Code != 200 {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}

	// When: it is placed.
	if w := postJSON(t, ctx, h.RunTask, map[string]any{"cluster": "c1", "taskDefinition": "legacy"}); w.Code != 200 {
		t.Fatalf("RunTask: HTTP %d: %s", w.Code, w.Body.String())
	}

	// Then: no namespace container was created, and each container keeps its own
	// networking — the shared namespace is what awsvpc means, not what a task is.
	created := fd.createdContainers()
	if len(created) != 2 {
		t.Fatalf("created %d containers, want 2 — a bridge task shares no namespace", len(created))
	}
	for _, c := range created {
		if strings.HasPrefix(c.req.HostConfig.NetworkMode, "container:") {
			t.Errorf("container %q joined another container's namespace in bridge mode", c.name)
		}
	}
	if _, ok := created[0].req.ExposedPorts["80/tcp"]; !ok {
		t.Errorf("container %q exposes %v, want its own 80/tcp", created[0].name, created[0].req.ExposedPorts)
	}
}

func TestStopTask_retiresTheNetworkNamespaceContainer(t *testing.T) {
	// Given: a placed Fargate task, whose namespace container outlives the
	// application containers running inside it.
	h, _, fd := newECSDockerTestHandler(t)
	created := runFargateTask(t, h, fd)
	namespaceID := created[0].id

	tasks, err := serviceutil.ScanRegions[Task](context.Background(), h.store.store, nsTasks, "us-east-1")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("stored tasks = %d (err %v), want 1", len(tasks), err)
	}
	stored := tasks[0].Value
	if stored.NetworkNamespaceID != namespaceID {
		t.Fatalf("task records namespace container %q, want %q", stored.NetworkNamespaceID, namespaceID)
	}

	// When: the task is stopped.
	if w := postJSON(t, context.Background(), h.StopTask, map[string]any{
		"cluster": "c1", "task": stored.TaskArn,
	}); w.Code != 200 {
		t.Fatalf("StopTask: HTTP %d: %s", w.Code, w.Body.String())
	}

	// Then: the namespace container goes with it. Nothing else removes it — no
	// application container's exit takes it down — so a task that left it behind
	// would leak one container and one address per task ever run.
	if !fd.wasRemoved(namespaceID) {
		t.Error("the task's network namespace container is still running after StopTask")
	}
}

// portSurfaceFor is what decides where a task's ports are published, so its
// merging is worth pinning directly: an awsvpc task publishes the union of
// every container's mappings on one namespace.
func TestPortSurfaceFor_mergesEveryContainersMappings(t *testing.T) {
	surface := portSurfaceFor(
		ContainerDefinition{Name: "nginx", PortMappings: []PortMapping{{ContainerPort: 80, HostPort: 80}}},
		ContainerDefinition{Name: "metrics", PortMappings: []PortMapping{{ContainerPort: 9090, Protocol: "udp"}}},
	)
	for _, key := range []string{"80/tcp", "9090/udp"} {
		if _, ok := surface.exposed[key]; !ok {
			t.Errorf("exposed ports %v are missing %s", surface.exposed, key)
		}
	}
	// Only the mapping that named a hostPort is published to the host; the other
	// is reachable inside the task's namespace and nowhere else, as on AWS.
	if len(surface.bindings) != 1 || len(surface.bindings["80/tcp"]) != 1 {
		t.Errorf("published ports = %v, want 80/tcp alone", surface.bindings)
	}
}
