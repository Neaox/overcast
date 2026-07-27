package lambda

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/clock"
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

func (i *poolTestInstance) Invoke(context.Context, []byte) (*InvokeResult, error) { return nil, nil }
func (i *poolTestInstance) LogStreamName() string                                 { return "stream" }
func (i *poolTestInstance) Healthy() bool                                         { return i.healthy }
func (i *poolTestInstance) FunctionName() string                                  { return i.functionName }
func (i *poolTestInstance) ConfigIdentity() string                                { return i.configIdentity }
func (i *poolTestInstance) ContainerID() string                                   { return i.containerID }
func (i *poolTestInstance) InstanceID() string                                    { return i.instanceID }

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
