package ecs

// deployment_health_test.go — what a service reports while its tasks keep
// dying. A crash-looping container is the case these cover: the deployment must
// count the failures, must not keep announcing a steady state it never reached,
// must say once that it cannot consistently start tasks, and must trip the
// deployment circuit breaker when the service asked for one.
//
// Time passes here by driving the injected clock, not by sleeping: the
// replacement backoff alone reaches 30 s per cycle, and a crash loop is several
// of those. The only real waits are the millisecond-scale ones in pollFor,
// which give a callback the mock clock has already released time to reach a
// core — they wait on the scheduler, never on the emulated delay.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
)

// crashLoopFixture is a service whose single task is placed, reaches RUNNING
// and can then be killed on demand, as a container exiting on its own does.
type crashLoopFixture struct {
	h       *Handler
	clk     *clock.Mock
	ctx     context.Context
	cluster string
	service string
}

// newCrashLoopService creates a cluster, a task definition and a one-task
// service, and settles the first task into RUNNING.
func newCrashLoopService(t *testing.T, cluster, service string, deploymentCfg map[string]any) *crashLoopFixture {
	t.Helper()
	h, clk := newECSRegionTestHandler(t)
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")

	if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": cluster}); w.Code != 200 {
		t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
	}
	if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
		"family":               service + "-td",
		"containerDefinitions": []map[string]any{{"name": "app", "image": "busybox"}},
	}); w.Code != 200 {
		t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
	}
	req := map[string]any{
		"cluster":        cluster,
		"serviceName":    service,
		"taskDefinition": service + "-td:1",
		"desiredCount":   1,
	}
	if deploymentCfg != nil {
		req["deploymentConfiguration"] = deploymentCfg
	}
	if w := postJSON(t, ctx, h.CreateService, req); w.Code != 200 {
		t.Fatalf("CreateService: HTTP %d: %s", w.Code, w.Body.String())
	}

	f := &crashLoopFixture{h: h, clk: clk, ctx: ctx, cluster: cluster, service: service}
	if !f.advanceUntil(5*time.Second, func() bool { return f.taskIsRunning("") }) {
		t.Fatal("the service's first task never reached RUNNING")
	}
	return f
}

// advance jumps the mock clock and waits out the scheduler's callbacks. Only
// for asserting that nothing happens — anything expecting an outcome should use
// advanceUntil, which does not depend on how fast the machine is.
func (f *crashLoopFixture) advance(d time.Duration) {
	f.clk.Add(d)
	pollFor(generousPoll, func() bool { return false })
}

// advanceUntil moves the mock clock a second at a time until done reports true,
// up to limit. One second at a time rather than one jump because a callback
// scheduled *by* a callback — the RUNNING transition scheduling the settle, the
// settle scheduling the recovery check — only reliably gets its turn on the
// next step.
//
// The per-step wait is short: noticing a step late costs one mock second, which
// none of these assertions care about. The wait before giving up is long,
// because that one is the difference between a real failure and a loaded
// machine.
func (f *crashLoopFixture) advanceUntil(limit time.Duration, done func() bool) bool {
	if pollFor(quickPoll, done) {
		return true
	}
	for waited := time.Duration(0); waited <= limit; waited += time.Second {
		f.clk.Add(time.Second)
		if pollFor(quickPoll, done) {
			return true
		}
	}
	return pollFor(generousPoll, done)
}

// How long to wait for a scheduler callback the clock has released but that has
// not been scheduled onto a core yet.
const (
	quickPoll    = 10 * time.Millisecond
	generousPoll = 500 * time.Millisecond
)

