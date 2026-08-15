package ecs

// deployment_settle_test.go — a deployment does not reach its steady state the
// instant one of its tasks reports RUNNING.
//
// AWS defines the rollout state in terms of the steady state: "When a service
// deployment is started, it begins in an IN_PROGRESS state. When the service
// reaches a steady state, the deployment transitions to a COMPLETED state"
// (API_Deployment), and a service reaches a steady state "when the service is
// healthy and at the desired number of tasks". A container that starts and then
// exits is never healthy — it is momentarily RUNNING, which is a different
// thing, and the moment it is RUNNING is the one CloudFormation's 100 ms poll
// lands in. That is how a stack reported CREATE_COMPLETE twenty seconds before
// DescribeServices reported the same service at 0/1 with three failed tasks.
//
// The fixture is deployment_health_test.go's: it places a real task through the
// fake Docker daemon and settles it into RUNNING, which is precisely the state
// these are about. Time passes by driving the injected clock.

import (
	"encoding/json"
	"testing"
	"time"
)

// describedRolloutState reads the primary deployment's rollout state back over
// DescribeServices — the read path CloudFormation samples. It recomputes the
// state from the tasks rather than reporting what was last persisted, so it
// answers the same question a poller would get, without depending on whether a
// reconcile has landed yet.
func (f *crashLoopFixture) describedRolloutState(t *testing.T) string {
	t.Helper()
	w := postJSON(t, f.ctx, f.h.DescribeServices, map[string]any{
		"cluster":  f.cluster,
		"services": []string{f.service},
	})
	if w.Code != 200 {
		t.Fatalf("DescribeServices: HTTP %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Services []struct {
			Deployments []struct {
				Status       string `json:"status"`
				RolloutState string `json:"rolloutState"`
			} `json:"deployments"`
		} `json:"services"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("DescribeServices: parse response: %v", err)
	}
	if len(resp.Services) != 1 {
		t.Fatalf("DescribeServices returned %d services, want 1", len(resp.Services))
	}
	for _, d := range resp.Services[0].Deployments {
		if d.Status == "PRIMARY" {
			return d.RolloutState
		}
	}
	t.Fatal("DescribeServices reported no PRIMARY deployment")
	return ""
}

// TestNewDeployment_isNotCompleteWhileItsTaskIsBrandNew is the live defect: a
// first deployment that has failed nothing used to complete the moment its task
// reported RUNNING, so anything sampling the service in that window — the
// CloudFormation ECS provisioner, which polls every 100 ms — saw a finished
// rollout around a container that had not yet had time to die.
func TestNewDeployment_isNotCompleteWhileItsTaskIsBrandNew(t *testing.T) {
	// Given: a one-task service whose task has just reported RUNNING
	f := newCrashLoopService(t, "settle-cluster", "settle-svc", nil)

	// Then: the deployment has not completed. A task that has been up for no
	// time at all is not evidence that it stays up.
	if got := f.describedRolloutState(t); got != rolloutInProgress {
		t.Errorf("rolloutState = %q the instant the task reported RUNNING, want IN_PROGRESS\n"+
			"A deployment that completes here reports a steady state a crash-looping container "+
			"reaches on every replacement, which is what let a stack finish around a dead service.", got)
	}

	// When: the task is left alone for the settle window
	f.clk.Add(deploymentSettleWindow)

	// Then: the deployment completes — the window is a wait, not a refusal
	if got := f.describedRolloutState(t); got != rolloutCompleted {
		t.Errorf("rolloutState = %q after the task stayed up for %s, want COMPLETED (events:\n%s)",
			got, deploymentSettleWindow, allEvents(f.storedService(t)))
	}
}

// TestNewDeployment_neverAnnouncesSteadyStateForATaskThatDiesYoung covers the
// other half of the same instant: the steady-state event is emitted on the edge
// into COMPLETED and never retracted, so announcing it for a container that is
// about to exit leaves a permanent record of a steady state that never was.
func TestNewDeployment_neverAnnouncesSteadyStateForATaskThatDiesYoung(t *testing.T) {
	// Given: a one-task service whose task has just reported RUNNING
	f := newCrashLoopService(t, "fastexit-cluster", "fastexit-svc", nil)
	taskID := f.runningTaskID(t)
	if taskID == "" {
		t.Fatal("expected a running task")
	}

	// When: its container lives about a second and then exits, which is the
	// container the live reproduction used
	f.clk.Add(time.Second)
	f.killTask(t, taskID)

	// Then: the service never said it had reached a steady state
	if pollFor(generousPoll, func() bool {
		return eventCount(f.storedService(t), "has reached a steady state") > 0
	}) {
		t.Errorf("the service announced a steady state for a task that only lived a second (events:\n%s)",
			allEvents(f.storedService(t)))
	}

	// ...and the exit is counted against the deployment
	if d := primaryDeployment(f.storedService(t)); d == nil || d.FailedTasks == 0 {
		t.Errorf("failedTasks = %#v after the container exited, want at least 1", d)
	}
}

// TestNewDeployment_recordsItsSteadyStateWithoutBeingRead — the settle window is
// only half the rule. Nothing looks at a service whose tasks are all placed, so
// without a scheduled re-reconcile the steady state, and the event announcing
// it, would surface on a read and never in the stored record.
func TestNewDeployment_recordsItsSteadyStateWithoutBeingRead(t *testing.T) {
	// Given: a one-task service whose task has just reported RUNNING
	f := newCrashLoopService(t, "settle-event-cluster", "settle-event-svc", nil)

	// When: the clock passes the settle window and nobody reads the service
	if !f.advanceUntil(3*deploymentSettleWindow, func() bool { return f.rolloutState() == rolloutCompleted }) {
		t.Fatalf("the stored deployment never completed within 3x the settle window (events:\n%s)",
			allEvents(f.storedService(t)))
	}

	// Then: the steady state is in the record the scheduler persisted, once
	svc := f.storedService(t)
	if got := eventCount(svc, "has reached a steady state"); got != 1 {
		t.Errorf("steady-state event recorded %d times, want exactly 1 (events:\n%s)", got, allEvents(svc))
	}
}
