package ecs

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/middleware"
	"go.uber.org/zap"
)

func TestRetainContainerLogs_exitedContainerCapturesBoundedTail(t *testing.T) {
	// Given: Docker still holds an exited ECS container long enough to return
	// its final multiplexed output.
	const rootCause = "entrypoint.sh: syntax error near unexpected token\n"
	frame := make([]byte, 8+len(rootCause))
	frame[0] = 2 // stderr
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(rootCause)))
	copy(frame[8:], rootCause)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/containers/removed-container/logs") {
			t.Fatalf("unexpected Docker request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		_, _ = w.Write(frame)
	}))
	t.Cleanup(daemon.Close)

	h, _ := newECSRegionTestHandler(t)
	h.docker = docker.NewClient(strings.Replace(daemon.URL, "http://", "tcp://", 1), zap.NewNop())
	h.dockerReady.Store(true)
	h.cfg.ECSKeepContainers = true
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	task := &Task{
		TaskArn:    "arn:aws:ecs:us-east-1:123456789012:task/demo/task-1",
		ClusterArn: "arn:aws:ecs:us-east-1:123456789012:cluster/demo",
		Containers: []Container{{Name: "app", DockerID: "removed-container"}},
	}

	// When: the Docker die event is handled.
	h.retainContainerLogs(ctx, task, "removed-container")

	// Then: the clean log payload is retained outside the AWS task shape.
	logs, found, aerr := h.store.getTaskContainerLogs(ctx, "demo", "task-1", "app")
	if aerr != nil {
		t.Fatalf("get retained logs: %s", aerr.Message)
	}
	if !found || logs.Logs != rootCause {
		t.Fatalf("retained logs = %q, found %v; want %q", logs.Logs, found, rootCause)
	}
}

func TestGetTaskContainerLogs_stoppedContainerUsesRetainedSnapshot(t *testing.T) {
	// Given: a stopped task whose Docker container is gone, but whose final
	// bounded log tail was retained when it exited.
	h, _ := newECSRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	task := &Task{
		TaskArn:    "arn:aws:ecs:us-east-1:123456789012:task/demo/task-1",
		ClusterArn: "arn:aws:ecs:us-east-1:123456789012:cluster/demo",
		LastStatus: "STOPPED",
		Containers: []Container{{Name: "app", DockerID: "removed-container"}},
	}
	if aerr := h.store.putTask(ctx, task); aerr != nil {
		t.Fatalf("put task: %s", aerr.Message)
	}
	if aerr := h.store.putTaskContainerLogs(ctx, "demo", "task-1", "app",
		taskContainerLogs{Logs: "entrypoint.sh: syntax error near unexpected token"}); aerr != nil {
		t.Fatalf("put retained logs: %s", aerr.Message)
	}

	// When: the emulator log endpoint is opened after Docker is unavailable.
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	routeCtx := chi.NewRouteContext()
	// The web route uses the final task ID because an ARN contains path
	// separators that cannot safely occupy one chi path segment.
	routeCtx.URLParams.Add("taskArn", "task-1")
	routeCtx.URLParams.Add("container", "app")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	w := httptest.NewRecorder()
	h.GetTaskContainerLogs(w, req)

	// Then: the retained root-cause output is returned instead of a Docker
	// availability error.
	if w.Code != http.StatusOK {
		t.Fatalf("GetTaskContainerLogs: HTTP %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Logs string `json:"logs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Logs != "entrypoint.sh: syntax error near unexpected token" {
		t.Fatalf("logs = %q, want retained entrypoint error", body.Logs)
	}
}