// pollFor checks done immediately and then every 2 ms until the budget runs out.
func pollFor(budget time.Duration, done func() bool) bool {
	for waited := time.Duration(0); ; waited += 2 * time.Millisecond {
		if done() {
			return true
		}
		if waited >= budget {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// runningTaskID returns the service's one non-STOPPED task.
func (f *crashLoopFixture) runningTaskID(t *testing.T) string {
	t.Helper()
	tasks, aerr := f.h.store.listTasks(f.ctx, f.cluster)
	if aerr != nil {
		t.Fatalf("listTasks: %v", aerr)
	}
	for _, task := range tasks {
		if task.Group == serviceGroupPrefix+f.service && task.LastStatus != "STOPPED" {
			return extractTaskID(task.TaskArn)
		}
	}
	return ""
}

// taskIsRunning reports whether the service has a RUNNING task other than the
// named one. It is the advanceUntil condition for "a replacement is up", and
// takes no *testing.T because it is polled off the assertion path.
func (f *crashLoopFixture) taskIsRunning(otherThan string) bool {
	tasks, aerr := f.h.store.listTasks(f.ctx, f.cluster)
	if aerr != nil {
		return false
	}
	for _, task := range tasks {
		if task.Group != serviceGroupPrefix+f.service || task.LastStatus != "RUNNING" {
			continue
		}
		if extractTaskID(task.TaskArn) != otherThan {
			return true
		}
	}
	return false
}

// rolloutState reports the primary deployment's persisted rollout state.
func (f *crashLoopFixture) rolloutState() string {
	svc, aerr := f.h.store.getService(f.ctx, f.cluster, f.service)
	if aerr != nil || svc == nil {
		return ""
	}
	if d := primaryDeployment(svc); d != nil {
		return d.RolloutState
	}
	return ""
}

// killTask makes the task's container exit the way a crash-looping one does:
// through the Docker exit notifier, which is the real path a service-owned
// container takes when it dies on its own.
func (f *crashLoopFixture) killTask(t *testing.T, taskID string) {
	t.Helper()
	task, aerr := f.h.store.getTask(f.ctx, f.cluster, taskID)
	if aerr != nil || task == nil {
		t.Fatalf("getTask(%s): %v", taskID, aerr)
	}
	dockerID := "docker-" + taskID
	task.Containers[0].DockerID = dockerID
	if aerr := f.h.store.putTask(f.ctx, task); aerr != nil {
		t.Fatalf("putTask: %v", aerr)
	}
	f.h.handleContainerDied(f.ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			Service:     "ecs",
			ResourceID:  f.cluster + "/" + taskID,
			ContainerID: dockerID,
			ExitCode:    "7",
		},
	})
}

// crashOnce kills the service's current task and waits for the replacement.
//
// The clock is advanced a second at a time rather than in one jump past the
// 30 s backoff cap, so the replacement is only as old as it takes to place it.
// That is the case under test: a container that exits on startup, not one left
// running for half a minute — which would be a recovery, and should be.
func (f *crashLoopFixture) crashOnce(t *testing.T) {
	t.Helper()
	previous := f.runningTaskID(t)
	if previous == "" {
		return // the deployment placed no task to kill (e.g. the breaker tripped)
	}
	f.killTask(t, previous)
	f.advanceUntil(replacementMaxDelay+2*time.Second, func() bool { return f.taskIsRunning(previous) })
}

// storedService reads back what the scheduler actually persisted, which is what
// a later DescribeServices reports and what these assertions are about.
func (f *crashLoopFixture) storedService(t *testing.T) *ecsService {
	t.Helper()
	svc, aerr := f.h.store.getService(f.ctx, f.cluster, f.service)
	if aerr != nil || svc == nil {
		t.Fatalf("getService: %v", aerr)
	}
	return svc
}

func eventCount(svc *ecsService, substr string) int {
	n := 0
	for _, e := range svc.Events {
		if strings.Contains(e.Message, substr) {
			n++
		}
	}
	return n
}

func allEvents(svc *ecsService) string {
	var b strings.Builder
	for _, e := range svc.Events {
		b.WriteString("  ")
		b.WriteString(e.Message)
		b.WriteString("\n")
	}
	return b.String()
}

// TestCrashLoopingService_countsFailuresAndStopsClaimingSteadyState covers
// defect 3: a service whose container exits immediately used to report
// rolloutState COMPLETED, failedTasks 0, and a fresh "has reached a steady
// state" event on every replacement.
func TestCrashLoopingService_countsFailuresAndStopsClaimingSteadyState(t *testing.T) {
	// Given: a one-task service that has placed its first task
	f := newCrashLoopService(t, "crash-cluster", "crash-svc", nil)

	// When: its container exits, over and over
	for i := 0; i < 5; i++ {
		f.crashOnce(t)
	}

	// Then: the failures are counted on the deployment
	svc := f.storedService(t)
	d := primaryDeployment(svc)
	if d == nil {
		t.Fatal("expected a PRIMARY deployment")
	}
	if d.FailedTasks < 5 {
		t.Errorf("failedTasks = %d, want at least 5 (events:\n%s)", d.FailedTasks, allEvents(svc))
	}

	// ...the deployment does not claim to have completed while tasks cycle
	if d.RolloutState == rolloutCompleted {
		t.Errorf("rolloutState = COMPLETED for a crash-looping service (reason %q, events:\n%s)",
			d.RolloutStateReason, allEvents(svc))
	}

	// ...and the steady-state event is not re-announced on every replacement
	if got := eventCount(svc, "has reached a steady state"); got > 1 {
		t.Errorf("steady-state event recorded %d times, want at most 1 (events:\n%s)", got, allEvents(svc))
	}
}

// TestCrashLoopingService_reportsUnableToConsistentlyStartTasks covers defect 1.
func TestCrashLoopingService_reportsUnableToConsistentlyStartTasks(t *testing.T) {
	// Given: a one-task service that has placed its first task
	f := newCrashLoopService(t, "inconsistent-cluster", "inconsistent-svc", nil)

	// When: its container exits repeatedly
	for i := 0; i < 6; i++ {
		f.crashOnce(t)
	}

	// Then: AWS's "unable to consistently start tasks" event is recorded once —
	// once per episode, not once per failure
	svc := f.storedService(t)
	got := eventCount(svc, "is unable to consistently start tasks successfully")
	if got != 1 {
		t.Errorf("'unable to consistently start tasks' recorded %d times, want exactly 1 (events:\n%s)",
			got, allEvents(svc))
	}
	if !strings.Contains(allEvents(svc), "For more information, see the Troubleshooting section.") {
		t.Errorf("expected AWS's troubleshooting sentence on the event, got:\n%s", allEvents(svc))
	}
}

// TestCrashLoopingService_circuitBreakerTrips covers defect 2.
func TestCrashLoopingService_circuitBreakerTrips(t *testing.T) {
	// Given: a one-task service that asked for a deployment circuit breaker
	f := newCrashLoopService(t, "breaker-cluster", "breaker-svc", map[string]any{
		"deploymentCircuitBreaker": map[string]any{"enable": true, "rollback": false},
	})

	// When: its container exits repeatedly, past the threshold
	for i := 0; i < 6; i++ {
		f.crashOnce(t)
	}

	// Then: the breaker has failed the rollout, naming itself in the reason
	svc := f.storedService(t)
	d := primaryDeployment(svc)
	if d == nil {
		t.Fatal("expected a PRIMARY deployment")
	}
	if d.RolloutState != rolloutFailed {
		t.Fatalf("rolloutState = %q, want FAILED (failedTasks %d, events:\n%s)",
			d.RolloutState, d.FailedTasks, allEvents(svc))
	}
	if !strings.Contains(d.RolloutStateReason, "circuit breaker") {
		t.Errorf("rolloutStateReason = %q, want it to name the circuit breaker", d.RolloutStateReason)
	}
	if got := eventCount(svc, "deployment failed: tasks failed to start."); got != 1 {
		t.Errorf("circuit-breaker event recorded %d times, want exactly 1 (events:\n%s)", got, allEvents(svc))
	}

	// ...and a failed deployment places no further tasks
	if id := f.runningTaskID(t); id != "" {
		t.Errorf("task %s placed after the circuit breaker failed the deployment", id)
	}
}

// TestCrashLoopingService_withoutCircuitBreakerStaysInProgress — with the
// breaker off AWS keeps retrying and reports the failure through events alone.
func TestCrashLoopingService_withoutCircuitBreakerStaysInProgress(t *testing.T) {
	// Given: a one-task service with no circuit breaker
	f := newCrashLoopService(t, "nobreaker-cluster", "nobreaker-svc", nil)

	// When: its container exits repeatedly
	for i := 0; i < 6; i++ {
		f.crashOnce(t)
	}

	// Then: the rollout stays IN_PROGRESS and the scheduler keeps replacing
	svc := f.storedService(t)
	d := primaryDeployment(svc)
	if d == nil {
		t.Fatal("expected a PRIMARY deployment")
	}
	if d.RolloutState != rolloutInProgress {
		t.Errorf("rolloutState = %q, want IN_PROGRESS without a circuit breaker", d.RolloutState)
	}
	if id := f.runningTaskID(t); id == "" {
		t.Errorf("expected a replacement task to have been placed (events:\n%s)", allEvents(svc))
	}
}

// TestServiceScaleDown_doesNotCountAsDeploymentFailure — a task the scheduler
// stopped on purpose is not a failed task, and the container's die event must
// not rewrite the stop code the scheduler already recorded.
func TestServiceScaleDown_doesNotCountAsDeploymentFailure(t *testing.T) {
	// Given: a service running one task
	f := newCrashLoopService(t, "scaledown-cluster", "scaledown-svc", nil)
	taskID := f.runningTaskID(t)
	if taskID == "" {
		t.Fatal("expected a running task")
	}

	// When: the service is scaled to zero and the container's exit is then
	// reported by Docker, as it is for every container the scheduler stops
	if w := postJSON(t, f.ctx, f.h.UpdateService, map[string]any{
		"cluster": f.cluster, "service": f.service, "desiredCount": 0,
	}); w.Code != 200 {
		t.Fatalf("UpdateService: HTTP %d: %s", w.Code, w.Body.String())
	}
	f.killTask(t, taskID)

	// Then: nothing counts as a deployment failure
	svc := f.storedService(t)
	d := primaryDeployment(svc)
	if d == nil {
		t.Fatal("expected a PRIMARY deployment")
	}
	if d.FailedTasks != 0 {
		t.Errorf("failedTasks = %d after a deliberate scale-down, want 0 (events:\n%s)",
			d.FailedTasks, allEvents(svc))
	}

	// ...the task keeps the stop code the scheduler gave it
	task, aerr := f.h.store.getTask(f.ctx, f.cluster, taskID)
	if aerr != nil || task == nil {
		t.Fatalf("getTask: %v", aerr)
	}
	if task.StopCode != "ServiceSchedulerInitiated" {
		t.Errorf("stopCode = %q, want ServiceSchedulerInitiated (the die event overwrote it)", task.StopCode)
	}

	// ...and no replacement is placed for a task nothing is missing
	f.advance(replacementMaxDelay + 2*time.Second)
	if id := f.runningTaskID(t); id != "" {
		t.Errorf("task %s placed after the service was scaled to zero", id)
	}
}

// TestStopTask_onAServiceTask_replacesItWithoutCountingAFailure — stopping a
// service's task by hand does not reduce its desired count, so ECS replaces it;
// and a task the caller stopped is not a task that failed.
func TestStopTask_onAServiceTask_replacesItWithoutCountingAFailure(t *testing.T) {
	// Given: a service running one task
	f := newCrashLoopService(t, "stoptask-cluster", "stoptask-svc", nil)
	taskID := f.runningTaskID(t)
	if taskID == "" {
		t.Fatal("expected a running task")
	}

	// When: the caller stops that task, and Docker then reports its container's
	// exit as it does for every container that is killed
	if w := postJSON(t, f.ctx, f.h.StopTask, map[string]any{
		"cluster": f.cluster, "task": taskID, "reason": "done with it",
	}); w.Code != 200 {
		t.Fatalf("StopTask: HTTP %d: %s", w.Code, w.Body.String())
	}
	f.killTask(t, taskID)

	// Then: the stop stays the caller's, and costs the deployment nothing
	task, aerr := f.h.store.getTask(f.ctx, f.cluster, taskID)
	if aerr != nil || task == nil {
		t.Fatalf("getTask: %v", aerr)
	}
	if task.StopCode != "UserInitiated" {
		t.Errorf("stopCode = %q, want UserInitiated (the die event overwrote it)", task.StopCode)
	}
	if d := primaryDeployment(f.storedService(t)); d == nil || d.FailedTasks != 0 {
		t.Errorf("failedTasks = %#v after a caller-initiated stop, want 0", d)
	}

	// ...and the service places a replacement, because its desired count stands
	if !f.advanceUntil(replacementMaxDelay+2*time.Second, func() bool { return f.taskIsRunning(taskID) }) {
		t.Errorf("expected a replacement task (events:\n%s)", allEvents(f.storedService(t)))
	}
}

// TestCrashLoopingService_recoversWhenATaskStaysUp — the consecutive-failure
// count is not a one-way door: a replacement that actually stays running
// settles the service again and clears it, as AWS does at steady state.
func TestCrashLoopingService_recoversWhenATaskStaysUp(t *testing.T) {
	// Given: a service that has already failed twice
	f := newCrashLoopService(t, "recover-cluster", "recover-svc", nil)
	f.crashOnce(t)
	f.crashOnce(t)
	if d := primaryDeployment(f.storedService(t)); d == nil || d.FailedTasks == 0 {
		t.Fatalf("expected failures to have been counted, got %#v", d)
	}

	// When: the current replacement is left alone long enough to be credible
	if !f.advanceUntil(3*deploymentRecoveryWindow, func() bool { return f.rolloutState() == rolloutCompleted }) {
		t.Errorf("the deployment never completed within 3x the recovery window (events:\n%s)",
			allEvents(f.storedService(t)))
	}

	// Then: the deployment completes and the failure count is cleared
	svc := f.storedService(t)
	d := primaryDeployment(svc)
	if d == nil {
		t.Fatal("expected a PRIMARY deployment")
	}
	if d.RolloutState != rolloutCompleted {
		t.Errorf("rolloutState = %q, want COMPLETED once a replacement stayed up (events:\n%s)",
			d.RolloutState, allEvents(svc))
	}
	if d.FailedTasks != 0 {
		t.Errorf("failedTasks = %d, want 0 at steady state", d.FailedTasks)
	}
	if got := eventCount(svc, "has reached a steady state"); got == 0 {
		t.Errorf("expected a steady-state event once the service recovered (events:\n%s)", allEvents(svc))
	}
}
