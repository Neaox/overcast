package lambda

// runtime_pool_memory_test.go — the aggregate memory budget at admission.
//
// Each container is hard-capped at its function's MemorySize with swap
// disabled, so Σ MemorySize over live containers is a real bound on what
// Lambda can take from the host, not an estimate. The pool admits a new
// container only while that sum stays inside the budget, using the same
// reclaim → queue → throttle-at-timeout ladder as the instance limits.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/internal/clock"
)

func TestAcquire_memoryBudgetQueuesThenThrottles(t *testing.T) {
	// Given: a 256 MB budget fully held by a running invocation, with plenty of
	// instance-count headroom, so memory is the binding constraint.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 256})
	defer pool.Stop()
	mustAcquire(t, pool, &Function{Name: "big", MemorySize: 200})

	// When: another function needs 200 MB with a short deadline and nothing
	// idle to reclaim.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := pool.Acquire(ctx, &Function{Name: "other", MemorySize: 200})

	// Then: it queues and is throttled at the deadline with the same reason the
	// instance limit uses — the budget is host protection, not a new AWS error.
	throttle, ok := asThrottle(err)
	if !ok {
		t.Fatalf("err = %v, want a throttle", err)
	}
	if throttle.Reason != reasonConcurrencyLimit {
		t.Fatalf("reason = %q, want %q", throttle.Reason, reasonConcurrencyLimit)
	}
}

func TestAcquire_memoryBudgetReclaimsIdleInstance(t *testing.T) {
	// Given: a 256 MB budget held entirely by one idle 200 MB container.
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 256})
	defer pool.Stop()
	idle := &Function{Name: "idle-fn", MemorySize: 200}
	pool.Release(context.Background(), mustAcquire(t, pool, idle), true)

	// When: a different function needs 200 MB.
	inst, err := pool.Acquire(context.Background(), &Function{Name: "busy-fn", MemorySize: 200})

	// Then: the idle container is reclaimed to free its memory rather than the
	// invocation being throttled.
	if err != nil {
		t.Fatalf("acquire: %v — should have reclaimed the idle instance", err)
	}
	if inst == nil {
		t.Fatal("no instance returned")
	}
	pool.mu.Lock()
	idleWarm := len(pool.entries["idle-fn"])
	pool.mu.Unlock()
	if idleWarm != 0 {
		t.Fatalf("idle-fn warm instances = %d, want 0 (reclaimed for memory)", idleWarm)
	}
}

func TestAcquire_memoryBudgetCountsAWSDefaultMemorySize(t *testing.T) {
	// Given: a 200 MB budget and functions that never set MemorySize — AWS
	// gives those 128 MB, and so does the container the pool creates.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 200})
	defer pool.Stop()
	mustAcquire(t, pool, &Function{Name: "first"})

	// When: a second default-sized function arrives with a short deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := pool.Acquire(ctx, &Function{Name: "second"})

	// Then: 128 + 128 > 200, so it is throttled — a zero MemorySize must not
	// be admitted as costing nothing.
	if _, ok := asThrottle(err); !ok {
		t.Fatalf("err = %v, want a throttle (2 × 128 MB default exceeds 200 MB)", err)
	}
}

func TestAcquire_queuedInvocationProceedsWhenMemoryFrees(t *testing.T) {
	// Given: the whole budget held by a running invocation.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 256})
	defer pool.Stop()
	held := mustAcquire(t, pool, &Function{Name: "first", MemorySize: 200})

	// When: a second function queues on memory, then the first finishes.
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Acquire(ctx, &Function{Name: "second", MemorySize: 200})
		done <- err
	}()
	time.Sleep(100 * time.Millisecond) // let it reach the queue
	pool.Release(context.Background(), held, true)

	// Then: the queued invocation reclaims the now-idle container's memory and
	// runs instead of waiting out its timeout.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queued invocation failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued invocation was not woken when memory freed")
	}
}

func TestRelease_overMemoryHighWaterClosesInsteadOfPooling(t *testing.T) {
	// Given: two running 200 MB invocations against a 420 MB budget — the pool
	// is above the 90% high-water mark (378 MB).
	rt := &countingColdStartRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 420})
	defer pool.Stop()
	first := mustAcquire(t, pool, &Function{Name: "a", MemorySize: 200})
	second := mustAcquire(t, pool, &Function{Name: "b", MemorySize: 200})

	// When: the first invocation finishes while the pool is over high water,
	// and the second finishes after the first's release brought it back under.
	pool.Release(context.Background(), first, true)
	pool.Release(context.Background(), second, true)

	// Then: the first container is destroyed instead of being kept warm — under
	// memory pressure surplus warmth is the first thing to give — while the
	// second, released under the high-water mark, is pooled as usual.
	if got := first.(*poolTestInstance).CloseCalls(); got != 1 {
		t.Fatalf("first close calls = %d, want 1 (over high water)", got)
	}
	if got := second.(*poolTestInstance).CloseCalls(); got != 0 {
		t.Fatalf("second close calls = %d, want 0 (under high water)", got)
	}
	pool.mu.Lock()
	warmA, warmB := len(pool.entries["a"]), len(pool.entries["b"])
	pool.mu.Unlock()
	if warmA != 0 || warmB != 1 {
		t.Fatalf("warm a/b = %d/%d, want 0/1", warmA, warmB)
	}
}

