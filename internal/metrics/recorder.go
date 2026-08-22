package metrics

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/state"
)

const (
	// resolutionSeconds is the fine (base) resolution Observe/Flush/the active
	// overlay always write at — AWS's one-minute service-metric model. Phase 3
	// adds the plan's coarser 300s/3600s rollup tiers (rollup.go) computed
	// from this one by a background maintenance job; Observe itself is
	// unchanged and never touches them.
	resolutionSeconds = 60

	// retention is the 60s tier's retention. Phase 1 kept this at 1h
	// ("initially matching the existing CloudWatch one-hour local-development
	// retention"); phase 3 extends it to the plan's full "Persist the
	// following resolution ladder" table so the 6h/24h chart controls (which
	// the plan intentionally keeps at 1-minute resolution) have real data to
	// read, not just GetMetricStatistics/alarm evaluation's shorter windows.
	retention = 24 * time.Hour

	// flushInterval is how often dirty active buckets are batched to the
	// backend. Bounded and coalescing, never inline with Observe.
	flushInterval = 5 * time.Second

	// sweepInterval is how often the retention/eviction/rollup pass runs. Well
	// under retention so a backend never accumulates much more than one
	// interval's worth of overdue rows between sweeps.
	sweepInterval = 5 * time.Minute

	// activeEvictionGrace is how long after a minute closes its active bucket
	// stays resident (and hence merged into query results without a backend
	// round trip) before the sweep may evict it. Larger than flushInterval so
	// eviction never races a bucket that has not had its final flush yet.
	activeEvictionGrace = 3 * time.Minute
)

// Service is the concrete Recorder: the shared metric repository backing
// every service's automatic observations, and the read side
// internal/services/cloudwatch merges its own PutMetricData-supplied points
// with. Construct with NewRecorder; call Stop before the underlying
// state.Store closes.
type Service struct {
	active  *activeStore
	backend bucketBackend
	clk     clock.Clock
	log     *zap.Logger

	flushTicker *clock.Ticker
	sweepTicker *clock.Ticker

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup

	stopped atomic.Bool
}

// NewRecorder constructs a Service with no I/O: the flush and sweep loops
// only register their tickers here and do not touch store or backend state
// until their first tick (AGENTS.md § startup budget), matching
// cloudwatch.New's own construction contract. store should be the
// unwrapped, service-scoped store (state.Unwrap(store, "metrics")) — callers
// (internal/router) are responsible for that, exactly as
// internal/services/sqs.New is for its own dedicated table.
func NewRecorder(store state.Store, clk clock.Clock, logger *zap.Logger) *Service {
	s := &Service{
		active:  newActiveStore(),
		backend: newBucketBackendFor(state.Unwrap(store, "metrics")),
		clk:     clk,
		log:     logger,
		stopCh:  make(chan struct{}),
	}
	s.flushTicker = clk.Ticker(flushInterval)
	s.sweepTicker = clk.Ticker(sweepInterval)
	s.wg.Add(2)
	go s.runFlushLoop()
	go s.runSweepLoop()
	return s
}

// Observe satisfies Recorder. See metrics.go's doc comment for the exact
// hot-path guarantee.
func (s *Service) Observe(_ context.Context, o Observation) error {
	if s.stopped.Load() {
		return errRecorderStopped
	}
	if o.Namespace == "" || o.Name == "" {
		return errInvalidObservation
	}
	dims := canonicalizeDimensions(o.Dimensions)
	id := seriesID(o.Namespace, o.Name, dims)
	ts := o.Timestamp
	if ts.IsZero() {
		ts = s.clk.Now()
	}
	minute := floorToMinute(ts)
	identity := Metric{Namespace: o.Namespace, Name: o.Name, Dimensions: dims}
	s.active.observe(id, identity, minute, o.Unit, o.Value)
	return nil
}

// Flush synchronously drains every currently-dirty active bucket to the
// backend. Safe to call concurrently with Observe and with the background
// flush loop; used directly by Stop and by tests that need deterministic
// visibility without waiting on a real ticker.
func (s *Service) Flush(ctx context.Context) error {
	dirty := s.active.snapshotDirty()
	if len(dirty) == 0 {
		return nil
	}
	batch := make([]persistedBucket, 0, len(dirty))
	for _, e := range dirty {
		batch = append(batch, persistedBucket{
			SeriesID: e.key.series, Identity: e.identity, ResolutionSec: resolutionSeconds,
			StartMs: e.bucket.Start.UnixMilli(), Count: e.bucket.Count, Sum: e.bucket.Sum,
			Min: e.bucket.Min, Max: e.bucket.Max, Unit: e.bucket.Unit,
		})
	}
	if err := s.backend.upsertBuckets(ctx, batch); err != nil {
		// Never silently report success — the plan's write/read/failure
		// semantics require dirty aggregates to be retained and retried, not
		// dropped. Buckets are already un-dirtied by snapshotDirty; mark them
		// dirty again so the next flush retries them.
		keys := make([]activeBucketKey, 0, len(dirty))
		for _, e := range dirty {
			keys = append(keys, e.key)
		}
		s.active.redirtyKeys(keys)
		if s.log != nil {
			s.log.Warn("metrics: flush failed, will retry", zap.Error(err), zap.Int("buckets", len(batch)))
		}
		return err
	}
	return nil
}

func (s *Service) runFlushLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.flushTicker.C:
			if err := s.Flush(context.Background()); err != nil && s.log != nil {
				s.log.Debug("metrics: periodic flush error", zap.Error(err))
			}
		}
	}
}

func (s *Service) runSweepLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.sweepTicker.C:
			s.sweepOnce(context.Background())
		}
	}
}

// sweepOnce evicts aged-out active buckets, deletes expired persisted buckets
// at every stored resolution tier, and rolls the fine tier up into the
// coarser 300s/3600s tiers (rollup.go). Extracted from the ticker loop so
// tests can trigger one sweep deterministically.
func (s *Service) sweepOnce(ctx context.Context) {
	now := s.clk.Now().UTC()
	s.active.evictOlderThan(now.Add(-activeEvictionGrace))
	for _, tier := range resolutionTiers {
		cutoffMs := now.Add(-tier.retention).UnixMilli()
		if _, err := s.backend.deleteOlderThan(ctx, tier.seconds, cutoffMs); err != nil && s.log != nil {
			s.log.Warn("metrics: retention sweep failed", zap.Int("resolutionSeconds", tier.seconds), zap.Error(err))
		}
	}
	s.rollupOnce(ctx, now)
}

// Stop stops accepting new background ticks, flushes every dirty bucket
// within ctx's deadline, and returns. Observe remains safe to call after Stop
// (it starts returning errRecorderStopped) so callers do not need to
// synchronize their own shutdown against it.
func (s *Service) Stop(ctx context.Context) {
	s.stopOnce.Do(func() {
		s.stopped.Store(true)
		close(s.stopCh)
	})
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
	// Best-effort final drain — bounded by the caller's context, and a
	// failure here is logged by Flush itself rather than panicking shutdown.
	_ = s.Flush(ctx)
}
