package ecs

// log_retention_test.go — the container output ECS keeps after a container is
// gone: that it is the container's own bytes, that it is captured before the
// container can be removed out from under the capture, and that it does not
// outlive the task record it belongs to.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// framed wraps payload in one Docker multiplex frame, the shape a container
// started without a TTY writes.
func framed(stream byte, payload string) []byte {
	out := make([]byte, 8+len(payload))
	out[0] = stream
	binary.BigEndian.PutUint32(out[4:8], uint32(len(payload)))
	copy(out[8:], payload)
	return out
}

// putLoggedTask stores a stopped-capable task with one container and points the
// fake daemon's log endpoint for it at body.
func putLoggedTask(t *testing.T, h *Handler, fd *fakeECSDockerDaemon, ctx context.Context, cluster, taskID, dockerID string, body []byte) *Task {
	t.Helper()
	task := &Task{
		TaskArn:       h.taskARN(ctx, cluster, taskID),
		ClusterArn:    h.clusterARN(ctx, cluster),
		LastStatus:    "RUNNING",
		DesiredStatus: "RUNNING",
		Containers:    []Container{{Name: "app", DockerID: dockerID, LastStatus: "RUNNING"}},
	}
	if aerr := h.store.putTask(ctx, task); aerr != nil {
		t.Fatalf("putTask: %s", aerr.Message)
	}
	fd.setContainerLogs(dockerID, body)
	return task
}

// A container started with a TTY has no frame headers at all — the first byte
// of its output is the first byte of its output. ECS used to strip eight of
// them regardless, then read a payload length out of the ninth through twelfth,
// so a TTY task's logs came back with the opening of every line missing and the
// rest chopped at an arbitrary offset. docker.DemuxStream detects the shape
// first; nothing in ECS may re-implement it.
func TestTTYContainerLogsAreNotCorrupted(t *testing.T) {
	// "Starting" is eight bytes, so the whole first word is what a blind
	// header strip eats.
	const output = "Starting the application on port 8080\nready\n"

	t.Run("retained on exit", func(t *testing.T) {
		// Given: a TTY container whose output Docker still holds
		h, _, fd := newECSDockerTestHandler(t)
		ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
		h.cfg.ECSKeepContainers = true
		task := putLoggedTask(t, h, fd, ctx, "demo", "tty-task", "tty-container", []byte(output))

		// When: its exit is handled
		h.retainContainerLogs(ctx, task, "tty-container")

		// Then: what was retained is what the container wrote
		got, found, aerr := h.store.getTaskContainerLogs(ctx, "demo", "tty-task", "app")
		if aerr != nil {
			t.Fatalf("get retained logs: %s", aerr.Message)
		}
		if !found || got.Logs != output {
			t.Fatalf("retained logs = %q, found %v; want %q", got.Logs, found, output)
		}
	})

	t.Run("read live from the endpoint", func(t *testing.T) {
		// Given: a TTY container that is still running, so the endpoint reads
		// it rather than falling back to a retained copy
		h, _, fd := newECSDockerTestHandler(t)
		ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
		putLoggedTask(t, h, fd, ctx, "demo", "tty-task", "tty-container", []byte(output))

		// When: the emulator log endpoint is opened
		body := readTaskContainerLogs(t, h, ctx, "tty-task", "app")

		// Then: the response carries the container's own bytes
		if body.Logs != output {
			t.Fatalf("logs = %q, want %q", body.Logs, output)
		}
	})
}

// A service the scheduler retires — which is what a CloudFormation rollback
// does when it deletes the service — stops each task's containers and removes
// them straight away. Retention that only rides the asynchronous die event
// races that removal, and loses precisely on the deployment that failed, which
// is the only one anybody reads the logs of.
func TestServiceStopRetainsContainerLogsBeforeRemoval(t *testing.T) {
	// Given: a running service task whose container is about to be retired
	const output = "panic: listen tcp :8080: bind: address already in use\n"
	h, _, fd := newECSDockerTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	task := putLoggedTask(t, h, fd, ctx, "demo", "svc-task", "svc-container", framed(2, output))
	svc := &ecsService{ServiceName: "web", ClusterArn: h.clusterARN(ctx, "demo")}

	// When: the service scheduler stops it
	h.stopServiceTasks(ctx, "demo", svc, []Task{*task}, stopReasonScaling)

	// Then: the container is gone, and its last words were captured first
	if !fd.wasRemoved("svc-container") {
		t.Fatal("container was not removed; the test is not exercising the race it is about")
	}
	got, found, aerr := h.store.getTaskContainerLogs(ctx, "demo", "svc-task", "app")
	if aerr != nil {
		t.Fatalf("get retained logs: %s", aerr.Message)
	}
	if !found || got.Logs != output {
		t.Fatalf("retained logs = %q, found %v; want %q", got.Logs, found, output)
	}
	if got.CapturedAt == "" {
		t.Error("retained logs carry no capture time")
	}
}