func TestRelease_overMemoryHighWaterKeepsProvisionedEnvironments(t *testing.T) {
	// Given: a provisioned function whose reservation alone puts the pool over
	// the high-water mark.
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 420})
	defer pool.Stop()
	fn := &Function{Name: "fn", MemorySize: 200}
	pool.SetProvisionedConcurrency(fn, 2)
	if got := waitForWarm(t, pool, "fn", 2, 3*time.Second); got != 2 {
		t.Fatalf("provisioned environments = %d, want 2", got)
	}

	// When: an invocation served by a provisioned environment finishes.
	inst := mustAcquire(t, pool, fn)
	pool.Release(context.Background(), inst, true)

	// Then: the environment goes back to the warm set — memory pressure never
	// eats a reservation, that is what provisioning buys.
	pool.mu.Lock()
	warm := len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 2 {
		t.Fatalf("warm instances = %d, want 2 (reservation intact)", warm)
	}
}

func TestAcquire_memoryBudgetWarnsOnceNamingTheEnvVar(t *testing.T) {
	// Given: a pool logging into an observer, with its budget exhausted.
	core, logs := observer.New(zap.WarnLevel)
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.New(core), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 256})
	defer pool.Stop()
	mustAcquire(t, pool, &Function{Name: "big", MemorySize: 200})

	// When: two invocations in a row queue on the exhausted budget.
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, _ = pool.Acquire(ctx, &Function{Name: "other", MemorySize: 200})
		cancel()
	}

	// Then: one warning names the env var to raise — not one per invocation.
	var warns int
	for _, entry := range logs.All() {
		for _, f := range entry.Context {
			if f.Key == "env_var" && f.String == "LAMBDA_MAX_MEMORY_MB" {
				warns++
			}
		}
	}
	if warns != 1 {
		t.Fatalf("budget warnings naming LAMBDA_MAX_MEMORY_MB = %d, want exactly 1", warns)
	}
}

func TestRelease_memoryPressureRegimeLogsEntryAndExitExactlyOnce(t *testing.T) {
	// Given: a pool logging into an observer at Info level, with a 420 MB
	// budget (high water 378 MB) and two 200 MB invocations in flight.
	core, logs := observer.New(zap.InfoLevel)
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.New(core), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 420})
	defer pool.Stop()
	a := mustAcquire(t, pool, &Function{Name: "a", MemorySize: 200})
	b := mustAcquire(t, pool, &Function{Name: "b", MemorySize: 200})

	// When: the pool bounces across the high-water mark — a release sheds its
	// container (dropping reserved memory right back under the mark), a new
	// invocation pushes it over again, a second release sheds again, and a
	// final release under the mark pools normally.
	pool.Release(context.Background(), a, true) // 400 ≥ 378: shed — regime entry
	c := mustAcquire(t, pool, &Function{Name: "c", MemorySize: 200})
	pool.Release(context.Background(), b, true) // 400 ≥ 378 again: shed — same episode, no second entry
	pool.Release(context.Background(), c, true) // 200 < 378: pooled — regime exit

	// Then: the episode is logged exactly once at each edge — an entry warning
	// when shedding starts and a recovery line when it stops — never one line
	// per release while the pool bounces on the boundary. The recovery line
	// reports how many containers the episode shed.
	var entries, exits int
	var shed int64 = -1
	for _, e := range logs.All() {
		switch e.Message {
		case memPressureEnterMsg:
			entries++
		case memPressureExitMsg:
			exits++
			for _, f := range e.Context {
				if f.Key == "containers_shed" {
					shed = f.Integer
				}
			}
		}
	}
	if entries != 1 {
		t.Errorf("regime entry lines = %d, want exactly 1", entries)
	}
	if exits != 1 {
		t.Errorf("regime exit lines = %d, want exactly 1", exits)
	}
	if shed != 2 {
		t.Errorf("containers_shed = %d, want 2 (both sheds belong to the one episode)", shed)
	}
}

