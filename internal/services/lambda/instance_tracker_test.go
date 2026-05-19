package lambda

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
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

	tracker.Acquire("my-fn", []byte(`{"hello":"world"}`))

	// When: the invocation is released as succeeded
	clk.Add(1 * time.Second)
	tracker.Release("my-fn", true, "")

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

	tracker.Acquire("my-fn", []byte(`{"hello":"world"}`))

	// When: the invocation is released as failed
	clk.Add(1 * time.Second)
	tracker.Release("my-fn", false, "task timed out")

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

	tracker.Acquire("my-fn", nil)

	// When: Ready is called (container is up, RIC not yet connected).
	tracker.Ready("my-fn")

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

	tracker.Acquire("my-fn", nil)
	tracker.Ready("my-fn") // → initializing

	// When: RuntimeConnected is called (RIC polled first /next).
	tracker.RuntimeConnected("my-fn")

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

	tracker.Acquire("my-fn", nil) // → starting
	snap := tracker.Instances()
	if snap[0].Status != "starting" {
		t.Fatalf("after Acquire: expected starting, got %q", snap[0].Status)
	}

	tracker.Ready("my-fn") // → initializing
	snap = tracker.Instances()
	if snap[0].Status != "initializing" {
		t.Fatalf("after Ready: expected initializing, got %q", snap[0].Status)
	}

	tracker.RuntimeConnected("my-fn") // → running
	snap = tracker.Instances()
	if snap[0].Status != "running" {
		t.Fatalf("after RuntimeConnected: expected running, got %q", snap[0].Status)
	}

	tracker.Release("my-fn", true, "") // → idle
	snap = tracker.Instances()
	if snap[0].Status != "idle" {
		t.Fatalf("after Release: expected idle, got %q", snap[0].Status)
	}
}
