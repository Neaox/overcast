package lambda

// instance_telemetry_test.go — the instance tracker's InitOrigin (how an
// execution environment was created) and EvictedReason (why it was removed)
// telemetry, plus the pool's threading of eviction reasons through to the
// tracker via InstanceObserver.

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
)

// ─── instanceTracker: InitOrigin ─────────────────────────────────────────────

func TestInstanceWarmed_proactiveOrigin(t *testing.T) {
	// Given: a tracker with no prior record for the instance.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)

	// When: a proactively created environment is recorded.
	tracker.InstanceWarmed("fn", "inst-1", "container-1", instanceOriginProactive)

	// Then: the snapshot reports the proactive origin, is not marked
	// provisioned, sits idle, and has no invocation outcome yet.
	snap := tracker.Instances()
	if len(snap) != 1 {
		t.Fatalf("tracked instances = %d, want 1", len(snap))
	}
	if snap[0].InitOrigin != instanceOriginProactive {
		t.Fatalf("InitOrigin = %q, want %q", snap[0].InitOrigin, instanceOriginProactive)
	}
	if snap[0].Provisioned {
		t.Fatal("Provisioned = true for a proactively created environment")
	}
	if snap[0].Status != instanceStatusIdle {
		t.Fatalf("Status = %q, want %q", snap[0].Status, instanceStatusIdle)
	}
	if snap[0].LastInvocationStatus != "" {
		t.Fatalf("LastInvocationStatus = %q, want empty", snap[0].LastInvocationStatus)
	}
}

func TestInstanceWarmed_provisionedOrigin(t *testing.T) {
	// Given: a tracker with no prior record for the instance.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)

	// When: an environment created for a provisioned concurrency reservation
	// is recorded.
	tracker.InstanceWarmed("fn", "inst-1", "container-1", instanceOriginProvisioned)

	// Then: the snapshot reports both the provisioned origin and the
	// Provisioned flag — the two must never disagree.
	snap := tracker.Instances()
	if len(snap) != 1 {
		t.Fatalf("tracked instances = %d, want 1", len(snap))
	}
	if snap[0].InitOrigin != instanceOriginProvisioned {
		t.Fatalf("InitOrigin = %q, want %q", snap[0].InitOrigin, instanceOriginProvisioned)
	}
	if !snap[0].Provisioned {
		t.Fatal("Provisioned = false for a provisioned-concurrency environment")
	}
}

func TestBegin_onDemandOrigin(t *testing.T) {
	// Given: a tracker.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)

	// When: an invocation opens a record the ordinary way (Begin, then Bind
	// to a freshly cold-started environment).
	trackedFor(tracker, "fn", nil)

	// Then: the environment reports an on-demand origin.
	snap := tracker.Instances()
	if len(snap) != 1 {
		t.Fatalf("tracked instances = %d, want 1", len(snap))
	}
	if snap[0].InitOrigin != instanceOriginOnDemand {
		t.Fatalf("InitOrigin = %q, want %q", snap[0].InitOrigin, instanceOriginOnDemand)
	}
}

func TestBind_warmReusePreservesProactiveOrigin(t *testing.T) {
	// Given: a proactively created, idle environment.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)
	inst := newPoolTestInstance("fn")
	tracker.InstanceWarmed("fn", inst.InstanceID(), inst.ContainerID(), instanceOriginProactive)

	// When: an on-demand invocation is bound to the same environment — a
	// warm reuse rather than a cold start.
	inv := tracker.Begin("fn", nil)
	inv.Bind(inst)

	// Then: the environment keeps reporting "proactive" — how it was actually
	// initialized — rather than being overwritten to "on-demand" just because
	// an invocation went on to use it.
	snap := tracker.Instances()
	if len(snap) != 1 {
		t.Fatalf("tracked instances = %d, want 1", len(snap))
	}
	if snap[0].InitOrigin != instanceOriginProactive {
		t.Fatalf("InitOrigin after warm reuse = %q, want %q (preserved)", snap[0].InitOrigin, instanceOriginProactive)
	}
	if snap[0].Status != instanceStatusRunning {
		t.Fatalf("Status after warm reuse = %q, want %q", snap[0].Status, instanceStatusRunning)
	}
}

// ─── instanceTracker: EvictedReason ──────────────────────────────────────────

