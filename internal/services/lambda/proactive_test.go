package lambda

// proactive_test.go — pins proactive initialization's stability guarantees:
// deploy churn collapses into one attempt (debounce), functions without
// trigger evidence get no environment, capacity contention is never queued
// on, and provisioned concurrency is never double-covered.

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/state"
)

// proactiveTestRuntime hands out instances for both normal and proactive
// acquisition and counts proactive creations.
type proactiveTestRuntime struct {
	mu        sync.Mutex
	proactive int
}

func (r *proactiveTestRuntime) CanHandle(string) bool { return true }

func (r *proactiveTestRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	return inst, nil
}

func (r *proactiveTestRuntime) AcquireProactive(_ context.Context, fn *Function) (RuntimeInstance, error) {
	r.mu.Lock()
	r.proactive++
	r.mu.Unlock()
	return r.Acquire(context.Background(), fn)
}

func (r *proactiveTestRuntime) Release(context.Context, RuntimeInstance, bool) {}

func (r *proactiveTestRuntime) proactiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.proactive
}

func seedProactiveFunction(t *testing.T, ls *lambdaStore, name string) *Function {
	t.Helper()
	fn := &Function{
		Name:  name,
		ARN:   "arn:aws:lambda:us-east-1:000000000000:function:" + name,
		State: "Active",
	}
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("putFunction: %v", aerr)
	}
	return fn
}

// awaitWarm polls until the pool holds want warm instances for name, or fails.
func awaitWarm(t *testing.T, pool *InstancePool, name string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pool.mu.Lock()
		got := len(pool.entries[name])
		pool.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	pool.mu.Lock()
	got := len(pool.entries[name])
	pool.mu.Unlock()
	t.Fatalf("warm instances for %s = %d, want %d", name, got, want)
}

func TestProactiveIniter_debouncesChurnIntoOneEnvironment(t *testing.T) {
	// Given: an invoked function and a stream of configuration updates closer
	// together than the settle delay, as a CDK deploy produces.
	clk := clock.NewMock()
	rt := &proactiveTestRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clk, PoolLimits{})
	defer pool.Stop()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	fn := seedProactiveFunction(t, ls, "churn-fn")
	pi := newProactiveIniter(func() *InstancePool { return pool }, ls, nil, zap.NewNop(), clk)
	pi.createEnvs = true
	defer pi.Stop()
	pi.NoteInvoked("churn-fn")

	// When: three updates land 5s apart, then the function settles.
	for range 3 {
		pi.NoteFunctionChanged(fn)
		clk.Add(5 * time.Second)
	}
	if got := rt.proactiveCount(); got != 0 {
		t.Fatalf("environments created during churn = %d, want 0 — debounce broken", got)
	}
	clk.Add(proactiveSettleDelay)

	// Then: exactly one environment is created and pooled.
	awaitWarm(t, pool, "churn-fn", 1)
	if got := rt.proactiveCount(); got != 1 {
		t.Fatalf("proactive creations = %d, want 1", got)
	}
}

func TestProactiveIniter_noTriggerEvidenceMeansNoEnvironment(t *testing.T) {
	// Given: a function that has never been invoked and has no URL or ESM.
	clk := clock.NewMock()
	rt := &proactiveTestRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clk, PoolLimits{})
	defer pool.Stop()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	fn := seedProactiveFunction(t, ls, "idle-fn")
	wired := func(context.Context, string) bool { return false }
	pi := newProactiveIniter(func() *InstancePool { return pool }, ls, wired, zap.NewNop(), clk)
	pi.createEnvs = true
	defer pi.Stop()

	// When: it settles after an update.
	pi.NoteFunctionChanged(fn)
	clk.Add(proactiveSettleDelay + time.Second)

	// Then: no environment is created — never-invoked, unwired functions stay
	// at zero containers.
	time.Sleep(50 * time.Millisecond) // give a wrong implementation time to act
	if got := rt.proactiveCount(); got != 0 {
		t.Fatalf("proactive creations = %d, want 0 for a function with no trigger evidence", got)
	}
}

func TestProactiveIniter_prefillRunsWithoutEnvironmentCreation(t *testing.T) {
	// Given: environment creation disabled (the default) and a prefill hook.
	clk := clock.NewMock()
	rt := &proactiveTestRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clk, PoolLimits{})
	defer pool.Stop()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	fn := seedProactiveFunction(t, ls, "prefill-fn")
	var prefilled []string
	var mu sync.Mutex
	pi := newProactiveIniter(func() *InstancePool { return pool }, ls, nil, zap.NewNop(), clk)
	pi.prefill = func(_ context.Context, fn *Function) {
		mu.Lock()
		prefilled = append(prefilled, fn.Name)
		mu.Unlock()
	}
	defer pi.Stop()

	// When: the function settles after a deploy.
	pi.NoteFunctionChanged(fn)
	clk.Add(proactiveSettleDelay + time.Second)

	// Then: artifacts were pre-built exactly once — no trigger evidence
	// needed — and no environment was created.
	mu.Lock()
	got := len(prefilled)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("prefill calls = %d, want 1", got)
	}
	if rt.proactiveCount() != 0 {
		t.Fatal("environment created despite LAMBDA_PROACTIVE_INIT being off")
	}
}

