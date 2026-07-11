package lambda

import (
	"context"
	"testing"
	"time"
)

func TestContainerRuntimeColdStartSlot_boundsConcurrency(t *testing.T) {
	// Given: a runtime with two cold-start slots already occupied.
	runtime := &ContainerRuntime{coldStartSem: make(chan struct{}, 2)}
	releaseFirst, err := runtime.acquireColdStartSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire first slot: %v", err)
	}
	releaseSecond, err := runtime.acquireColdStartSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire second slot: %v", err)
	}
	defer releaseSecond()

	// When: a third caller waits for a slot.
	acquired := make(chan func(), 1)
	go func() {
		release, acquireErr := runtime.acquireColdStartSlot(context.Background())
		if acquireErr != nil {
			t.Errorf("acquire third slot: %v", acquireErr)
			return
		}
		acquired <- release
	}()

	select {
	case release := <-acquired:
		release()
		t.Fatal("third acquire completed before a slot was released")
	case <-time.After(20 * time.Millisecond):
	}

	// Then: releasing a slot lets the queued caller proceed.
	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("third acquire did not complete after a slot was released")
	}
}

func TestContainerRuntimeColdStartSlot_contextCancelled(t *testing.T) {
	// Given: a runtime with its only cold-start slot occupied.
	runtime := &ContainerRuntime{coldStartSem: make(chan struct{}, 1)}
	release, err := runtime.acquireColdStartSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire slot: %v", err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When: another caller tries to wait with a cancelled context.
	_, err = runtime.acquireColdStartSlot(ctx)

	// Then: the wait returns promptly with the context error.
	if err != context.Canceled {
		t.Fatalf("acquire error = %v, want context.Canceled", err)
	}
}

func TestContainerInstanceAwaitReady_contextCancelled(t *testing.T) {
	// Given: a container instance whose runtime has not polled /next yet.
	ready := make(chan struct{})
	inst := &containerInstance{readyCh: ready}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When: readiness is awaited with a cancelled context.
	err := inst.AwaitReady(ctx)

	// Then: the context error is returned.
	if err != context.Canceled {
		t.Fatalf("AwaitReady error = %v, want context.Canceled", err)
	}
}

func TestContainerInstanceAwaitReady_ready(t *testing.T) {
	// Given: a container instance whose ready channel is already closed.
	ready := make(chan struct{})
	close(ready)
	inst := &containerInstance{readyCh: ready}

	// When: readiness is awaited.
	err := inst.AwaitReady(context.Background())

	// Then: it returns immediately.
	if err != nil {
		t.Fatalf("AwaitReady: %v", err)
	}
}
