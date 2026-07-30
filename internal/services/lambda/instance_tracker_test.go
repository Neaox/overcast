package lambda

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
)

func TestInstanceTracker_releasePublishesSucceededOutcome(t *testing.T) {
	// Given: a tracker with an attached bus and one acquired invocation
	clk := clock.NewMock()
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)

	bus := events.NewBus()
	t.Cleanup(bus.Stop)
	tracker.SetBus(bus)

	released := make(chan events.LambdaInstancePayload, 1)
	cancel := bus.Subscribe(events.LambdaInstanceReleased, func(_ context.Context, e events.Event) {
		if payload, ok := e.Payload.(events.LambdaInstancePayload); ok {
			released <- payload
		}
	})
	t.Cleanup(cancel)

	inv := trackedFor(tracker, "my-fn", []byte(`{"hello":"world"}`))

	// When: the invocation is released as succeeded
	clk.Add(1 * time.Second)
	inv.Finish(true, "")

	// Then: the release payload contains the explicit succeeded outcome
	select {
	case payload := <-released:
		if payload.LastInvocationStatus != "succeeded" {
			t.Fatalf("expected LastInvocationStatus=succeeded, got %q", payload.LastInvocationStatus)
		}
		if payload.LastInvocationError != "" {
			t.Fatalf("expected empty LastInvocationError, got %q", payload.LastInvocationError)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for LambdaInstanceReleased event")
	}
}

func TestInstanceTracker_releasePublishesFailedOutcome(t *testing.T) {
	// Given: a tracker with an attached bus and one acquired invocation
	clk := clock.NewMock()
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)

	bus := events.NewBus()
	t.Cleanup(bus.Stop)
	tracker.SetBus(bus)

	released := make(chan events.LambdaInstancePayload, 1)
	cancel := bus.Subscribe(events.LambdaInstanceReleased, func(_ context.Context, e events.Event) {
		if payload, ok := e.Payload.(events.LambdaInstancePayload); ok {
			released <- payload
		}
	})
	t.Cleanup(cancel)

	inv := trackedFor(tracker, "my-fn", []byte(`{"hello":"world"}`))

	// When: the invocation is released as failed
	clk.Add(1 * time.Second)
	inv.Finish(false, "task timed out")

	// Then: the release payload contains failed status and failure reason
	select {
	case payload := <-released:
		if payload.LastInvocationStatus != "failed" {
			t.Fatalf("expected LastInvocationStatus=failed, got %q", payload.LastInvocationStatus)
		}
		if payload.LastInvocationError != "task timed out" {
			t.Fatalf("expected LastInvocationError=task timed out, got %q", payload.LastInvocationError)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for LambdaInstanceReleased event")
	}
}

func TestInstanceTracker_readyTransitionsToInitializing(t *testing.T) {
	// Given: a tracker with an acquired (starting) instance.
	clk := clock.NewMock()
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)

	bus := events.NewBus()
	t.Cleanup(bus.Stop)
	tracker.SetBus(bus)

	ready := make(chan events.LambdaInstancePayload, 1)
	cancel := bus.Subscribe(events.LambdaInstanceReady, func(_ context.Context, e events.Event) {
		if payload, ok := e.Payload.(events.LambdaInstancePayload); ok {
			ready <- payload
		}
	})
	t.Cleanup(cancel)

	inv := trackedFor(tracker, "my-fn", nil)

	// When: Ready is called (container is up, RIC not yet connected).
	inv.Ready()

	// Then: status transitions to "initializing", not "running".
	select {
	case payload := <-ready:
		if payload.Status != "initializing" {
			t.Fatalf("expected Status=initializing, got %q", payload.Status)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for LambdaInstanceReady event")
	}

	// And the snapshot confirms the status.
	instances := tracker.Instances()
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	if instances[0].Status != "initializing" {
		t.Fatalf("expected snapshot Status=initializing, got %q", instances[0].Status)
	}
}

func TestInstanceTracker_runtimeConnectedTransitionsToRunning(t *testing.T) {
	// Given: a tracker with an initializing instance.
	clk := clock.NewMock()
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)

	bus := events.NewBus()
	t.Cleanup(bus.Stop)
	tracker.SetBus(bus)

	init := make(chan events.LambdaInstancePayload, 1)
	cancel := bus.Subscribe(events.LambdaInstanceInitializing, func(_ context.Context, e events.Event) {
		if payload, ok := e.Payload.(events.LambdaInstancePayload); ok {
			init <- payload
		}
	})
	t.Cleanup(cancel)

	inv := trackedFor(tracker, "my-fn", nil)
	inv.Ready() // → initializing

	// When: RuntimeConnected is called (RIC polled first /next).
	inv.Running()

	// Then: status transitions to "running".
	select {
	case payload := <-init:
		if payload.Status != "running" {
			t.Fatalf("expected Status=running, got %q", payload.Status)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for LambdaInstanceInitializing event")
	}
}