// StopTask tears a task's containers down the same way the scheduler does, and
// so has the same race: the container is removed on the caller's goroutine
// while the only capture rides the asynchronous die event. A task somebody
// stopped by hand is one they are about to go read the logs of.
//
// This is the Docker-less-GC arm of the two, where the handler stops and
// removes the container itself. There the loss is not a race at all — nothing
// captured on that path.
func TestStopTaskRetainsContainerLogsBeforeRemoval(t *testing.T) {
	const output = "fatal: config missing DATABASE_URL\n"

	stops := []struct {
		name string
		stop func(t *testing.T, h *Handler, ctx context.Context, task *Task)
	}{{
		name: "StopTask",
		stop: func(t *testing.T, h *Handler, ctx context.Context, task *Task) {
			w := postJSON(t, ctx, h.StopTask, map[string]any{
				"cluster": "demo", "task": task.TaskArn, "reason": "by hand",
			})
			if w.Code != http.StatusOK {
				t.Fatalf("StopTask: HTTP %d: %s", w.Code, w.Body.String())
			}
		},
	}, {
		name: "stopTaskTyped",
		stop: func(t *testing.T, h *Handler, ctx context.Context, task *Task) {
			if _, aerr := h.stopTaskTyped(ctx, &stopTaskRequest{
				Cluster: "demo", Task: task.TaskArn, Reason: "by hand",
			}); aerr != nil {
				t.Fatalf("stopTaskTyped: %s", aerr.Message)
			}
		},
	}}

	for _, s := range stops {
		t.Run(s.name, func(t *testing.T) {
			// Given: a running task whose container still has its output
			h, _, fd := newECSDockerTestHandler(t)
			ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
			task := putLoggedTask(t, h, fd, ctx, "demo", "stop-task", "stop-container", framed(2, output))

			// When: the caller stops it
			s.stop(t, h, ctx, task)

			// Then: the container is gone, and its last words were captured first
			if !fd.wasRemoved("stop-container") {
				t.Fatal("container was not removed; the test is not exercising the race it is about")
			}
			got, found, aerr := h.store.getTaskContainerLogs(ctx, "demo", "stop-task", "app")
			if aerr != nil {
				t.Fatalf("get retained logs: %s", aerr.Message)
			}
			if !found || got.Logs != output {
				t.Fatalf("retained logs = %q, found %v; want %q", got.Logs, found, output)
			}
		})
	}
}

