package lambda

// runtime_pool.go — InstancePool manages one warm RuntimeInstance per function.
//
// AWS Lambda reuses execution environments (containers) for sequential
// invocations of the same function version. InstancePool models this by keeping
// one warm instance per function name. When UpdateFunctionCode replaces the
// code, the old instance is discarded and a fresh one will start on the next
// invocation (cold start).
//
// Capacity model:
//   - One warm instance per function (correct baseline — no provisioned concurrency).
//   - Idle instances unused for 15 minutes are evicted by the background sweeper.
//   - Instances with a stale code hash (after UpdateFunctionCode) are evicted on
//     the next Acquire call.
//
// Thread safety: all public methods are safe for concurrent use.

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
)

const (
	// poolSweepInterval is how often the sweeper goroutine wakes up.
	poolSweepInterval = 30 * time.Second
	// poolIdleTTL is how long an instance may sit idle before being evicted.
	poolIdleTTL = 15 * time.Minute
)

// poolEntry holds a single warm RuntimeInstance for one function.
type poolEntry struct {
	inst     RuntimeInstance
	codeHash string    // SHA-256 of the code zip at the time the instance was created
	lastUsed time.Time // updated on every Release
}

// InstancePool manages warm RuntimeInstances, one per function.
type InstancePool struct {
	mu      sync.Mutex
	entries map[string]*poolEntry // keyed by function name
	rt      Runtime               // used for cold starts
	log     *zap.Logger
	clk     clock.Clock
	stopCh  chan struct{}
}

// NewInstancePool creates an InstancePool backed by rt and starts the
// background sweeper. Call Stop() to shut it down.
func NewInstancePool(rt Runtime, log *zap.Logger, clk clock.Clock) *InstancePool {
	p := &InstancePool{
		entries: make(map[string]*poolEntry),
		rt:      rt,
		log:     log,
		clk:     clk,
		stopCh:  make(chan struct{}),
	}
	go p.sweepLoop()
	return p
}

// Acquire returns a warm RuntimeInstance for fn.
//   - Warm hit: existing instance with matching codeHash → reused.
//   - Stale hit: existing instance with different codeHash (code was updated) →
//     old instance closed, new cold start.
//   - Miss: no entry → cold start via rt.Acquire.
func (p *InstancePool) Acquire(ctx context.Context, fn *Function) (RuntimeInstance, error) {
	p.mu.Lock()
	entry, ok := p.entries[fn.Name]
	if ok {
		if entry.codeHash != functionCodeIdentity(fn) {
			// Code was updated — discard the stale instance.
			p.log.Debug("lambda pool: evicting stale instance",
				zap.String("function", fn.Name),
			)
			if err := entry.inst.Close(); err != nil {
				p.log.Warn("lambda pool: close stale instance failed",
					zap.String("function", fn.Name),
					zap.Error(err),
				)
			}
			delete(p.entries, fn.Name)
			ok = false
		}
	}
	if ok {
		// Warm hit — remove from pool while in use.
		inst := entry.inst
		delete(p.entries, fn.Name)
		p.mu.Unlock()
		p.log.Info("lambda pool: warm start", zap.String("function", fn.Name))
		return inst, nil
	}
	p.mu.Unlock()

	// Cold start — create a new instance outside the lock.
	p.log.Info("lambda pool: cold start", zap.String("function", fn.Name))
	return p.rt.Acquire(ctx, fn)
}

// AcquireWithProgress is like Acquire but reports lifecycle steps via progress.
// If a warm instance is available it is returned immediately; otherwise it
// delegates to the underlying ContainerRuntime.AcquireWithProgress for a cold
// start with progress reporting.
func (p *InstancePool) AcquireWithProgress(ctx context.Context, fn *Function, progress ProgressFunc) (RuntimeInstance, error) {
	p.mu.Lock()
	entry, ok := p.entries[fn.Name]
	if ok {
		if entry.codeHash != functionCodeIdentity(fn) {
			p.log.Debug("lambda pool: evicting stale instance",
				zap.String("function", fn.Name),
			)
			if err := entry.inst.Close(); err != nil {
				p.log.Warn("lambda pool: close stale instance failed",
					zap.String("function", fn.Name),
					zap.Error(err),
				)
			}
			delete(p.entries, fn.Name)
			ok = false
		}
	}
	if ok {
		inst := entry.inst
		delete(p.entries, fn.Name)
		p.mu.Unlock()
		p.log.Info("lambda pool: warm start", zap.String("function", fn.Name))
		return inst, nil
	}
	p.mu.Unlock()

	// Cold start with progress reporting.
	p.log.Info("lambda pool: cold start", zap.String("function", fn.Name))
	if cr, ok := p.rt.(*ContainerRuntime); ok {
		return cr.AcquireWithProgress(ctx, fn, progress)
	}
	return p.rt.Acquire(ctx, fn)
}