func TestAcquire_memoryBoundReclaimLogsAtWarn(t *testing.T) {
	// Given: a 256 MB budget held entirely by one idle container, with plenty
	// of instance-count headroom, so a reclaim can only be memory-bound.
	core, logs := observer.New(zap.InfoLevel)
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.New(core), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 256})
	defer pool.Stop()
	idle := &Function{Name: "idle-fn", MemorySize: 200}
	pool.Release(context.Background(), mustAcquire(t, pool, idle), true)

	// When: admission reclaims the idle container to free its memory.
	if _, err := pool.Acquire(context.Background(), &Function{Name: "busy-fn", MemorySize: 200}); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Then: the reclaim is logged at Warn — the host budget is the binding
	// constraint and the operator should act on it — not at the Info the
	// routine count-cap reclaim uses.
	var found bool
	for _, e := range logs.All() {
		for _, f := range e.Context {
			if f.Key == "memory_bound" && f.Integer == 1 { // zap encodes bools as 0/1
				found = true
				if e.Level != zap.WarnLevel {
					t.Errorf("memory-bound reclaim logged at %v, want %v", e.Level, zap.WarnLevel)
				}
			}
		}
	}
	if !found {
		t.Fatal("no memory-bound reclaim log entry found")
	}
}

func TestAcquire_countBoundReclaimStaysAtInfo(t *testing.T) {
	// Given: a two-container cap with no memory budget, both slots held by
	// idle containers.
	core, logs := observer.New(zap.InfoLevel)
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.New(core), clock.NewMock(),
		PoolLimits{MaxInstances: 2, MaxWarmPerFunction: 2})
	defer pool.Stop()
	idle := &Function{Name: "idle-fn"}
	first := mustAcquire(t, pool, idle) // hold both so two containers exist
	second := mustAcquire(t, pool, idle)
	pool.Release(context.Background(), first, true)
	pool.Release(context.Background(), second, true)

	// When: admission reclaims an idle container for the count cap.
	if _, err := pool.Acquire(context.Background(), &Function{Name: "busy-fn"}); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Then: the routine count-cap reclaim stays at Info.
	var found bool
	for _, e := range logs.All() {
		for _, f := range e.Context {
			if f.Key == "memory_bound" && f.Integer == 0 {
				found = true
				if e.Level != zap.InfoLevel {
					t.Errorf("count-bound reclaim logged at %v, want %v", e.Level, zap.InfoLevel)
				}
			}
		}
	}
	if !found {
		t.Fatal("no count-bound reclaim log entry found")
	}
}

// flakyColdStartRuntime fails its first Acquire calls, then succeeds.
type flakyColdStartRuntime struct {
	mu       sync.Mutex
	failures int
}

func (r *flakyColdStartRuntime) CanHandle(string) bool { return true }

func (r *flakyColdStartRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures > 0 {
		r.failures--
		return nil, errors.New("docker exploded")
	}
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	return inst, nil
}

func (r *flakyColdStartRuntime) Release(context.Context, RuntimeInstance, bool) {}

func TestAcquire_failedColdStartReturnsItsMemoryReservation(t *testing.T) {
	// Given: a budget with room for exactly one container, and a cold start
	// that fails once.
	pool := NewInstancePool(&flakyColdStartRuntime{failures: 1}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 10, MaxMemoryMB: 256})
	defer pool.Stop()
	fn := &Function{Name: "fn", MemorySize: 200}
	if _, err := pool.Acquire(context.Background(), fn); err == nil {
		t.Fatal("first acquire: expected the injected cold start failure")
	}

	// When: the next invocation arrives with a short deadline.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inst, err := pool.Acquire(ctx, fn)

	// Then: it is admitted immediately — the failed cold start gave its
	// reservation back instead of leaking 200 MB of budget forever.
	if err != nil {
		t.Fatalf("acquire after failed cold start: %v — memory reservation leaked", err)
	}
	if inst == nil {
		t.Fatal("no instance returned")
	}
}

func TestPoolLimits_zeroMemoryBudgetMeansUnlimited(t *testing.T) {
	// Given: no budget configured (MaxMemoryMB left zero).
	pool := NewInstancePool(&countingColdStartRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxInstances: 10})
	defer pool.Stop()

	// When/Then: memory never gates admission — this is the pre-budget
	// behaviour, kept for daemons whose /info cannot be read.
	for i := range 5 {
		fn := &Function{Name: "fn", MemorySize: 10240}
		fn.Name = fn.Name + string(rune('a'+i))
		if _, err := pool.Acquire(context.Background(), fn); err != nil {
			t.Fatalf("acquire %d: %v — zero budget must mean unlimited", i, err)
		}
	}
}
