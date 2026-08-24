package metrics

// rollup.go implements the plan's fuller resolution ladder
// (docs/plans/service-metrics-platform.md "Access patterns, retention, and
// graph time spans": "Persist the following resolution ladder, computed by
// the maintenance job from immutable bucket aggregates (count/sum/min/max
// compose exactly)") — phase 3's answer to phase 1's explicitly deferred
// "300s/3600s resolution rollup ladder and its 7d/30d retention tiers".
//
// A coarser tier is never written by Observe/Flush (the active overlay only
// ever holds 60s buckets); it is derived entirely from the next-finer
// persisted tier by this package's own background sweep, using the same
// bucketBackend.upsertBuckets/rangeQuery methods the fine tier already uses —
// no second storage engine, no second query API, matching the plan's
// "aggregate selected stored resolution into requested period" principle.
import (
	"context"
	"math"
	"time"

	"go.uber.org/zap"
)

// resolutionTier is one stored resolution and how long it is retained. Listed
// finest-to-coarsest; retention must be non-decreasing down the list so
// SelectResolution's "finest resolution whose retention covers the request"
// scan can stop at the first match.
type resolutionTier struct {
	seconds   int
	retention time.Duration
}

// resolutionTiers extends the plan's original table (60s/24h, 300s/7d,
// 3600s/30d — "initial local-development budgets, not AWS retention claims")
// by retaining the 300s tier for the full 30 days (#1307): that lets month-
// scale charts answer at sub-hourly periods (ParseChartRange's 30d row is
// 15m) instead of falling back to the 3600s tier. Cost is bounded — at most
// 8640 constant-size 300s buckets per active series instead of 2016. The
// 3600s tier remains as the cheap final fallback should the 300s sweep ever
// lag, and because removing a tier would orphan its already-persisted
// buckets.
var resolutionTiers = []resolutionTier{
	{seconds: resolutionSeconds, retention: retention}, // 60s / 24h
	{seconds: 300, retention: 30 * 24 * time.Hour},
	{seconds: 3600, retention: 30 * 24 * time.Hour},
}

// rollupSpec describes how one coarser tier is derived from the next-finer
// stored tier.
type rollupSpec struct {
	targetSeconds int
	sourceSeconds int
	window        time.Duration
	// catchUpWindows is how many trailing, already-closed windows are
	// recomputed on every sweep tick. Recomputation is idempotent (a rollup
	// bucket is always a full replace of the same aggregate, never an
	// additive delta — mirroring activeBucket's own replace-not-add
	// contract), so this is purely a self-healing margin against a missed
	// tick or a late-arriving flush, not a correctness requirement for the
	// most recent window alone.
	catchUpWindows int
}

var rollupSpecs = []rollupSpec{
	{targetSeconds: 300, sourceSeconds: resolutionSeconds, window: 5 * time.Minute, catchUpWindows: 3},
	{targetSeconds: 3600, sourceSeconds: 300, window: time.Hour, catchUpWindows: 3},
}

// rollupSafetyMargin is how far behind "now" a rollup window's end must be
// before it is considered fully closed — large enough that every 60s bucket
// inside it is guaranteed to have had its final flush (activeEvictionGrace)
// plus one more flush cycle of slack.
const rollupSafetyMargin = activeEvictionGrace + flushInterval

// rollupOnce recomputes the trailing catchUpWindows-many fully-closed windows
// for every rollupSpec, in finest-target-first order — 300s before 3600s —
// so a same-tick 3600s rollup that sources from 300s sees the freshest
// 300s data the 60s→300s pass in this same call just wrote.
func (s *Service) rollupOnce(ctx context.Context, now time.Time) {
	safeNow := now.Add(-rollupSafetyMargin)
	for _, spec := range rollupSpecs {
		s.rollupSpecOnce(ctx, spec, safeNow)
	}
}

func (s *Service) rollupSpecOnce(ctx context.Context, spec rollupSpec, safeNow time.Time) {
	// series is every known series across every namespace — the rollup ladder
	// is namespace-agnostic, same as the storage/backend layer itself.
	series, err := s.backend.listSeries(ctx, "")
	if err != nil {
		if s.log != nil {
			s.log.Warn("metrics: rollup listSeries failed", zap.Int("targetResolutionSeconds", spec.targetSeconds), zap.Error(err))
		}
		return
	}
	if len(series) == 0 {
		return
	}

	// lastClosedStart is the most recent window whose end is at or before
	// safeNow — floorTo(safeNow) always leaves that window still open (its
	// end is >= safeNow only when safeNow lands exactly on a boundary), so
	// step back one full window to guarantee closure.
	lastClosedStart := safeNow.UTC().Truncate(spec.window).Add(-spec.window)

	for i := 0; i < spec.catchUpWindows; i++ {
		windowStart := lastClosedStart.Add(-time.Duration(i) * spec.window)
		if windowStart.Before(time.Unix(0, 0)) {
			break
		}
		windowEnd := windowStart.Add(spec.window)
		s.rollupWindow(ctx, spec, series, windowStart, windowEnd)
	}
}

func (s *Service) rollupWindow(ctx context.Context, spec rollupSpec, series []Metric, windowStart, windowEnd time.Time) {
	startMs := windowStart.UnixMilli()
	endMs := windowEnd.Add(-time.Millisecond).UnixMilli()

	batch := make([]persistedBucket, 0, len(series))
	for _, m := range series {
		id := seriesID(m.Namespace, m.Name, m.Dimensions)
		srcBuckets, err := s.backend.rangeQuery(ctx, id, spec.sourceSeconds, startMs, endMs)
		if err != nil {
			if s.log != nil {
				s.log.Warn("metrics: rollup rangeQuery failed",
					zap.String("namespace", m.Namespace), zap.String("metric", m.Name),
					zap.Int("sourceResolutionSeconds", spec.sourceSeconds), zap.Error(err))
			}
			continue
		}
		// No source buckets in this window means no observation was ever
		// made in it — the plan's "a missing metric means no observation was
		// emitted, not a synthetic zero" rule applies to a rollup bucket
		// exactly as it does to a fine one, so nothing is written.
		if len(srcBuckets) == 0 {
			continue
		}
		count, sum, min, max, unit := sumBuckets(srcBuckets)
		batch = append(batch, persistedBucket{
			SeriesID: id, Identity: m, ResolutionSec: spec.targetSeconds,
			StartMs: startMs, Count: count, Sum: sum, Min: min, Max: max, Unit: unit,
		})
	}
	if len(batch) == 0 {
		return
	}
	if err := s.backend.upsertBuckets(ctx, batch); err != nil && s.log != nil {
		s.log.Warn("metrics: rollup upsert failed", zap.Int("targetResolutionSeconds", spec.targetSeconds), zap.Int("buckets", len(batch)), zap.Error(err))
	}
}

// sumBuckets composes a set of same-series buckets into one aggregate —
// count/sum add, min/max fold, and the first non-empty unit wins. Buckets
// compose exactly (recorder.go's Bucket doc comment), which is what makes
// this safe both for a rollup (finer buckets into one coarser one) and for
// aggregateIntoPeriods' read-time regrouping (planner.go).
func sumBuckets(bs []Bucket) (count, sum, min, max float64, unit string) {
	min = math.MaxFloat64
	max = -math.MaxFloat64
	for _, b := range bs {
		count += b.Count
		sum += b.Sum
		if b.Min < min {
			min = b.Min
		}
		if b.Max > max {
			max = b.Max
		}
		if unit == "" {
			unit = b.Unit
		}
	}
	return count, sum, min, max, unit
}