func TestProactiveInit_neverQueuesForContendedCapacity(t *testing.T) {
	// Given: a pool whose only instance slot is taken by a real invocation.
	rt := &proactiveTestRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{MaxInstances: 1, MaxInstancesPerFunction: 1})
	defer pool.Stop()
	busy := &Function{Name: "busy-fn"}
	inst, err := pool.Acquire(context.Background(), busy)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.Release(context.Background(), inst, true)

	// When/Then: a proactive attempt reports busy instead of queueing.
	if got := pool.ProactiveInit(&Function{Name: "other-fn"}); got != proactiveBusy {
		t.Fatalf("ProactiveInit = %v, want proactiveBusy", got)
	}
	if rt.proactiveCount() != 0 {
		t.Fatal("proactive creation ran despite contended capacity")
	}
}

func TestProactiveInit_skipsCoveredAndThrottledFunctions(t *testing.T) {
	// Given: a pool with a warm instance for one function and a provisioned
	// reservation for another.
	rt := &proactiveTestRuntime{}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	warmFn := &Function{Name: "warm-fn"}
	inst, err := pool.Acquire(context.Background(), warmFn)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.Release(context.Background(), inst, true)
	pcFn := &Function{Name: "pc-fn"}
	pool.SetProvisionedConcurrency(pcFn, 1)

	// When/Then: a function that already has an environment is not doubled up…
	if got := pool.ProactiveInit(warmFn); got != proactiveAlreadyWarm {
		t.Fatalf("ProactiveInit(warm) = %v, want proactiveAlreadyWarm", got)
	}
	// …a provisioned function is already covered…
	if got := pool.ProactiveInit(pcFn); got != proactiveAlreadyWarm {
		t.Fatalf("ProactiveInit(provisioned) = %v, want proactiveAlreadyWarm", got)
	}
	// …and a function throttled to zero runs nothing, as on AWS.
	zero := 0
	if got := pool.ProactiveInit(&Function{Name: "throttled-fn", ReservedConcurrency: &zero}); got != proactiveUnavailable {
		t.Fatalf("ProactiveInit(throttled) = %v, want proactiveUnavailable", got)
	}
}

// blockingProactiveRuntime blocks AcquireProactive until release is closed, so
// a test can land DeleteFunction's eviction while the environment is still
// being created — the window #460 is about. The container's ID is not on any
// record yet, so a delete has nothing of its own to stop; only the goroutine's
// own Release call, once AcquireProactive finally returns, ever learns whether
// the function is still there.
type blockingProactiveRuntime struct {
	proactiveTestRuntime
	startOnce sync.Once
	started   chan struct{}
	release   chan struct{}

	mu   sync.Mutex
	inst *poolTestInstance
}

func newBlockingProactiveRuntime() *blockingProactiveRuntime {
	return &blockingProactiveRuntime{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingProactiveRuntime) AcquireProactive(_ context.Context, fn *Function) (RuntimeInstance, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.release
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	r.mu.Lock()
	r.inst = inst
	r.mu.Unlock()
	return inst, nil
}

func (r *blockingProactiveRuntime) instance() *poolTestInstance {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inst
}

// TestProactiveInit_deletedMidStartDoesNotLeakContainer covers the third
// instance of the delete-mid-start race (#460), for the site the two prior
// fixes (RDS #412, ElastiCache #459) don't reach: an execution environment
// ProactiveInit is still creating in the background when DeleteFunction runs.
// EvictFunction can only close what is already in the warm set, and the new
// container's ID reaches nothing else until the goroutine's own Release call —
// see InstancePool.Release's `p.evicted[name]` check, the same guard an
// in-flight invocation's release relies on
// (TestInstancePoolRelease_afterEvictionDoesNotPoolForADeletedFunction).
func TestProactiveInit_deletedMidStartDoesNotLeakContainer(t *testing.T) {
	// Given: a function whose proactive environment is still being created —
	// blocked mid-flight, the way a slow image pull or container start would
	// hold it open on a real daemon.
	rt := newBlockingProactiveRuntime()
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "proactive-delete-race", State: "Active"}

	if got := pool.ProactiveInit(fn); got != proactiveStarted {
		t.Fatalf("ProactiveInit = %v, want proactiveStarted", got)
	}
	select {
	case <-rt.started:
	case <-time.After(2 * time.Second):
		t.Fatal("AcquireProactive was never called")
	}

	// When: DeleteFunction evicts the function while the container is still
	// starting — exactly what EvictFunction does, before the goroutine's own
	// Release call ever runs.
	pool.EvictFunction(fn.Name)
	close(rt.release)

	// Then: Release — the one point that learns the container came up — sees
	// the function already evicted and destroys the instance instead of
	// pooling it. Nothing else was ever going to reclaim it: it holds no
	// entry, no checked-out slot mapping back to a live task, nothing a sweep
	// would ever find.
	pool.warmWG.Wait()
	inst := rt.instance()
	if inst == nil {
		t.Fatal("AcquireProactive never returned an instance")
	}
	if got := inst.CloseCalls(); got != 1 {
		t.Fatalf("close calls = %d, want 1 — the container was pooled for a deleted function", got)
	}
	pool.mu.Lock()
	warm := len(pool.entries[fn.Name])
	pool.mu.Unlock()
	if warm != 0 {
		t.Fatalf("warm instances for %s = %d, want 0 — the container was pooled after the function was deleted", fn.Name, warm)
	}
}
