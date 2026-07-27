package lambda

// runtime_pool_limits_test.go — instance limits, throttling, and provisioned
// concurrency.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
)

func intPtr(v int) *int { return &v }

// mustAcquire acquires and fails the test on error.
func mustAcquire(t *testing.T, pool *InstancePool, fn *Function) RuntimeInstance {
	t.Helper()
	inst, err := pool.Acquire(context.Background(), fn)
	if err != nil {
		t.Fatalf("acquire %s: %v", fn.Name, err)
	}
	return inst
}

// ─── reserved concurrency ────────────────────────────────────────────────────

func TestAcquire_reservedConcurrencyThrottlesImmediately(t *testing.T) {
	// Given: a function with reserved concurrency of 2, both slots busy.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn", ReservedConcurrency: intPtr(2)}
	mustAcquire(t, pool, fn)
	mustAcquire(t, pool, fn)

	// When: a third invocation arrives with plenty of time on its context.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := pool.Acquire(ctx, fn)

	// Then: it is throttled at once rather than queued — applications are
	// written against AWS returning 429 here, not against a slow invoke.
	throttle, ok := asThrottle(err)
	if !ok {
		t.Fatalf("err = %v, want a throttle", err)
	}
	if throttle.Reason != reasonReservedConcurrency {
		t.Fatalf("reason = %q, want %q", throttle.Reason, reasonReservedConcurrency)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("throttle took %s — reserved concurrency must not queue", elapsed)
	}
}

func TestAcquire_reservedConcurrencyZeroBlocksEveryInvocation(t *testing.T) {
	// Given: a function throttled to zero — the AWS idiom for disabling one.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn", ReservedConcurrency: intPtr(0)}

	// When/Then: nothing gets through.
	if _, err := pool.Acquire(context.Background(), fn); !errors.As(err, new(*throttleError)) {
		t.Fatalf("err = %v, want a throttle", err)
	}
}

func TestAcquire_reservedConcurrencyFreesOnRelease(t *testing.T) {
	// Given: a function with one reserved slot, currently in use.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn", ReservedConcurrency: intPtr(1)}
	inst := mustAcquire(t, pool, fn)

	// When: the invocation finishes.
	pool.Release(context.Background(), inst, true)

	// Then: the next invocation is admitted.
	if _, err := pool.Acquire(context.Background(), fn); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

// ─── instance limits ─────────────────────────────────────────────────────────

func TestAcquire_globalLimitReclaimsAnIdleInstance(t *testing.T) {
	// Given: a two-container budget, both held by idle instances of one
	// function, and a second function that now wants to run.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 2, MaxWarmPerFunction: 2})
	defer pool.Stop()
	idle := &Function{Name: "idle-fn"}
	for range 2 {
		pool.Release(context.Background(), mustAcquire(t, pool, idle), true)
	}

	// When: the other function is invoked with no free slot.
	busy := &Function{Name: "busy-fn"}
	inst, err := pool.Acquire(context.Background(), busy)

	// Then: an idle container is reclaimed to make room rather than the
	// invocation being throttled.
	if err != nil {
		t.Fatalf("acquire: %v — should have reclaimed an idle instance", err)
	}
	if inst == nil {
		t.Fatal("no instance returned")
	}
	pool.mu.Lock()
	idleWarm := len(pool.entries["idle-fn"])
	pool.mu.Unlock()
	if idleWarm != 1 {
		t.Fatalf("idle-fn warm instances = %d, want 1 (one reclaimed)", idleWarm)
	}
}

func TestAcquire_globalLimitQueuesThenThrottles(t *testing.T) {
	// Given: a one-container budget held by a running invocation, so there is
	// nothing idle to reclaim.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 1})
	defer pool.Stop()
	first := &Function{Name: "first"}
	held := mustAcquire(t, pool, first)

	// When: another function is invoked with a short deadline — the invocation
	// queues waiting for capacity.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := pool.Acquire(ctx, &Function{Name: "second"})

	// Then: it is throttled once the deadline passes, the way AWS answers an
	// invocation it cannot place.
	throttle, ok := asThrottle(err)
	if !ok {
		t.Fatalf("err = %v, want a throttle", err)
	}
	if throttle.Reason != reasonConcurrencyLimit {
		t.Fatalf("reason = %q, want %q", throttle.Reason, reasonConcurrencyLimit)
	}
	pool.Release(context.Background(), held, true)
}

func TestAcquire_queuedInvocationProceedsWhenCapacityFrees(t *testing.T) {
	// Given: a one-container budget held by a running invocation.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 1, MaxWarmPerFunction: 1})
	defer pool.Stop()
	held := mustAcquire(t, pool, &Function{Name: "first"})

	// When: a second function queues, then the first invocation finishes.
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Acquire(ctx, &Function{Name: "second"})
		done <- err
	}()
	time.Sleep(100 * time.Millisecond) // let it reach the queue
	pool.Release(context.Background(), held, true)

	// Then: the queued invocation runs instead of waiting out its timeout.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queued invocation failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued invocation was not woken when capacity freed")
	}
}

