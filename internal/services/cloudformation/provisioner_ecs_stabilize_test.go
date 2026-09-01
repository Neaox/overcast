package cloudformation

// provisioner_ecs_stabilize_test.go — what waitForServiceStable does with the
// service shapes a failing deploy actually produces, poll by poll.
//
// provisioner_ecs_test.go covers the two predicates in isolation. These drive
// the loop itself, because both defects they cover are about *when* it looks:
// a rollout still in progress sampled during the instant its first task is
// RUNNING, and the reason it reports once it gives up.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/services/ecs"
)

// scriptedECSRouter answers DescribeServices from a list of service shapes, one
// per poll, holding the last once the script runs out — the way a real service
// holds its final state while the provisioner keeps asking.
type scriptedECSRouter struct {
	mu     sync.Mutex
	polls  int
	shapes []describedECSService
}

func (r *scriptedECSRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if target := req.Header.Get("X-Amz-Target"); !strings.HasSuffix(target, "DescribeServices") {
		http.Error(w, "unexpected target "+target, http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	svc := r.shapes[min(r.polls, len(r.shapes)-1)]
	r.polls++
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	body, err := json.Marshal(map[string]any{"services": []describedECSService{svc}})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(body)
}

const testServiceARN = "arn:aws:ecs:us-east-1:123456789012:service/app-cluster/orders-api"

// TestWaitForServiceStable_transientlyRunningTaskIsNotStability is the live
// defect from the CloudFormation side. The provisioner polls 150x faster than
// the AWS CLI's `services-stable` waiter, so it samples states real AWS never
// shows a caller — including the instant a container that is about to exit is
// briefly RUNNING. Reading that as stability is what reported CREATE_COMPLETE
// twenty seconds before the same service reported 0/1 and a failed rollout.
func TestWaitForServiceStable_transientlyRunningTaskIsNotStability(t *testing.T) {
	// Given: a service whose first task is RUNNING but whose deployment has not
	// completed, and which has lost that task by the next poll
	router := &scriptedECSRouter{shapes: []describedECSService{
		{
			ServiceName: "orders-api", DesiredCount: 1, RunningCount: 1,
			Deployments: []describedECSDeployment{
				{Status: "PRIMARY", RolloutState: "IN_PROGRESS",
					RolloutStateReason: "ECS deployment e3a3e8a0 in progress."},
			},
			Events: []describedECSEvent{
				{Message: fmt.Sprintf(ecs.ServiceEventStartedTasksFormat, "orders-api", 1)},
			},
		},
		{
			ServiceName: "orders-api", DesiredCount: 1, RunningCount: 0,
			Deployments: []describedECSDeployment{
				{Status: "PRIMARY", RolloutState: "FAILED", FailedTasks: 3,
					RolloutStateReason: "ECS deployment circuit breaker: task failed to start."},
			},
			Events: []describedECSEvent{
				{Message: fmt.Sprintf(ecs.ServiceEventStartedTasksFormat, "orders-api", 1)},
			},
		},
	}}

	// When: the provisioner waits for the service
	err := waitForServiceStable(context.Background(), clock.New(), router, "us-east-1", "app-cluster", testServiceARN)

	// Then: it does not report success on the strength of that first sample
	if err == nil {
		t.Fatalf("waitForServiceStable returned success after %d poll(s)\n"+
			"The resource is complete around a deployment that never reached a steady state, which is "+
			"exactly the CREATE_COMPLETE a crash-looping container produced.", router.polls)
	}
	if !strings.Contains(err.Error(), "circuit breaker") {
		t.Errorf("error = %q, want the circuit breaker's reason", err)
	}
}

// TestWaitForServiceStable_reasonIsNeverTheInProgressPlaceholder — a deployment
// that has failed tasks but has not tripped a circuit breaker still carries the
// rolloutStateReason it was created with, which says the deploy is in progress.
// Reporting that as the reason a stack failed describes a deploy that is over as
// though it were still running, and was observed on a real failed update:
// "service orders-api did not stabilize: ECS deployment e3a3e8a0-… in progress."
func TestWaitForServiceStable_reasonIsNeverTheInProgressPlaceholder(t *testing.T) {
	// Given: a service whose tasks keep dying, with no circuit breaker to name
	// the failure and no reason-bearing scheduler event — a container that
	// starts and exits is never a placement failure
	router := &scriptedECSRouter{shapes: []describedECSService{{
		ServiceName: "orders-api", DesiredCount: 1, RunningCount: 0,
		Deployments: []describedECSDeployment{
			{Status: "PRIMARY", RolloutState: "IN_PROGRESS", FailedTasks: 2,
				RolloutStateReason: "ECS deployment e3a3e8a0-1e0f-4c2b-9f0a-7a1b2c3d4e5f in progress."},
		},
		Events: []describedECSEvent{
			{Message: fmt.Sprintf(ecs.ServiceEventStartedTasksFormat, "orders-api", 1)},
		},
	}}}

	// When: the provisioner gives up on it
	err := waitForServiceStable(context.Background(), clock.New(), router, "us-east-1", "app-cluster", testServiceARN)

	// Then: it says something true about the failure
	if err == nil {
		t.Fatal("expected the wait to fail on a deployment with failed tasks")
	}
	if strings.Contains(err.Error(), "in progress") {
		t.Errorf("error = %q, want a reason rather than the in-progress placeholder", err)
	}
	if !strings.Contains(err.Error(), "2 task") {
		t.Errorf("error = %q, want it to report the failed task count", err)
	}
	if strings.Contains(err.Error(), "has started 1 tasks") {
		t.Errorf("error = %q, want it not to quote a progress event as the failure", err)
	}
}

// TestWaitForServiceStable_completedRolloutSettles keeps the window honest: it
// is a wait, not a refusal, and a deployment that reports a steady state ends
// the poll on the spot.
func TestWaitForServiceStable_completedRolloutSettles(t *testing.T) {
	// Given: a service whose deployment has completed
	router := &scriptedECSRouter{shapes: []describedECSService{{
		ServiceName: "orders-api", DesiredCount: 2, RunningCount: 2,
		Deployments: []describedECSDeployment{
			{Status: "PRIMARY", RolloutState: "COMPLETED",
				RolloutStateReason: "ECS deployment e3a3e8a0 completed."},
		},
	}}}

	// When: the provisioner waits for it
	if err := waitForServiceStable(context.Background(), clock.New(), router, "us-east-1", "app-cluster", testServiceARN); err != nil {
		t.Fatalf("waitForServiceStable: %v", err)
	}

	// Then: it looked once and was done
	if router.polls != 1 {
		t.Errorf("polls = %d, want 1 — a settled service must not be waited on", router.polls)
	}
}
