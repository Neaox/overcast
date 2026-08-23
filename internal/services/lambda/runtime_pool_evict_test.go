package lambda

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
)

// blockingColdStartRuntime models the window a real cold start spends with a
// container that already exists on the Docker daemon and no instance to show
// for it yet: `created` fires once the container would have been created, and
// the acquire is then held until the test releases it or its context is
// cancelled.
//
// finishAnyway makes the held acquire ignore cancellation and hand back the
// instance regardless — the cancellation that lands a moment too late, after
// the container came up.
type blockingColdStartRuntime struct {
	created      chan struct{}
	createdOnce  sync.Once
	release      chan struct{}
	finishAnyway bool

	mu      sync.Mutex
	inst    *poolTestInstance
	removed bool
}

func newBlockingColdStartRuntime() *blockingColdStartRuntime {
	return &blockingColdStartRuntime{
		created: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingColdStartRuntime) CanHandle(string) bool { return true }

func (r *blockingColdStartRuntime) Acquire(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	r.createdOnce.Do(func() { close(r.created) })

	if !r.finishAnyway {
		select {
		case <-ctx.Done():
			// What ContainerRuntime.acquireContainer does at its next Docker
			// call once the context is cancelled: remove what it created.
			r.mu.Lock()
			r.removed = true
			r.mu.Unlock()
			return nil, ctx.Err()
		case <-r.release:
		}
	} else {
		<-r.release
	}

	inst := newPoolTestInstance(fn.Name)
	inst.configIdentity = functionInstanceIdentity(fn)
	r.mu.Lock()
	r.inst = inst
	r.mu.Unlock()
	return inst, nil
}

func (r *blockingColdStartRuntime) Release(context.Context, RuntimeInstance, bool) {}

func (r *blockingColdStartRuntime) containerRemoved() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.removed
}

func (r *blockingColdStartRuntime) instance() *poolTestInstance {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inst
}

// TestInstancePoolEvictFunction_abortsAColdStartInFlight is the leak from
// #1336: a container between `docker create` and its first `GET /next` belongs
// to no map in the pool, so EvictFunction — all DeleteFunction has — used to
// walk straight past it. The container then outlived the function it was
// created for, and nothing was ever going to reclaim it.
func TestInstancePoolEvictFunction_abortsAColdStartInFlight(t *testing.T) {
	// Given: a function whose only environment is still being created.
	rt := newBlockingColdStartRuntime()
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "cold-start-delete-race", State: "Active"}

	acquired := make(chan error, 1)
	go func() {
		inst, err := pool.Acquire(context.Background(), fn)
		if inst != nil {
			t.Errorf("Acquire returned an instance for a deleted function")
		}
		acquired <- err
	}()
	select {
	case <-rt.created:
	case <-time.After(2 * time.Second):
		t.Fatal("the cold start never started")
	}

	// When: the function is deleted while that cold start is in flight.
	pool.EvictFunction(fn.Name)

	// Then: the creation is cancelled, so the runtime tears down the container
	// it had already made rather than leaving it running for a function that
	// no longer exists...
	select {
	case err := <-acquired:
		if err == nil {
			t.Fatal("Acquire succeeded for a function deleted mid cold start")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire was never released by the eviction")
	}
	if !rt.containerRemoved() {
		t.Fatal("the container was left running — the cold start was never cancelled")
	}

	// ...and the slot it held is given back, so the function's concurrency is
	// not permanently shrunk by the abort.
	pool.mu.Lock()
	checkedOut := pool.checkedOut[fn.Name]
	starting := len(pool.starting[fn.Name])
	pool.mu.Unlock()
	if checkedOut != 0 {
		t.Fatalf("checked-out slots = %d, want 0", checkedOut)
	}
	if starting != 0 {
		t.Fatalf("cold starts still registered = %d, want 0", starting)
	}
}

// TestInstancePoolEvictFunction_destroysAnEnvironmentThatLandsAfterTheDelete
// covers the other side of the same race: the cancellation arrives after the
// container is already up, so the cold start hands back a usable environment
// for a function that is gone. The instance never reaches the caller and is
// destroyed here — it holds no warm entry and no tracker record, so nothing
// else could.
func TestInstancePoolEvictFunction_destroysAnEnvironmentThatLandsAfterTheDelete(t *testing.T) {
	// Given: a cold start in flight that will complete regardless of the
	// eviction.
	rt := newBlockingColdStartRuntime()
	rt.finishAnyway = true
	pool := NewInstancePool(rt, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	fn := &Function{Name: "cold-start-delete-late", State: "Active"}

	acquired := make(chan error, 1)
	go func() {
		_, err := pool.Acquire(context.Background(), fn)
		acquired <- err
	}()
	select {
	case <-rt.created:
	case <-time.After(2 * time.Second):
		t.Fatal("the cold start never started")
	}

	// When: the function is deleted, and the container comes up anyway.
	pool.EvictFunction(fn.Name)
	close(rt.release)

	// Then: the environment is destroyed rather than handed to the invocation.
	select {
	case err := <-acquired:
		if err == nil {
			t.Fatal("Acquire handed back an environment for a deleted function")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire never returned")
	}
	inst := rt.instance()
	if inst == nil {
		t.Fatal("the runtime never produced an instance")
	}
	if got := inst.CloseCalls(); got != 1 {
		t.Fatalf("close calls = %d, want 1 — the container outlived its function", got)
	}
	pool.mu.Lock()
	warm := len(pool.entries[fn.Name])
	checkedOut := pool.checkedOut[fn.Name]
	pool.mu.Unlock()
	if warm != 0 {
		t.Fatalf("warm instances = %d, want 0", warm)
	}
	if checkedOut != 0 {
		t.Fatalf("checked-out slots = %d, want 0", checkedOut)
	}
}