func TestAcquire_perFunctionLimitCapsOneFunctionOnly(t *testing.T) {
	// Given: a per-function budget of 1 but plenty of global capacity.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxInstancesPerFunction: 1})
	defer pool.Stop()
	hog := &Function{Name: "hog"}
	mustAcquire(t, pool, hog)

	// When: the same function is invoked again with a short deadline, and a
	// different function is invoked too.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, sameErr := pool.Acquire(ctx, hog)
	_, otherErr := pool.Acquire(context.Background(), &Function{Name: "other"})

	// Then: only the saturated function is held back.
	if _, ok := asThrottle(sameErr); !ok {
		t.Fatalf("second invocation of hog: err = %v, want a throttle", sameErr)
	}
	if otherErr != nil {
		t.Fatalf("other function was blocked by hog's limit: %v", otherErr)
	}
}

func TestPoolLimits_perFunctionClampedToGlobal(t *testing.T) {
	// Given: a per-function limit above the global one, which could never be
	// reached.
	limits := PoolLimits{MaxInstances: 3, MaxInstancesPerFunction: 50}.withDefaults()

	// Then: it is clamped so the binding limit is the one reported.
	if limits.MaxInstancesPerFunction != 3 {
		t.Fatalf("MaxInstancesPerFunction = %d, want 3", limits.MaxInstancesPerFunction)
	}
}

// ─── provisioned concurrency ─────────────────────────────────────────────────

