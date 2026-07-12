package lambda

import (
	"context"
	"sync"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"go.uber.org/zap"
)

type poolTestRuntime struct{}

func (poolTestRuntime) CanHandle(string) bool { return true }

func (poolTestRuntime) Acquire(context.Context, *Function) (RuntimeInstance, error) {
	return nil, nil
}

func (poolTestRuntime) Release(context.Context, RuntimeInstance, bool) {}

type poolTestInstance struct {
	mu           sync.Mutex
	functionName string
	codeHash     string
	healthy      bool
	closeCalls   int
}

func newPoolTestInstance(functionName string) *poolTestInstance {
	return &poolTestInstance{functionName: functionName, codeHash: "code", healthy: true}
}

func (i *poolTestInstance) Invoke(context.Context, []byte) (*InvokeResult, error) { return nil, nil }
func (i *poolTestInstance) LogStreamName() string                                 { return "stream" }
func (i *poolTestInstance) Healthy() bool                                         { return i.healthy }
func (i *poolTestInstance) FunctionName() string                                  { return i.functionName }
func (i *poolTestInstance) CodeHash() string                                      { return i.codeHash }

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

func TestInstancePoolRelease_duplicateHealthyInstance(t *testing.T) {
	// Given: a pool and two healthy instances for the same function, as can happen
	// when concurrent cold starts finish before either instance is reused.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock())
	defer pool.Stop()
	first := newPoolTestInstance("fn")
	second := newPoolTestInstance("fn")

	// When: both instances are released as healthy.
	pool.Release(context.Background(), first, true)
	pool.Release(context.Background(), second, true)

	// Then: only one instance remains pooled, and the duplicate is closed instead
	// of being overwritten and leaked.
	if first.CloseCalls()+second.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1", first.CloseCalls()+second.CloseCalls())
	}
	pool.mu.Lock()
	entryCount := len(pool.entries)
	pooled := pool.entries["fn"].inst
	pool.mu.Unlock()
	if entryCount != 1 {
		t.Fatalf("pooled entries = %d, want 1", entryCount)
	}
	if pooled != first {
		t.Fatalf("pooled instance = %p, want first instance %p", pooled, first)
	}
	if second.CloseCalls() != 1 {
		t.Fatalf("second close calls = %d, want 1", second.CloseCalls())
	}
}

func TestInstancePoolRelease_afterStop(t *testing.T) {
	// Given: a stopped pool.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock())
	pool.Stop()
	inst := newPoolTestInstance("fn")

	// When: an in-flight invocation releases a healthy instance after Stop.
	pool.Release(context.Background(), inst, true)

	// Then: the instance is closed instead of panicking or writing into a nil map.
	if inst.CloseCalls() != 1 {
		t.Fatalf("close calls = %d, want 1", inst.CloseCalls())
	}
}
