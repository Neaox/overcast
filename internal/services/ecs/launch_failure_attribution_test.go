package ecs

// launch_failure_attribution_test.go — a task that cannot start blames the
// container that could not start, not every container in the task.
//
// A CDK stack whose ECS deployment failed reported CannotPullContainerError,
// naming the same CDK container-asset image, against all four containers of its
// task definition — the nginx, php and migrations containers built from that
// asset, and an X-Ray sidecar that runs public.ecr.aws/xray/aws-xray-daemon and
// never names the asset at all. Real ECS records the failure on the container
// whose image could not be pulled and leaves the others STOPPED with no reason,
// so the sidecar reads as the cause of a failure it had no part in.
//
// The pull is the only Docker call this reaches, so the daemon is faked rather
// than required: the attribution is what is under test, not Docker.
// tests/integration/ecs/task_start_failure_test.go covers the same shape
// end-to-end where a real daemon is available.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
)

// The two images the report named: the container asset CDK publishes to its
// bootstrapped ECR repository, and the sidecar's public image.
const (
	assetImage   = "000000000000.dkr.ecr.us-east-1.amazonaws.com/cdk-hnb659fds-container-assets-000000000000-us-east-1:10fe2b8f"
	sidecarImage = "public.ecr.aws/xray/aws-xray-daemon:latest"
)

// newFailingPullDaemon refuses every image pull the way a registry refuses an
// image it does not have. When the test itself runs in a container, endpoint
// discovery also attaches that container to Overcast's control plane.
func newFailingPullDaemon(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/networks/overcast_control/connect":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images/create"):
			// Docker reports a failed pull in the last line of the progress
			// stream, not in the status code.
			image := r.URL.Query().Get("fromImage")
			_, _ = w.Write([]byte(`{"status":"Pulling from ` + image + `"}` + "\n" +
				`{"error":"pull access denied for ` + image + `, repository does not exist"}` + "\n"))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/json"):
			// The daemon does not already hold the image either, so the failed
			// pull stands rather than being forgiven as an offline daemon.
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected Docker request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// handlerWithFailingPulls returns a handler that places real containers, on a
// daemon whose every pull fails.
func handlerWithFailingPulls(t *testing.T) *Handler {
	t.Helper()
	h, _ := newECSRegionTestHandler(t)
	daemon := newFailingPullDaemon(t)
	h.docker = docker.NewClient(strings.Replace(daemon.URL, "http://", "tcp://", 1), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	return h
}

func TestRunTask_pullFailureAttributedToTheContainerThatCouldNotPull(t *testing.T) {
	// Given: a task definition whose first container is a CDK container asset
	// and whose second is the X-Ray sidecar, on a daemon that cannot pull.
	h := handlerWithFailingPulls(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")

	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "managed"}); w.Code != http.StatusOK {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family": "managed-services",
		"containerDefinitions": []map[string]any{
			{"name": "php", "image": assetImage},
			{"name": "xray-daemon", "image": sidecarImage},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}

	// When: the task is placed and the first container's image cannot be pulled.
	w := postJSON(t, ctx, h.RunTask, map[string]any{
		"cluster":        "managed",
		"taskDefinition": "managed-services:1",
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

	// Then: the task is stopped, and says so once, at task level.
	if task.LastStatus != "STOPPED" || task.StopCode != "TaskFailedToStart" {
		t.Fatalf("task lastStatus=%q stopCode=%q, want STOPPED/TaskFailedToStart", task.LastStatus, task.StopCode)
	}
	if !strings.HasPrefix(task.StoppedReason, "CannotPullContainerError: ") {
		t.Errorf("task stoppedReason = %q, want a CannotPullContainerError", task.StoppedReason)
	}

	if len(task.Containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(task.Containers))
	}
	php, xray := task.Containers[0], task.Containers[1]

	// Then: the container whose image could not be pulled carries the reason.
	if !strings.HasPrefix(php.Reason, "CannotPullContainerError: ") || !strings.Contains(php.Reason, assetImage) {
		t.Errorf("php container reason = %q, want a CannotPullContainerError naming %s", php.Reason, assetImage)
	}

	// Then: the sidecar, which was never reached, carries none — it stopped
	// because the task did, and did not fail to pull anything.
	if xray.Reason != "" {
		t.Errorf("xray-daemon container reason = %q, want none: it did not fail to start", xray.Reason)
	}
	if xray.LastStatus != "STOPPED" {
		t.Errorf("xray-daemon container lastStatus = %q, want STOPPED", xray.LastStatus)
	}

	// Then: each container still reports its own image, so the sidecar is not
	// read as running the asset the failure names.
	if php.Image != assetImage {
		t.Errorf("php container image = %q, want %q", php.Image, assetImage)
	}
	if xray.Image != sidecarImage {
		t.Errorf("xray-daemon container image = %q, want %q", xray.Image, sidecarImage)
	}
}
