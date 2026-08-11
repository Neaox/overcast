package lambda

// invoker_async_test.go pins the accept/execute split of ServiceInvoker.
//
// InvokeEvent is the internal seam S3 notifications, Scheduler targets and SNS
// subscription fan-out reach Lambda through. It has to mean the same thing as
// an HTTP `InvocationType=Event` invoke, because it is the same API: the caller
// finds out synchronously whether the event was *accepted*, and everything
// after that — cold starts, throttles, handler exceptions — belongs to Lambda.
//
// The case that made the two paths disagree is a throttle. HTTP answers 202 and
// retries internally; InvokeEvent used to hand the throttle back, and SNS reads
// a non-nil error as a delivery failure and dead-letters the message. A
// function reserved to zero concurrency — the documented way to switch a
// function off — therefore lost notifications that AWS would have retried.

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// ---- Fixture ---------------------------------------------------------------

// asyncFixture is a Handler and the ServiceInvoker that shares its async
// machinery, wired the way service.New wires them.
type asyncFixture struct {
	h   *Handler
	inv *ServiceInvoker
	clk *clock.Mock
	fn  *Function
}

// newAsyncFixture builds the pair over rt, with one Active function seeded.
func newAsyncFixture(t *testing.T, rt Runtime) *asyncFixture {
	t.Helper()
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	tracker := newInstanceTracker(clk, zap.NewNop())
	t.Cleanup(tracker.Stop)
	registry := newRuntimeRegistry([]Runtime{rt})

	h := &Handler{
		cfg:      &config.Config{Region: "us-east-1", AccountID: "000000000000"},
		log:      serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk:      clk,
		ls:       ls,
		tracker:  tracker,
		runtimes: registry,
	}
	fn := &Function{
		Name:       "async-fn",
		ARN:        "arn:aws:lambda:us-east-1:000000000000:function:async-fn",
		Runtime:    "nodejs22.x",
		Handler:    "index.handler",
		Timeout:    3,
		MemorySize: 128,
		State:      "Active",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("seed function: %s", aerr.Message)
	}

	f := &asyncFixture{
		h:   h,
		inv: newServiceInvoker(h, ls, registry, zap.NewNop(), tracker),
		clk: clk,
		fn:  fn,
	}
	// Every test here starts work on h.asyncWg; draining it keeps goroutines
	// from outliving the test.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.StopAsync(ctx)
	})
	return f
}

// reserveConcurrency sets the function's reserved concurrency and persists it.
func (f *asyncFixture) reserveConcurrency(t *testing.T, n int) {
	t.Helper()
	f.fn.ReservedConcurrency = &n
	if aerr := f.h.ls.putFunction(context.Background(), f.fn); aerr != nil {
		t.Fatalf("put function: %s", aerr.Message)
	}
}

// ---- Fake runtimes ---------------------------------------------------------

// scriptedThrottleRuntime refuses the first throttleCount acquires with the
// throttle the instance pool raises for reserved concurrency, then succeeds.
// It records every attempt so the internal retry is observable.
type scriptedThrottleRuntime struct {
	throttleCount int

	mu       sync.Mutex
	attempts int
	invoked  chan struct{} // closed by Acquire on the first success
	once     sync.Once
}

func newScriptedThrottleRuntime(throttleCount int) *scriptedThrottleRuntime {
	return &scriptedThrottleRuntime{throttleCount: throttleCount, invoked: make(chan struct{})}
}

func (r *scriptedThrottleRuntime) CanHandle(string) bool { return true }

func (r *scriptedThrottleRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	r.mu.Lock()
	r.attempts++
	throttled := r.attempts <= r.throttleCount
	r.mu.Unlock()
	if throttled {
		return nil, errThrottled(reasonReservedConcurrency)
	}
	r.once.Do(func() { close(r.invoked) })
	return newPoolTestInstance(fn.Name), nil
}

func (r *scriptedThrottleRuntime) Release(context.Context, RuntimeInstance, bool) {}

func (r *scriptedThrottleRuntime) attemptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts
}

// blockingRuntime parks in Acquire until it is released, standing in for a cold
// start that takes seconds.
type blockingRuntime struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingRuntime() *blockingRuntime {
	return &blockingRuntime{entered: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingRuntime) CanHandle(string) bool { return true }

func (r *blockingRuntime) Acquire(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
		return newPoolTestInstance(fn.Name), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *blockingRuntime) Release(context.Context, RuntimeInstance, bool) {}

// ---- Tests -----------------------------------------------------------------

// pumpClockUntil advances the mock clock in backoff-sized steps until done is
// closed, or fails after a wall-clock deadline.
//
// The retry sleeps on the injected clock, so the test has to drive it — but the
// goroutine registers each timer only after the acquire before it has been
// refused, which a single batch of Add calls can easily outrun. Advancing in a
// loop removes the ordering assumption: an Add with no timer pending simply
// does nothing, and the next one round the loop catches the timer that appears.
func pumpClockUntil(t *testing.T, clk *clock.Mock, done <-chan struct{}, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-done:
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		clk.Add(asyncThrottleBackoff)
		time.Sleep(time.Millisecond)
	}
}

