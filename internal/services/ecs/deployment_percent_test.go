package ecs

// deployment_percent_test.go — what a rolling deployment is allowed to run at
// once, and what happens on a shared Docker host when it runs too much.
//
// On AWS a service's deploymentConfiguration bounds the rollout: maximumPercent
// is the ceiling on running-plus-pending tasks, minimumHealthyPercent the floor
// on running ones, both as a percentage of desiredCount. The default pair
// (200/100) starts the replacement before retiring what it replaces, which is
// what keeps a service serving through a deploy. Setting maximumPercent to 100
// asks for the opposite order — stop first, then start — and that is the
// documented way to deploy a service that cannot tolerate two copies of itself
// at once.
//
// Locally, "cannot tolerate two copies" is not a niche case: an awsvpc task
// whose port mapping carries a hostPort publishes it on the one Docker host, so
// two tasks of the same service contend for it. On AWS they would not — each
// task has its own ENI — and the emulator has no ENIs to hand out, so the
// deployment settings are the lever that actually exists. They therefore have
// to work.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/middleware"
)

// newRollingFixture creates a one-task service whose container publishes a
// fixed host port, and settles its first task into RUNNING. The port is the
// point: it is what makes a second, overlapping task fail to start, the way it
// does against a real daemon.
func newRollingFixture(t *testing.T, service string, deploymentCfg map[string]any, enforcePorts bool) *crashLoopFixture {
	t.Helper()
	h, clk, fd := newECSDockerTestHandler(t)
	if enforcePorts {
		// Before the first task, so that it claims the port and a second one
		// genuinely contends with it. Enabling this afterwards would leave the
		// incumbent holding nothing.
		fd.enforceHostPorts()
	}
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	cluster := service + "-cluster"

	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": cluster}); w.Code != 200 {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	registerPortedTaskDefinition(t, ctx, h, service, "busybox")

	req := map[string]any{
		"cluster":        cluster,
		"serviceName":    service,
		"taskDefinition": service + "-td:1",
		"desiredCount":   1,
		"networkConfiguration": map[string]any{
			"awsvpcConfiguration": map[string]any{"subnets": []string{"subnet-1"}},
		},
	}
	if deploymentCfg != nil {
		req["deploymentConfiguration"] = deploymentCfg
	}
	if w := postJSON(t, ctx, h.CreateService, req); w.Code != 200 {
		t.Fatalf("CreateService: HTTP %d: %s", w.Code, w.Body.String())
	}

	f := &crashLoopFixture{h: h, clk: clk, fd: fd, ctx: ctx, cluster: cluster, service: service}
	if !f.advanceUntil(5*time.Second, func() bool { return f.taskIsRunning("") }) {
		t.Fatal("the service's first task never reached RUNNING")
	}
	return f
}

// registerPortedTaskDefinition registers a revision of the fixture's task
// definition. Every revision publishes host port 80, so a second task alive
// beside the first collides with it — which is the situation under test.
func registerPortedTaskDefinition(t *testing.T, ctx context.Context, h *Handler, service, image string) {
	t.Helper()
	w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family":      service + "-td",
		"networkMode": "awsvpc",
		"containerDefinitions": []map[string]any{{
			"name":  "app",
			"image": image,
			"portMappings": []map[string]any{
				{"containerPort": 80, "hostPort": 80, "protocol": "tcp"},
			},
		}},
	})
	if w.Code != 200 {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}
}

// rollOut points the service at a second revision of its task definition, which
// is what starts a new deployment.
func (f *crashLoopFixture) rollOut(t *testing.T) {
	t.Helper()
	registerPortedTaskDefinition(t, f.ctx, f.h, f.service, "busybox:latest")
	w := postJSON(t, f.ctx, f.h.UpdateService, map[string]any{
		"cluster":        f.cluster,
		"service":        f.service,
		"taskDefinition": f.service + "-td:2",
	})
	if w.Code != 200 {
		t.Fatalf("UpdateService: HTTP %d: %s", w.Code, w.Body.String())
	}
}