// waitForWarm polls until the function has at least n warm instances.
func waitForWarm(t *testing.T, pool *InstancePool, name string, n int, within time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		pool.mu.Lock()
		got := len(pool.entries[name])
		pool.mu.Unlock()
		if got >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSetProvisionedConcurrency_preWarmsEnvironments(t *testing.T) {
	// Given: a function with no warm instances.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn", Environment: map[string]string{"STAGE": "dev"}}

	// When: provisioned concurrency of 3 is configured.
	pool.SetProvisionedConcurrency(fn, 3)

	// Then: three environments are created without waiting for an invocation.
	if got := waitForWarm(t, pool, "fn", 3, 3*time.Second); got != 3 {
		t.Fatalf("warm instances = %d, want 3", got)
	}
	requested, allocated, available, ok := pool.ProvisionedStatus("fn")
	if !ok || requested != 3 || allocated != 3 || available != 3 {
		t.Fatalf("status = (%d,%d,%d,%v), want (3,3,3,true)", requested, allocated, available, ok)
	}
}

func TestSetProvisionedConcurrency_survivesIdleSweep(t *testing.T) {
	// Given: a provisioned function whose environments have gone idle well past
	// the idle TTL.
	clk := clock.NewMock()
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clk, PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn"}
	pool.SetProvisionedConcurrency(fn, 2)
	if got := waitForWarm(t, pool, "fn", 2, 3*time.Second); got != 2 {
		t.Fatalf("warm instances = %d, want 2", got)
	}

	// When: the sweeper runs long after they last served an invocation.
	clk.Add(30 * time.Minute)
	pool.sweep()

	// Then: they are kept — reserving them is what provisioned concurrency
	// buys.
	pool.mu.Lock()
	warm := len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 2 {
		t.Fatalf("warm instances after sweep = %d, want 2", warm)
	}
}

func TestProvisionedConcurrency_replenishesAfterContainerDies(t *testing.T) {
	// Given: a provisioned function with two environments.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	pool.SetProvisionedConcurrency(&Function{Name: "fn"}, 2)
	if got := waitForWarm(t, pool, "fn", 2, 3*time.Second); got != 2 {
		t.Fatalf("warm instances = %d, want 2", got)
	}
	pool.mu.Lock()
	victim := pool.entries["fn"][0].inst.(*poolTestInstance)
	pool.mu.Unlock()
	victim.containerID = "dead-container"

	// When: one container dies out from under the pool.
	pool.handleContainerDied(context.Background(), events.Event{
		Payload: events.DockerContainerPayload{
			ContainerID: "dead-container", Service: "lambda", Action: "die",
		},
	})

	// Then: the reservation is rebuilt rather than silently shrinking.
	if got := waitForWarm(t, pool, "fn", 2, 3*time.Second); got != 2 {
		t.Fatalf("warm instances after death = %d, want 2 (should replenish)", got)
	}
}

func TestClearProvisionedConcurrency_leavesEnvironmentsToIdleOut(t *testing.T) {
	// Given: a provisioned function.
	clk := clock.NewMock()
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clk, PoolLimits{})
	defer pool.Stop()
	pool.SetProvisionedConcurrency(&Function{Name: "fn"}, 2)
	if got := waitForWarm(t, pool, "fn", 2, 3*time.Second); got != 2 {
		t.Fatalf("warm instances = %d, want 2", got)
	}

	// When: the reservation is removed.
	pool.ClearProvisionedConcurrency("fn")

	// Then: the environments are not torn down on the spot — they become
	// ordinary warm instances and age out on the idle sweep.
	pool.mu.Lock()
	warm := len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 2 {
		t.Fatalf("warm instances immediately after clear = %d, want 2", warm)
	}
	if _, _, _, ok := pool.ProvisionedStatus("fn"); ok {
		t.Fatal("reservation still reported after clear")
	}
	clk.Add(30 * time.Minute)
	pool.sweep()
	pool.mu.Lock()
	warm = len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 0 {
		t.Fatalf("warm instances after sweep = %d, want 0", warm)
	}
}

func TestProvisionedConcurrency_notReclaimedForOtherFunctions(t *testing.T) {
	// Given: the whole container budget held by one function's provisioned
	// environments.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 2})
	defer pool.Stop()
	pool.SetProvisionedConcurrency(&Function{Name: "reserved-fn"}, 2)
	if got := waitForWarm(t, pool, "reserved-fn", 2, 3*time.Second); got != 2 {
		t.Fatalf("warm instances = %d, want 2", got)
	}

	// When: another function needs a slot.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := pool.Acquire(ctx, &Function{Name: "other"})

	// Then: the provisioned environments are not cannibalised; the other
	// function is throttled instead.
	if _, ok := asThrottle(err); !ok {
		t.Fatalf("err = %v, want a throttle — provisioned instances must not be reclaimed", err)
	}
	pool.mu.Lock()
	warm := len(pool.entries["reserved-fn"])
	pool.mu.Unlock()
	if warm != 2 {
		t.Fatalf("provisioned instances = %d, want 2", warm)
	}
}

