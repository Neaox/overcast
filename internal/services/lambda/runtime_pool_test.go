package lambda

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/overcast-sh/overcast/internal/clock"
	"go.uber.org/zap"
)

type poolTestRuntime struct{}

func (poolTestRuntime) CanHandle(string) bool { return true }

func (poolTestRuntime) Acquire(_ context.Context, fn *Function) (RuntimeInstance, error) {
	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	return inst, nil
}

func (poolTestRuntime) Release(context.Context, RuntimeInstance, bool) {}

type poolTestInstance struct {
	mu             sync.Mutex
	functionName   string
	configIdentity string
	containerID    string
	instanceID     string
	healthy        bool
	closeCalls     int
}

func newPoolTestInstance(functionName string) *poolTestInstance {
	return &poolTestInstance{
		functionName:   functionName,
		configIdentity: "code",
		instanceID:     uuid.NewString(),
		healthy:        true,
	}
}

func (i *poolTestInstance) Invoke(context.Context, []byte, InvokeOptions) (*InvokeResult, error) {
	return nil, nil
}
func (i *poolTestInstance) LogStreamName() string  { return "stream" }
func (i *poolTestInstance) Healthy() bool          { return i.healthy }
func (i *poolTestInstance) FunctionName() string   { return i.functionName }
func (i *poolTestInstance) ConfigIdentity() string { return i.configIdentity }
func (i *poolTestInstance) ContainerID() string    { return i.containerID }
func (i *poolTestInstance) InstanceID() string     { return i.instanceID }

func (i *poolTestInstance) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closeCalls++
	return nil
}

func (i *poolTestInstance) CloseCalls() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.closeCalls
}

func TestInstancePoolRelease_concurrentInstancesBothStayWarm(t *testing.T) {
	// Given: a pool and two healthy instances for the same function, as happens
	// when two concurrent invocations each cold start one.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	first := newPoolTestInstance("fn")
	second := newPoolTestInstance("fn")

	// When: both instances are released as healthy.
	pool.Release(context.Background(), first, true)
	pool.Release(context.Background(), second, true)

	// Then: both stay warm. A burst scales the function out and the surplus is
	// kept for reuse until the idle sweep, rather than being destroyed the
	// moment its invocation ends.
	if got := first.CloseCalls() + second.CloseCalls(); got != 0 {
		t.Fatalf("close calls = %d, want 0", got)
	}
	pool.mu.Lock()
	warm := len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 2 {
		t.Fatalf("warm instances = %d, want 2", warm)
	}
}

func TestInstancePoolRelease_closesSurplusBeyondWarmCap(t *testing.T) {
	// Given: a pool that keeps at most two idle instances per function.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(),
		PoolLimits{MaxWarmPerFunction: 2})
	defer pool.Stop()
	insts := []*poolTestInstance{
		newPoolTestInstance("fn"), newPoolTestInstance("fn"), newPoolTestInstance("fn"),
	}

	// When: three instances are released.
	for _, inst := range insts {
		pool.Release(context.Background(), inst, true)
	}

	// Then: the cap holds and the third is destroyed rather than left to hold
	// memory until the idle sweep.
	pool.mu.Lock()
	warm := len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 2 {
		t.Fatalf("warm instances = %d, want 2", warm)
	}
	closed := 0
	for _, inst := range insts {
		closed += inst.CloseCalls()
	}
	if closed != 1 {
		t.Fatalf("closed instances = %d, want 1", closed)
	}
}

func TestInstancePoolRelease_afterStop(t *testing.T) {
	// Given: a stopped pool.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	pool.Stop()
	inst := newPoolTestInstance("fn")

	// When: an in-flight invocation releases a healthy instance after Stop.
	pool.Release(context.Background(), inst, true)

	// Then: the instance is closed instead of panicking or writing into a nil map.
	if inst.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1", inst.CloseCalls())
	}
}

func TestInstancePoolRelease_afterEvictionDoesNotPoolForADeletedFunction(t *testing.T) {
	// Given: a function with one invocation in flight — its instance is checked
	// out of the pool, not idle in the warm set.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	inFlight := newPoolTestInstance("fn")
	pool.mu.Lock()
	pool.expected["fn"] = inFlight.ConfigIdentity()
	pool.mu.Unlock()

	// When: DeleteFunction evicts the function, and only then does the
	// invocation finish and release its instance.
	pool.EvictFunction("fn")
	pool.Release(context.Background(), inFlight, true)

	// Then: the container is destroyed. EvictFunction can only close the idle
	// instances it can see; an in-flight one used to come back to a pool that
	// had forgotten the function, take the "never admitted" branch (expected is
	// empty for both cases), and be pooled for a function that no longer
	// exists — leaving a container running with nothing to ever reclaim it.
	if got := inFlight.CloseCalls(); got != 1 {
		t.Fatalf("close calls = %d, want 1 — the instance was pooled for a deleted function", got)
	}
	pool.mu.Lock()
	warm := len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 0 {
		t.Fatalf("warm instances = %d, want 0 for a deleted function", warm)
	}
}

func TestInstancePoolRelease_recreatedFunctionPoolsAgain(t *testing.T) {
	// Given: a function that was deleted and then recreated with the same name
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	pool.EvictFunction("fn")

	// When: the recreated function is admitted and its instance released
	revived, err := pool.Acquire(context.Background(), &Function{Name: "fn"})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.Release(context.Background(), revived, true)

	// Then: it pools normally. The eviction marker must not outlive the
	// function it was about, or every recreated function cold starts forever.
	pool.mu.Lock()
	warm := len(pool.entries["fn"])
	pool.mu.Unlock()
	if warm != 1 {
		t.Fatalf("warm instances = %d, want 1", warm)
	}
}
