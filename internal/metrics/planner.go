package metrics

// planner.go is the query planner the plan's "Access patterns, retention, and
// graph time spans" section describes: "The query planner selects the finest
// resolution whose retention fully covers the requested interval, then uses a
// period that is a multiple of that resolution." It is the read path the web
// Monitor tab's BFF endpoint (chart.go) is built on — QueryRange/queryRangeAt
// remain the only storage query, this only chooses which stored resolution to
// read and, when the caller's requested display period is coarser than that,
// regroups the returned buckets — never a second query engine.
import (
	"context"
	"sort"
	"time"
)

// SelectResolution returns the finest stored resolution (one of
// resolutionTiers' seconds values) whose retention fully covers
// [start, now] — i.e. the oldest point in the request is still within that
// tier's retention window. Tiers are checked finest-first, so the first
// covering tier wins.
func SelectResolution(now, start time.Time) int {
	age := now.Sub(start)
	for _, tier := range resolutionTiers {
		if age <= tier.retention {
			return tier.seconds
		}
	}
	// Older than every tier's retention: best effort is the coarsest tier —
	// the caller still gets whatever of it survived, not an error.
	return resolutionTiers[len(resolutionTiers)-1].seconds
}

// chartRange is one of the plan's "coherent controls": "1h/1m, 6h/1m, 24h/5m,
// 7d/5m, and 30d/1h". period here is the *display* granularity the UI shows,
// which the query planner treats as a floor — QueryAuto never returns a finer
// period than requested, but may fall back to a coarser one when the stored
// resolution available for the request's age is coarser still.
type chartRange struct {
	span   time.Duration
	period time.Duration
}

var chartRangesByToken = map[string]chartRange{
	"1h":  {span: time.Hour, period: time.Minute},
	"6h":  {span: 6 * time.Hour, period: time.Minute},
	"24h": {span: 24 * time.Hour, period: 5 * time.Minute},
	"7d":  {span: 7 * 24 * time.Hour, period: 5 * time.Minute},
	"30d": {span: 30 * 24 * time.Hour, period: time.Hour},
}

// ParseChartRange resolves one of the plan's five range tokens ("1h", "6h",
// "24h", "7d", "30d") to a concrete [start, now] window and display period.
// ok is false for anything else — callers (the BFF read endpoint) must reject
// an unrecognized range rather than silently substituting a default, so the
// UI never receives a range/period combination the plan did not design for.
func ParseChartRange(token string, now time.Time) (start, end time.Time, period time.Duration, ok bool) {
	r, found := chartRangesByToken[token]
	if !found {
		return time.Time{}, time.Time{}, 0, false
	}
	end = now.UTC()
	start = end.Add(-r.span)
	return start, end, r.period, true
}

// QueryAuto answers a chart-shaped request over [start, end] at a requested
// display period: it selects the finest stored resolution whose retention
// covers the range (SelectResolution), reads it (queryRangeAt), and — when
// period is coarser than that resolution — regroups the returned buckets into
// period-aligned points (aggregateIntoPeriods). It never returns a resolution
// finer than what is actually stored for the request's age, and the
// resolution actually used is returned so a caller can be honest about it
// (e.g. "showing hourly data" when a finer period was asked for data too old
// to have it).
func (s *Service) QueryAuto(ctx context.Context, namespace, name string, dims []Dimension, start, end time.Time, period time.Duration) ([]Bucket, int, error) {
	now := s.clk.Now().UTC()
	resSec := SelectResolution(now, start)

	periodSec := int(period.Seconds())
	if periodSec < resSec {
		// Never finer than what is actually stored for this age.
		periodSec = resSec
	} else if periodSec%resSec != 0 {
		// Round down to a multiple of the stored resolution (plan: "uses a
		// period that is a multiple of that resolution").
		periodSec -= periodSec % resSec
		if periodSec < resSec {
			periodSec = resSec
		}
	}

	raw, err := s.queryRangeAt(ctx, namespace, name, dims, resSec, start, end)
	if err != nil {
		return nil, 0, err
	}
	if periodSec == resSec {
		return raw, resSec, nil
	}
	return aggregateIntoPeriods(raw, periodSec), resSec, nil
}

// aggregateIntoPeriods regroups already-queried buckets (all at the same
// stored resolution) into coarser period-aligned points, using the same
// compose-exactly aggregation the rollup ladder itself uses (sumBuckets).
// Unlike the rollup ladder this never persists anything — it is purely a
// read-time reshape for display.
func aggregateIntoPeriods(buckets []Bucket, periodSec int) []Bucket {
	periodDur := time.Duration(periodSec) * time.Second
	groups := make(map[int64][]Bucket)
	keys := make([]int64, 0)
	for _, b := range buckets {
		key := b.Start.Truncate(periodDur).UnixMilli()
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], b)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	out := make([]Bucket, 0, len(keys))
	for _, key := range keys {
		count, sum, min, max, unit := sumBuckets(groups[key])
		out = append(out, Bucket{Start: time.UnixMilli(key).UTC(), Count: count, Sum: sum, Min: min, Max: max, Unit: unit})
	}
	return out
}