// The same teardown with the container GC wired, which is the arm that runs in
// a real Overcast: the stop and the removal both happen on the GC's own
// goroutines, so nothing on the caller's side can order the capture against
// them. The GC captures before it removes, and that is what makes the ordering
// hold for every caller that hands it a container rather than only these two.
func TestGCScheduledRemovalCapturesLogsFirst(t *testing.T) {
	// Given: a running task whose container is scheduled for GC removal
	const output = "panic: runtime error: invalid memory address\n"
	h, _, fd := newECSDockerTestHandler(t)
	wireFakeGC(t, h, fd)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	task := putLoggedTask(t, h, fd, ctx, "demo", "gc-task", "gc-container", framed(2, output))

	// When: the caller stops it and the GC gets as far as removing it
	w := postJSON(t, ctx, h.StopTask, map[string]any{
		"cluster": "demo", "task": task.TaskArn, "reason": "by hand",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("StopTask: HTTP %d: %s", w.Code, w.Body.String())
	}
	waitFor(t, "container removed", func() bool { return fd.wasRemoved("gc-container") })

	// Then: the logs it was holding survived it
	got, found, aerr := h.store.getTaskContainerLogs(ctx, "demo", "gc-task", "app")
	if aerr != nil {
		t.Fatalf("get retained logs: %s", aerr.Message)
	}
	if !found || got.Logs != output {
		t.Fatalf("retained logs = %q, found %v; want %q", got.Logs, found, output)
	}
}

// The die event fires on the scheduler path too, so both captures can run for
// one container. The first success wins: the copy taken while the container was
// certainly still there is never replaced by a later read of a container that
// is on its way out.
func TestContainerLogCaptureIsWriteOnce(t *testing.T) {
	// Given: a container whose logs have already been captured
	h, _, fd := newECSDockerTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	h.cfg.ECSKeepContainers = true
	task := putLoggedTask(t, h, fd, ctx, "demo", "once-task", "once-container", framed(1, "the real failure\n"))
	h.retainContainerLogs(ctx, task, "once-container")

	// When: the daemon starts serving something less useful and the exit is
	// handled a second time, as a reconnect replay or a die event would
	fd.setContainerLogs("once-container", nil)
	h.retainContainerLogs(ctx, task, "once-container")

	// Then: the first capture stands
	got, _, aerr := h.store.getTaskContainerLogs(ctx, "demo", "once-task", "app")
	if aerr != nil {
		t.Fatalf("get retained logs: %s", aerr.Message)
	}
	if got.Logs != "the real failure\n" {
		t.Fatalf("retained logs = %q; the second capture overwrote the first", got.Logs)
	}
}

// Retained logs are keyed outside the task record so they can never reach
// DescribeTasks' AWS shape — which also means nothing deletes them with the
// task unless it is done on purpose. Left undone, every task a crash-looping
// service ever placed keeps its log tail for the life of the process.
func TestReapedTaskDropsItsRetainedLogs(t *testing.T) {
	// Given: a stopped task, past its retention window, with retained logs
	h, clk := newECSRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	stoppedAt := clk.Now().Unix()
	task := &Task{
		TaskArn:    h.taskARN(ctx, "demo", "old-task"),
		ClusterArn: h.clusterARN(ctx, "demo"),
		LastStatus: "STOPPED",
		StoppedAt:  &stoppedAt,
		Containers: []Container{{Name: "app", LastStatus: "STOPPED"}},
	}
	if aerr := h.store.putTask(ctx, task); aerr != nil {
		t.Fatalf("putTask: %s", aerr.Message)
	}
	if aerr := h.store.putTaskContainerLogs(ctx, "demo", "old-task", "app",
		taskContainerLogs{Logs: "exit status 1\n", CapturedAt: "2026-08-15T00:00:00Z"}); aerr != nil {
		t.Fatalf("put retained logs: %s", aerr.Message)
	}
	clk.Add(stoppedTaskRetention + time.Minute)

	// When: the record is read after it has expired, which is where the reap
	// happens
	got, aerr := h.getVisibleTask(ctx, "demo", "old-task")
	if aerr != nil || got != nil {
		t.Fatalf("getVisibleTask = %v, %v; want the task reaped", got, aerr)
	}

	// Then: its logs went with it
	pairs, err := h.store.store.Scan(ctx, nsTaskContainerLogs, serviceutil.RegionKey("us-east-1", ""))
	if err != nil {
		t.Fatalf("scan retained logs: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("%d retained log entries survived the reap: %v", len(pairs), pairs)
	}
}

// readTaskContainerLogs drives the emulator endpoint and decodes its body.
func readTaskContainerLogs(t *testing.T, h *Handler, ctx context.Context, taskID, container string) struct {
	Logs       string `json:"logs"`
	LogSource  string `json:"logSource"`
	CapturedAt string `json:"capturedAt"`
} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("taskArn", taskID)
	routeCtx.URLParams.Add("container", container)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	w := httptest.NewRecorder()
	h.GetTaskContainerLogs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetTaskContainerLogs: HTTP %d: %s", w.Code, strings.TrimSpace(w.Body.String()))
	}
	var body struct {
		Logs       string `json:"logs"`
		LogSource  string `json:"logSource"`
		CapturedAt string `json:"capturedAt"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}