func TestInstanceLost_publishesReason(t *testing.T) {
	// Given: a tracker with an idle environment and a subscriber to eviction
	// events.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)
	bus := events.NewBus()
	t.Cleanup(bus.Stop)
	tracker.SetBus(bus)

	evicted := make(chan events.LambdaInstancePayload, 1)
	cancel := bus.Subscribe(events.LambdaInstanceEvicted, func(_ context.Context, e events.Event) {
		if payload, ok := e.Payload.(events.LambdaInstancePayload); ok {
			evicted <- payload
		}
	})
	t.Cleanup(cancel)

	trackedFor(tracker, "fn", nil).Finish(true, "")
	instanceID := tracker.Instances()[0].InstanceID

	// When: the pool reports the environment as lost with a specific reason.
	tracker.InstanceLost("fn", instanceID, evictReasonContainerDied)

	// Then: the published event carries that reason.
	select {
	case payload := <-evicted:
		if payload.EvictedReason != evictReasonContainerDied {
			t.Fatalf("EvictedReason = %q, want %q", payload.EvictedReason, evictReasonContainerDied)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for LambdaInstanceEvicted event")
	}
}

func TestInvalidate_publishesConfigChangeReason(t *testing.T) {
	// Given: a tracker with an idle environment and a subscriber to eviction
	// events.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)
	bus := events.NewBus()
	t.Cleanup(bus.Stop)
	tracker.SetBus(bus)

	evicted := make(chan events.LambdaInstancePayload, 1)
	cancel := bus.Subscribe(events.LambdaInstanceEvicted, func(_ context.Context, e events.Event) {
		if payload, ok := e.Payload.(events.LambdaInstancePayload); ok {
			evicted <- payload
		}
	})
	t.Cleanup(cancel)

	trackedFor(tracker, "fn", nil).Finish(true, "")

	// When: the function's configuration changes.
	tracker.Invalidate("fn")

	// Then: the eviction it publishes for the idle instance carries
	// "config-change".
	select {
	case payload := <-evicted:
		if payload.EvictedReason != evictReasonConfigChange {
			t.Fatalf("EvictedReason = %q, want %q", payload.EvictedReason, evictReasonConfigChange)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for LambdaInstanceEvicted event")
	}
}

func TestEvict_publishesFunctionDeletedReason(t *testing.T) {
	// Given: a tracker with a running (mid-invocation) environment and a
	// subscriber to eviction events.
	tracker := newInstanceTracker(clock.NewMock(), zap.NewNop())
	t.Cleanup(tracker.Stop)
	bus := events.NewBus()
	t.Cleanup(bus.Stop)
	tracker.SetBus(bus)

	evicted := make(chan events.LambdaInstancePayload, 1)
	cancel := bus.Subscribe(events.LambdaInstanceEvicted, func(_ context.Context, e events.Event) {
		if payload, ok := e.Payload.(events.LambdaInstancePayload); ok {
			evicted <- payload
		}
	})
	t.Cleanup(cancel)

	trackedFor(tracker, "fn", nil)

	// When: the function is deleted.
	tracker.Evict("fn")

	// Then: the eviction it publishes carries "function-deleted".
	select {
	case payload := <-evicted:
		if payload.EvictedReason != evictReasonFunctionDeleted {
			t.Fatalf("EvictedReason = %q, want %q", payload.EvictedReason, evictReasonFunctionDeleted)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for LambdaInstanceEvicted event")
	}
}

// ─── InstancePool: reason threading through InstanceObserver ────────────────

// reasonObserver is an InstanceObserver test double that records every
// InstanceLost reason (and InstanceWarmed origin) it is told about, in call
// order, for tests asserting the pool threads the right value through.
type reasonObserver struct {
	mu     sync.Mutex
	lost   []string
	warmed []string
}

func (o *reasonObserver) InstanceWarmed(_, _, _, origin string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.warmed = append(o.warmed, origin)
}

func (o *reasonObserver) InstanceLost(_, _, reason string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lost = append(o.lost, reason)
}

func (o *reasonObserver) reasons() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.lost...)
}

func TestInstancePoolSweep_reportsIdleTTLReason(t *testing.T) {
	// Given: a pool holding an idle instance well past the idle TTL, with an
	// observer wired in.
	clk := clock.NewMock()
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clk, PoolLimits{})
	defer pool.Stop()
	observer := &reasonObserver{}
	pool.observer = observer
	pool.Release(context.Background(), newPoolTestInstance("fn"), true)

	// When: the idle sweeper runs.
	clk.Add(poolIdleTTL + time.Minute)
	pool.sweep()

	// Then: the observer is told the instance was lost to the idle TTL, not
	// some other cause.
	if reasons := observer.reasons(); len(reasons) != 1 || reasons[0] != evictReasonIdleTTL {
		t.Fatalf("InstanceLost reasons = %v, want [%q]", reasons, evictReasonIdleTTL)
	}
}

func TestInstancePoolInvalidateFunction_reportsConfigChangeReason(t *testing.T) {
	// Given: a pool holding an idle instance for a function, with an observer
	// wired in.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	observer := &reasonObserver{}
	pool.observer = observer
	pool.Release(context.Background(), newPoolTestInstance("fn"), true)

	// When: the function's configuration changes.
	pool.InvalidateFunction(&Function{Name: "fn", Environment: map[string]string{"NEW": "1"}})

	// Then: the observer is told the instance was lost to a configuration
	// change.
	if reasons := observer.reasons(); len(reasons) != 1 || reasons[0] != evictReasonConfigChange {
		t.Fatalf("InstanceLost reasons = %v, want [%q]", reasons, evictReasonConfigChange)
	}
}

func TestInstancePoolEvictFunction_reportsFunctionDeletedReason(t *testing.T) {
	// Given: a pool holding an idle instance for a function, with an observer
	// wired in.
	pool := NewInstancePool(poolTestRuntime{}, zap.NewNop(), clock.NewMock(), PoolLimits{})
	defer pool.Stop()
	observer := &reasonObserver{}
	pool.observer = observer
	pool.Release(context.Background(), newPoolTestInstance("fn"), true)

	// When: the function is deleted.
	pool.EvictFunction("fn")

	// Then: the observer is told the instance was lost because its function
	// was deleted.
	if reasons := observer.reasons(); len(reasons) != 1 || reasons[0] != evictReasonFunctionDeleted {
		t.Fatalf("InstanceLost reasons = %v, want [%q]", reasons, evictReasonFunctionDeleted)
	}
}
