package lambda

// runtime_pool_recreate_test.go — a function that is deleted and created again
// is a live function, not a leftover.
//
// EvictFunction marks a name as wholesale-retired so an invocation still in
// flight destroys its environment on release instead of pooling it for a
// function that no longer exists. The marker has to be lifted the moment the
// name is demonstrably in use again — otherwise every environment built for the
// new function is destroyed as though it belonged to the old one.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
)

// countingRuntime hands out instances and counts how many it was asked for. It
// refuses past max so a pool that will not stop rebuilding an environment ends
// the test instead of spinning containers forever.
type countingRuntime struct {
	max int

	mu       sync.Mutex
	acquires int
	handed   []*poolTestInstance
}

func (r *countingRuntime) CanHandle(string) bool { return true }

func (r *countingRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.acquires >= r.max {
		return nil, errors.New("countingRuntime: refusing further cold starts")
	}
	r.acquires++
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	r.handed = append(r.handed, inst)
	return inst, nil
}

func (r *countingRuntime) Release(context.Context, RuntimeInstance, bool) {}

func (r *countingRuntime) coldStarts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.acquires
}

// TestInstancePoolSetProvisionedConcurrency_afterDeleteAndRecreate covers a
// function deleted and created again under the same name, then given
// provisioned concurrency before anything invokes it.
//
// EvictFunction's marker survived until the next Acquire, so the environments
// the reservation built were destroyed on release as leftovers of the deleted
// function — and because a provisioned environment lost while the reservation
// stands is rebuilt, the pool rebuilt and destroyed one container after
// another for as long as the reservation stood.
func TestInstancePoolSetProvisionedConcurrency_afterDeleteAndRecreate(t *testing.T) {
	// Given: a function that was deleted...
	rt := &countingRuntime{max: 4}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "recreated-fn", State: "Active", MemorySize: 128}
	pool.EvictFunction(fn.Name)

	// When: it is created again and given provisioned concurrency, with no
	// invocation in between.
	pool.SetProvisionedConcurrency(fn, 1)
	pool.warmWG.Wait()

	// Then: the reservation is filled by exactly one cold start, and the
	// environment it built is still there.
	if got := rt.coldStarts(); got != 1 {
		t.Fatalf("cold starts = %d, want 1 — the pool kept rebuilding an environment it destroyed", got)
	}
	pool.mu.Lock()
	warm := len(pool.entries[fn.Name])
	reserved := len(pool.provisionedInstances[fn.Name])
	pool.mu.Unlock()
	if warm != 1 {
		t.Fatalf("warm instances = %d, want 1 — the reservation's environment was destroyed", warm)
	}
	if reserved != 1 {
		t.Fatalf("environments holding the reservation = %d, want 1", reserved)
	}
	for _, inst := range rt.handed {
		if got := inst.CloseCalls(); got != 0 {
			t.Fatalf("close calls = %d, want 0 — an environment built for the new function was destroyed", got)
		}
	}
}

// TestInstancePoolProactiveInit_afterDeleteAndRecreate is the same marker seen
// from the other creation path: proactive initialization builds an environment
// for the recreated function, and it must survive its own release.
func TestInstancePoolProactiveInit_afterDeleteAndRecreate(t *testing.T) {
	// Given: a deleted function, created again.
	rt := &proactiveCountingRuntime{countingRuntime: countingRuntime{max: 4}}
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "recreated-proactive-fn", State: "Active", MemorySize: 128}
	pool.EvictFunction(fn.Name)

	// When: an environment is initialized ahead of traffic.
	if got := pool.ProactiveInit(fn); got != proactiveStarted {
		t.Fatalf("ProactiveInit = %v, want proactiveStarted", got)
	}
	pool.warmWG.Wait()

	// Then: it is warm, not destroyed as a leftover of the deleted function.
	pool.mu.Lock()
	warm := len(pool.entries[fn.Name])
	pool.mu.Unlock()
	if warm != 1 {
		t.Fatalf("warm instances = %d, want 1 — the proactive environment was destroyed", warm)
	}
}

// proactiveCountingRuntime is countingRuntime that can also be asked for a
// proactively initialized environment.
type proactiveCountingRuntime struct {
	countingRuntime
}

func (r *proactiveCountingRuntime) AcquireProactive(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	return r.Acquire(ctx, fn)
}