// liveTaskCount counts the service's tasks that have not stopped — the number
// maximumPercent bounds.
func (f *crashLoopFixture) liveTaskCount(t *testing.T) int {
	t.Helper()
	tasks, aerr := f.h.store.listTasks(f.ctx, f.cluster)
	if aerr != nil {
		t.Fatalf("listTasks: %v", aerr)
	}
	live := 0
	for _, task := range tasks {
		if task.Group == serviceGroupPrefix+f.service && task.LastStatus != "STOPPED" {
			live++
		}
	}
	return live
}

// serviceEventMessages returns the service's events, newest first.
func (f *crashLoopFixture) serviceEventMessages(t *testing.T) []string {
	t.Helper()
	svc := f.storedService(t)
	msgs := make([]string, 0, len(svc.Events))
	for _, e := range svc.Events {
		msgs = append(msgs, e.Message)
	}
	return msgs
}

func containsMessage(msgs []string, substr string) bool {
	for _, m := range msgs {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

// TestRollingDeployment_maximumPercent100_retiresTheOldTaskBeforePlacingTheNew
// is the live defect: deploymentConfiguration is stored, echoed back by
// DescribeServices, and never acted on, so a service that asked to be deployed
// one task at a time is deployed two at a time anyway.
func TestRollingDeployment_maximumPercent100_retiresTheOldTaskBeforePlacingTheNew(t *testing.T) {
	// Given: a one-task service that asked never to run more than one task
	f := newRollingFixture(t, "max-100", map[string]any{
		"maximumPercent":        100,
		"minimumHealthyPercent": 0,
	}, false)
	original := f.runningTaskID(t)

	// When: a new revision is rolled out
	f.rollOut(t)

	// Then: at no point are two tasks alive — the old one is retired first
	if live := f.liveTaskCount(t); live > 1 {
		t.Errorf("live tasks during the rollout = %d, want at most 1 (maximumPercent 100)", live)
	}

	// Then: the replacement starts, because the port it needs was given up
	if !f.advanceUntil(replacementMaxDelay+5*time.Second, func() bool { return f.taskIsRunning(original) }) {
		t.Fatalf("the replacement task never reached RUNNING; service events: %v", f.serviceEventMessages(t))
	}
	if containsMessage(f.serviceEventMessages(t), "unable to place a task") {
		t.Errorf("the rollout reported a placement failure it should not have hit: %v", f.serviceEventMessages(t))
	}
}

// TestRollingDeployment_defaultPercentages_overlapTheTwoDeployments pins the
// other half of the contract: with AWS's defaults the replacement is placed
// before the task it replaces is retired, because minimumHealthyPercent 100
// says the service must keep serving. That is correct, and it is also why a
// service publishing a fixed host port cannot be deployed on the defaults here.
func TestRollingDeployment_defaultPercentages_overlapTheTwoDeployments(t *testing.T) {
	// Given: a one-task service on AWS's default deployment configuration
	f := newRollingFixture(t, "default-pct", nil, false)
	original := f.runningTaskID(t)

	// When: a new revision is rolled out
	f.rollOut(t)

	// Then: both deployments' tasks are alive at once
	if live := f.liveTaskCount(t); live != 2 {
		t.Errorf("live tasks during the rollout = %d, want 2 (maximumPercent defaults to 200)", live)
	}
	if !f.advanceUntil(replacementMaxDelay+5*time.Second, func() bool { return f.taskIsRunning(original) }) {
		t.Fatal("the replacement task never reached RUNNING")
	}
}

// TestRollingDeployment_maximumPercent100_survivesAFixedHostPortConflict is the
// reported failure, end to end: a service whose task publishes host port 80 is
// deployed against a daemon that enforces host-port exclusivity, as a real one
// does. On the defaults the replacement cannot start; asking for one task at a
// time is what makes the deploy possible.
func TestRollingDeployment_maximumPercent100_survivesAFixedHostPortConflict(t *testing.T) {
	for _, tc := range []struct {
		name        string
		service     string
		cfg         map[string]any
		wantBlocked bool
	}{
		{
			name:        "default percentages collide on the host port",
			service:     "port-default",
			cfg:         nil,
			wantBlocked: true,
		},
		{
			name:        "one task at a time deploys cleanly",
			service:     "port-serial",
			cfg:         map[string]any{"maximumPercent": 100, "minimumHealthyPercent": 0},
			wantBlocked: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a service publishing a fixed host port, and a daemon that
			// refuses a second container asking for the same one
			f := newRollingFixture(t, tc.service, tc.cfg, true)
			original := f.runningTaskID(t)

			// When: a new revision is rolled out
			f.rollOut(t)
			started := f.advanceUntil(replacementMaxDelay+5*time.Second, func() bool {
				return f.taskIsRunning(original)
			})

			// Then: the deployment either wedges on the port or rolls cleanly,
			// according to what it asked to be allowed to run at once
			if tc.wantBlocked {
				if started {
					t.Error("the replacement started while the old task still held host port 80")
				}
				if !containsMessage(f.serviceEventMessages(t), "port is already allocated") {
					t.Errorf("the port conflict is not reported in the service's events: %v",
						f.serviceEventMessages(t))
				}
				return
			}
			if !started {
				t.Fatalf("the replacement never started; service events: %v", f.serviceEventMessages(t))
			}
		})
	}
}

// TestRollingDeployment_impossibleConfiguration_saysWhyItIsNotMoving covers the
// pair that cannot make progress at any desired count: nothing may be retired
// and nothing more may be placed. AWS stalls here with no explanation, which is
// the least diagnosable shape a stuck service has — so the stall is faithful
// and the sentence saying why is not.
func TestRollingDeployment_impossibleConfiguration_saysWhyItIsNotMoving(t *testing.T) {
	// Given: a service that may run at most one task and must keep one running
	f := newRollingFixture(t, "stalled", map[string]any{
		"maximumPercent":        100,
		"minimumHealthyPercent": 100,
	}, false)
	original := f.runningTaskID(t)

	// When: a new revision is rolled out
	f.rollOut(t)

	// Then: the rollout cannot move, and the old task keeps serving
	if started := f.advanceUntil(2*time.Second, func() bool { return f.taskIsRunning(original) }); started {
		t.Error("a replacement was placed past the service's own maximumPercent")
	}
	if live := f.liveTaskCount(t); live != 1 {
		t.Errorf("live tasks = %d, want 1 — the incumbent, still serving", live)
	}

	// Then: the service says why, rather than sitting silent until the caller
	// times out
	msgs := f.serviceEventMessages(t)
	if !containsMessage(msgs, "is holding at 1 task(s)") {
		t.Errorf("the stall is unexplained in the service's events: %v", msgs)
	}

	// Then: it says it once, however many times the scheduler comes back
	f.advance(2 * time.Second)
	held := 0
	for _, m := range f.serviceEventMessages(t) {
		if strings.Contains(m, "is holding at") {
			held++
		}
	}
	if held != 1 {
		t.Errorf("the stall was reported %d times, want 1", held)
	}
}

// TestRollingDeployment_describeServicesEchoesTheConfiguration guards the read
// path the settings arrive on: a caller that cannot see what it set has no way
// to tell whether the emulator took it.
func TestRollingDeployment_describeServicesEchoesTheConfiguration(t *testing.T) {
	f := newRollingFixture(t, "echo-pct", map[string]any{
		"maximumPercent":        100,
		"minimumHealthyPercent": 0,
	}, false)

	w := postJSON(t, f.ctx, f.h.DescribeServices, map[string]any{
		"cluster":  f.cluster,
		"services": []string{f.service},
	})
	if w.Code != 200 {
		t.Fatalf("DescribeServices: HTTP %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Services []struct {
			DeploymentConfiguration struct {
				MaximumPercent        *int `json:"maximumPercent"`
				MinimumHealthyPercent *int `json:"minimumHealthyPercent"`
			} `json:"deploymentConfiguration"`
		} `json:"services"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse DescribeServices: %v", err)
	}
	if len(resp.Services) != 1 {
		t.Fatalf("DescribeServices returned %d services, want 1", len(resp.Services))
	}
	got := resp.Services[0].DeploymentConfiguration
	if got.MaximumPercent == nil || *got.MaximumPercent != 100 {
		t.Errorf("maximumPercent = %v, want 100", got.MaximumPercent)
	}
	if got.MinimumHealthyPercent == nil || *got.MinimumHealthyPercent != 0 {
		t.Errorf("minimumHealthyPercent = %v, want 0", got.MinimumHealthyPercent)
	}
}