// Release returns inst to the pool after an invocation.
// If the instance is healthy it is stored for reuse; otherwise it is closed.
// Implements the Runtime interface — inst.FunctionName() is used as the pool key.
func (p *InstancePool) Release(_ context.Context, inst RuntimeInstance, healthy bool) {
	name := inst.FunctionName()
	instHealthy := inst.Healthy()
	if !healthy || !instHealthy {
		p.log.Info("lambda pool: discarding unhealthy instance", zap.String("function", name),
			zap.Bool("healthy_arg", healthy), zap.Bool("inst_healthy", instHealthy))
		if err := inst.Close(); err != nil {
			p.log.Warn("lambda pool: close unhealthy instance failed",
				zap.String("function", name),
				zap.Error(err),
			)
		}
		return
	}
	p.mu.Lock()
	if p.entries == nil {
		p.mu.Unlock()
		p.log.Info("lambda pool: closing instance released after stop", zap.String("function", name))
		if err := inst.Close(); err != nil {
			p.log.Warn("lambda pool: close after stop failed",
				zap.String("function", name),
				zap.Error(err),
			)
		}
		return
	}
	if _, exists := p.entries[name]; exists {
		p.mu.Unlock()
		p.log.Info("lambda pool: closing duplicate warm instance", zap.String("function", name))
		if err := inst.Close(); err != nil {
			p.log.Warn("lambda pool: close duplicate instance failed",
				zap.String("function", name),
				zap.Error(err),
			)
		}
		return
	}
	p.entries[name] = &poolEntry{
		inst:     inst,
		codeHash: inst.CodeHash(),
		lastUsed: p.clk.Now(),
	}
	p.mu.Unlock()
	p.log.Info("lambda pool: instance returned to pool", zap.String("function", name))
}

// CanHandle delegates to the underlying runtime.
func (p *InstancePool) CanHandle(runtimeID string) bool {
	return p.rt.CanHandle(runtimeID)
}

// EvictFunction closes and removes the warm instance for the named function, if any.
// Called by DeleteFunction so the container does not linger after deletion.
func (p *InstancePool) EvictFunction(name string) {
	p.mu.Lock()
	entry, ok := p.entries[name]
	if ok {
		delete(p.entries, name)
	}
	p.mu.Unlock()

	if ok {
		if err := entry.inst.Close(); err != nil {
			p.log.Warn("lambda pool: evict close failed",
				zap.String("function", name),
				zap.Error(err),
			)
		}
	}
}

// Stop shuts down the background sweeper. It does not close existing instances.
func (p *InstancePool) Stop() {
	close(p.stopCh)

	// Cancel log contexts on all remaining instances so log streaming
	// goroutines exit cleanly and don't fight with GC during shutdown.
	p.mu.Lock()
	entries := p.entries
	p.entries = nil
	p.mu.Unlock()
	for name, entry := range entries {
		if err := entry.inst.Close(); err != nil {
			p.log.Debug("lambda pool: stop close failed",
				zap.String("function", name),
				zap.Error(err),
			)
		}
	}
}

// sweepLoop runs until Stop() is called, evicting instances idle longer than
// poolIdleTTL.
func (p *InstancePool) sweepLoop() {
	ticker := p.clk.Ticker(poolSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.sweep()
		case <-p.stopCh:
			return
		}
	}
}

func (p *InstancePool) sweep() {
	cutoff := p.clk.Now().Add(-poolIdleTTL)

	p.mu.Lock()
	var stale []struct {
		name string
		inst RuntimeInstance
	}
	for name, entry := range p.entries {
		if entry.lastUsed.Before(cutoff) {
			stale = append(stale, struct {
				name string
				inst RuntimeInstance
			}{name, entry.inst})
			delete(p.entries, name)
		}
	}
	p.mu.Unlock()

	for _, s := range stale {
		p.log.Debug("lambda pool: evicting idle instance", zap.String("function", s.name))
		if err := s.inst.Close(); err != nil {
			p.log.Warn("lambda pool: sweep close failed",
				zap.String("function", s.name),
				zap.Error(err),
			)
		}
	}
}