func TestAcquire_concurrentInvocationsRespectGlobalLimit(t *testing.T) {
	// Given: a budget of three containers.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 3, MaxInstancesPerFunction: 3})
	defer pool.Stop()

	// When: six invocations of two functions race, each with a short deadline.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var granted int
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			name := "fn-a"
			if i%2 == 1 {
				name = "fn-b"
			}
			if _, err := pool.Acquire(ctx, &Function{Name: name}); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Then: the limit is never exceeded — no more containers were created than
	// the budget allows.
	if granted > 3 {
		t.Fatalf("granted %d concurrent instances, want at most 3", granted)
	}
	if got := rt.count(); got > 3 {
		t.Fatalf("created %d containers, want at most 3", got)
	}
}

func TestProvisionedConcurrency_isAFloorNotACeiling(t *testing.T) {
	// Given: a function with 2 provisioned environments, both busy.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn"}
	pool.SetProvisionedConcurrency(fn, 2)
	if got := waitForWarm(t, pool, "fn", 2, 3*time.Second); got != 2 {
		t.Fatalf("provisioned environments = %d, want 2", got)
	}
	mustAcquire(t, pool, fn)
	mustAcquire(t, pool, fn)

	// When: a third concurrent invocation arrives, exceeding the reservation.
	// AWS spills over into on-demand capacity here (with a cold start) rather
	// than throttling, because no reserved concurrency caps the function.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inst, err := pool.Acquire(ctx, fn)

	// Then: it gets an on-demand environment.
	if err != nil {
		t.Fatalf("acquire beyond provisioned concurrency: %v — provisioned concurrency must not cap the function", err)
	}
	if inst == nil {
		t.Fatal("no instance returned")
	}
	if got := rt.count(); got != 3 {
		t.Fatalf("environments created = %d, want 3 (2 provisioned + 1 spillover)", got)
	}
}

func TestProvisionedConcurrency_replenishesWhileOnDemandInstancesExist(t *testing.T) {
	// Given: a function running purely on-demand instances, which then gets a
	// provisioned concurrency reservation.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn"}
	for range 3 {
		mustAcquire(t, pool, fn) // three in-flight on-demand invocations
	}

	// When: 2 provisioned environments are reserved.
	pool.SetProvisionedConcurrency(fn, 2)

	// Then: two *new* environments are created for the reservation. Counting
	// the on-demand instances towards the target would leave the reservation
	// permanently unfulfilled on a busy function.
	if got := waitForWarm(t, pool, "fn", 2, 3*time.Second); got != 2 {
		t.Fatalf("provisioned environments = %d, want 2", got)
	}
	_, allocated, _, ok := pool.ProvisionedStatus("fn")
	if !ok || allocated != 2 {
		t.Fatalf("allocated = %d (ok=%v), want 2", allocated, ok)
	}
	if got := rt.count(); got != 5 {
		t.Fatalf("environments created = %d, want 5 (3 on-demand + 2 provisioned)", got)
	}
}

func TestProvisionedConcurrency_spilloverInstancesStillAgeOut(t *testing.T) {
	// Given: a provisioned function whose burst created spillover instances,
	// all now idle.
	clk := clock.NewMock()
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clk, PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "fn"}
	pool.SetProvisionedConcurrency(fn, 2)
	if got := waitForWarm(t, pool, "fn", 2, 3*time.Second); got != 2 {
		t.Fatalf("provisioned environments = %d, want 2", got)
	}
	spill := []RuntimeInstance{
		mustAcquire(t, pool, fn), mustAcquire(t, pool, fn),
		mustAcquire(t, pool, fn), mustAcquire(t, pool, fn),
	}
	for _, inst := range spill {
		pool.Release(context.Background(), inst, true)
	}

	// When: the idle sweep runs long after.
	clk.Add(30 * time.Minute)
	pool.sweep()

	// Then: exactly the reservation survives — the spillover ages out, the
	// provisioned environments do not.
	pool.mu.Lock()
	warm := len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 2 {
		t.Fatalf("warm instances after sweep = %d, want 2 (the reservation)", warm)
	}
	_, allocated, available, _ := pool.ProvisionedStatus("fn")
	if allocated != 2 || available != 2 {
		t.Fatalf("allocated/available = %d/%d, want 2/2", allocated, available)
	}
}
