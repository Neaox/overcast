package lambda

import (
	"context"
	"testing"
	"time"
)

// runtime_api_release_test.go pins the two-phase hand-off that keeps START in
// front of the handler's own output.
//
// The invoke path writes an invocation's START record into the tail buffer
// between PrepareInvocation and ReleaseInvocation. That only works if a
// prepared invocation is genuinely invisible: the moment a parked GET /next
// can return it, the handler can be printing — and its lines, framed by the
// in-container init and ingested on another goroutine, would race the START
// synth exactly the way TestInvoke_logTail_exactAttribution caught in CI
// (issue #1437). These tests assert visibility semantics, not timing, so they
// are deterministic in outcome however the goroutines interleave.

// TestRuntimeAPI_preparedInvocationIsInvisibleUntilReleased pins that a
// runtime asking for work is never handed an invocation that has not been
// released — even one prepared earlier than the work it is handed instead.
func TestRuntimeAPI_preparedInvocationIsInvisibleUntilReleased(t *testing.T) {
	srv, addr := newWaiterTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Given: an invocation that is prepared but not released…
	prepared, _ := srv.PrepareInvocation(waiterTestARN, []byte(`{"n":"prepared"}`), time.Now().Add(30*time.Second))
	// …and a runtime waiting for work.
	poll := pollNext(t, ctx, addr)

	// When: a second invocation goes through the one-call form.
	released, _ := srv.SubmitInvocation(waiterTestARN, []byte(`{"n":"released"}`), time.Now().Add(30*time.Second))

	// Then: the runtime gets the released one, never the prepared one — the
	// prepared invocation does not exist as far as GET /next is concerned.
	select {
	case id := <-poll:
		if id != released {
			t.Fatalf("GET /next was handed %q, want the released invocation %q (prepared, unreleased: %q)", id, released, prepared)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GET /next was never answered")
	}

	// And when the prepared invocation is released, it becomes ordinary work.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	poll2 := pollNext(t, ctx2, addr)
	srv.ReleaseInvocation(prepared)
	select {
	case id := <-poll2:
		if id != prepared {
			t.Fatalf("GET /next was handed %q after release, want %q", id, prepared)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GET /next was never answered after the release")
	}
}

// TestRuntimeAPI_releasingACancelledInvocationIsANoOp pins the compose with
// CancelInvocation: a caller that gives up between prepare and release must
// not resurrect the invocation by releasing it.
func TestRuntimeAPI_releasingACancelledInvocationIsANoOp(t *testing.T) {
	srv, addr := newWaiterTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	prepared, resultCh := srv.PrepareInvocation(waiterTestARN, []byte(`{}`), time.Now().Add(30*time.Second))
	srv.CancelInvocation(prepared)
	srv.ReleaseInvocation(prepared)

	// The caller's channel is closed, as cancel promises.
	select {
	case _, ok := <-resultCh:
		if ok {
			t.Fatal("a cancelled invocation delivered a result")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not close the result channel")
	}

	// And the runtime is handed the next real invocation, not the ghost.
	poll := pollNext(t, ctx, addr)
	next, _ := srv.SubmitInvocation(waiterTestARN, []byte(`{}`), time.Now().Add(30*time.Second))
	select {
	case id := <-poll:
		if id != next {
			t.Fatalf("GET /next was handed %q, want %q — a cancelled invocation was resurrected by its release", id, next)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GET /next was never answered")
	}
}