func TestInstanceTracker_fullColdStartLifecycle(t *testing.T) {
	// Verify the full status progression: starting → initializing → running → idle.
	clk := clock.NewMock()
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)

	inv := trackedFor(tracker, "my-fn", nil) // → starting
	snap := tracker.Instances()
	if snap[0].Status != "starting" {
		t.Fatalf("after Acquire: expected starting, got %q", snap[0].Status)
	}

	inv.Ready() // → initializing
	snap = tracker.Instances()
	if snap[0].Status != "initializing" {
		t.Fatalf("after Ready: expected initializing, got %q", snap[0].Status)
	}

	inv.Running() // → running
	snap = tracker.Instances()
	if snap[0].Status != "running" {
		t.Fatalf("after RuntimeConnected: expected running, got %q", snap[0].Status)
	}

	inv.Finish(true, "") // → idle
	snap = tracker.Instances()
	if snap[0].Status != "idle" {
		t.Fatalf("after Release: expected idle, got %q", snap[0].Status)
	}
}

// trackedFor opens a tracker record for one invocation and binds it to a stub
// execution environment, mirroring what the invoke paths do.
func trackedFor(t *instanceTracker, functionName string, payload []byte) *trackedInvocation {
	inv := t.Begin(functionName, payload)
	inv.Bind(newPoolTestInstance(functionName))
	return inv
}

func TestInstanceTracker_instancesReportContainerStats(t *testing.T) {
	// Given: a tracker whose stats function returns a fixed memory usage and
	// CPU counters that grow by 0.2 s of container time per 1 s of host time.
	clk := clock.NewMock()
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)

	var calls atomic.Int64
	tracker.SetStatsFunc(func(_ context.Context, containerID string) (docker.ContainerStats, error) {
		if containerID != "container-1" {
			t.Errorf("stats sampled for unexpected container %q", containerID)
		}
		n := calls.Add(1)
		return docker.ContainerStats{
			MemoryUsageBytes: 96 * 1024 * 1024,
			CPUTotalUsage:    uint64(n) * 200_000_000,
			SystemCPUUsage:   uint64(n) * 1_000_000_000,
			OnlineCPUs:       2,
		}, nil
	})

	inv := tracker.Begin("my-fn", nil)
	inst := newPoolTestInstance("my-fn")
	inst.containerID = "container-1"
	inv.Bind(inst)

	// When: the snapshot is served, twice in quick succession, then again
	// after the refresh throttle window has passed.
	first := tracker.Instances()
	throttled := tracker.Instances()
	callsAfterThrottle := calls.Load()
	clk.Add(2 * time.Second)
	second := tracker.Instances()

	// Then: memory comes from the first sample; CPU% needs a delta between two
	// samples, so it appears on the later snapshot: 0.2 s CPU over 1 s host
	// time on 2 CPUs = 40 % by the docker-CLI formula.
	if len(first) != 1 || len(throttled) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 instance per snapshot, got %d/%d/%d", len(first), len(throttled), len(second))
	}
	if first[0].MemoryUsedMB != 96 {
		t.Fatalf("first MemoryUsedMB = %d, want 96", first[0].MemoryUsedMB)
	}
	if first[0].CPUPercent != 0 {
		t.Fatalf("first CPUPercent = %v, want 0 (no delta yet)", first[0].CPUPercent)
	}
	if callsAfterThrottle != 1 {
		t.Fatalf("stats calls after back-to-back snapshots = %d, want 1 (throttled)", callsAfterThrottle)
	}
	if second[0].CPUPercent != 40 {
		t.Fatalf("second CPUPercent = %v, want 40", second[0].CPUPercent)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("stats calls after throttle window = %d, want 2", got)
	}
}

func TestInstanceTracker_statsErrorKeepsPreviousValues(t *testing.T) {
	// Given: a tracker whose stats function succeeds once and then fails.
	clk := clock.NewMock()
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)

	var calls atomic.Int64
	tracker.SetStatsFunc(func(_ context.Context, _ string) (docker.ContainerStats, error) {
		if calls.Add(1) > 1 {
			return docker.ContainerStats{}, context.DeadlineExceeded
		}
		return docker.ContainerStats{MemoryUsageBytes: 64 * 1024 * 1024}, nil
	})

	inv := tracker.Begin("my-fn", nil)
	inst := newPoolTestInstance("my-fn")
	inst.containerID = "container-1"
	inv.Bind(inst)

	// When: a successful sample is followed by a failing one.
	first := tracker.Instances()
	clk.Add(2 * time.Second)
	second := tracker.Instances()

	// Then: the failed sample keeps the previous value instead of zeroing it.
	if first[0].MemoryUsedMB != 64 {
		t.Fatalf("first MemoryUsedMB = %d, want 64", first[0].MemoryUsedMB)
	}
	if second[0].MemoryUsedMB != 64 {
		t.Fatalf("MemoryUsedMB after failed sample = %d, want 64 (kept)", second[0].MemoryUsedMB)
	}
}