func TestInvokeEvent_throttleIsRetriedInternallyNotReturnedToTheCaller(t *testing.T) {
	// Given: a function whose first two acquires are throttled, exactly as the
	// pool throttles one reserved to fewer slots than it has work for.
	rt := newScriptedThrottleRuntime(2)
	f := newAsyncFixture(t, rt)

	// When: it is invoked through the async seam.
	err := f.inv.InvokeEvent(context.Background(), f.fn.ARN, []byte(`{"hello":"world"}`))

	// Then: the caller is told the event was accepted. SNS reads a non-nil
	// error here as a delivery failure and dead-letters the notification, so a
	// throttle surfacing to the caller loses a message AWS would have retried.
	if err != nil {
		t.Fatalf("InvokeEvent returned %v, want nil — a throttle must not reach the caller", err)
	}

	// And: the throttle is retried internally, on the injected clock, until it
	// clears — the event runs rather than being dropped.
	pumpClockUntil(t, f.clk, rt.invoked, "the throttled event to be retried into the function")
	if got := rt.attemptCount(); got < 3 {
		t.Errorf("acquire attempts = %d, want at least 3 (two throttles then a success)", got)
	}
}

func TestInvokeEvent_returnsBeforeTheFunctionRuns(t *testing.T) {
	// Given: a function whose cold start parks — the seconds-long container
	// start that used to be paid on the caller's goroutine.
	rt := newBlockingRuntime()
	f := newAsyncFixture(t, rt)

	// When: it is invoked through the async seam.
	done := make(chan error, 1)
	go func() { done <- f.inv.InvokeEvent(context.Background(), f.fn.ARN, []byte(`{}`)) }()

	// Then: the call returns while the cold start is still in progress. S3 and
	// Scheduler invoke from the events bus worker pool; an inline cold start
	// parks one of its sixteen workers for the duration.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("InvokeEvent returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("InvokeEvent blocked on the cold start instead of accepting the event")
	}

	// And: the execution really was handed off, not skipped.
	select {
	case <-rt.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the accepted event never reached the runtime")
	}
	close(rt.release)
}

func TestInvokeEvent_reservedConcurrencyZeroDoesNotDeadLetter(t *testing.T) {
	// Given: a function switched off with a zero reservation — AWS's documented
	// idiom for disabling one — behind the real admission gate.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	t.Cleanup(pool.Stop)
	f := newAsyncFixture(t, pool)
	pool.observer = f.h.tracker
	f.reserveConcurrency(t, 0)

	// When: a notification is delivered to it.
	err := f.inv.InvokeEvent(context.Background(), f.fn.ARN, []byte(`{}`))

	// Then: the event is still accepted. What Lambda does with it afterwards —
	// drop it, or send it to the function's own DeadLetterConfig target (see
	// dead_letter_test.go) — is Lambda's business; what matters here is that it
	// is not bounced back to SNS to be dead-lettered against the subscription's
	// RedrivePolicy.
	if err != nil {
		t.Fatalf("InvokeEvent returned %v, want nil — a reserved-concurrency throttle is Lambda's to retry", err)
	}
}

func TestInvokeEvent_afterShutdownIsRefusedRatherThanOrphaned(t *testing.T) {
	// Given: a Lambda service that has already drained its async invocations.
	// This is reachable in production — Lambda stops before S3, Scheduler and
	// the event bus, any of which can still raise an event afterwards.
	rt := newScriptedThrottleRuntime(0)
	f := newAsyncFixture(t, rt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.h.StopAsync(ctx)

	// When: a late event arrives.
	err := f.inv.InvokeEvent(context.Background(), f.fn.ARN, []byte(`{}`))

	// Then: it is refused. Accepting it would start a goroutine on a WaitGroup
	// nothing is waiting on, against a pool and tracker already torn down.
	if err == nil {
		t.Fatal("InvokeEvent returned nil after shutdown, want a refusal the caller can act on")
	}
	if rt.attemptCount() != 0 {
		t.Errorf("acquire attempts = %d, want 0 — nothing should run after the drain", rt.attemptCount())
	}
}

func TestInvokeEvent_rejectsWhatLambdaRejectsSynchronously(t *testing.T) {
	// A caller only learns about the failures real Lambda answers an
	// InvocationType=Event call with. Those must still come back as errors, or
	// SNS has nothing to dead-letter.
	tests := []struct {
		name  string
		arn   string
		state string
	}{
		{name: "function not found", arn: "arn:aws:lambda:us-east-1:000000000000:function:absent"},
		{name: "function pending", arn: "", state: "Pending"},
		{name: "function failed", arn: "", state: "Failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAsyncFixture(t, newScriptedThrottleRuntime(0))
			arn := tc.arn
			if arn == "" {
				f.fn.State = tc.state
				if aerr := f.h.ls.putFunction(context.Background(), f.fn); aerr != nil {
					t.Fatalf("put function: %s", aerr.Message)
				}
				arn = f.fn.ARN
			}
			if err := f.inv.InvokeEvent(context.Background(), arn, []byte(`{}`)); err == nil {
				t.Fatal("InvokeEvent returned nil, want an error the caller can dead-letter")
			}
		})
	}
}
