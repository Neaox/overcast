package metrics

import (
	"context"
	"sort"
	"time"
)

// ListMetrics returns every distinct series this package knows about,
// merging the durable backend with any series that only exists in the
// not-yet-flushed active overlay — so a metric recorded a moment ago is
// discoverable immediately, matching QueryRange's own merge guarantee.
// namespace filters to one AWS namespace; empty returns every namespace.
func (s *Service) ListMetrics(ctx context.Context, namespace string) ([]Metric, error) {
	persisted, err := s.backend.listSeries(ctx, namespace)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(persisted))
	out := make([]Metric, 0, len(persisted))
	for _, m := range persisted {
		id := seriesID(m.Namespace, m.Name, m.Dimensions)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, m)
	}
	for _, m := range s.active.distinctSeries() {
		if namespace != "" && m.Namespace != namespace {
			continue
		}
		id := seriesID(m.Namespace, m.Name, m.Dimensions)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, m)
	}
	return out, nil
}

// QueryRange returns one series' one-minute buckets covering
// [start, end], merging persisted history with the in-memory active overlay
// so a just-recorded observation is visible before its first flush (plan:
// "Reads merge persisted buckets with current in-memory buckets"). The
// active overlay always wins over its persisted counterpart for the same
// minute: activeBucket never resets while resident, so its total is always
// greater than or equal to whatever has been flushed for that minute so far.
//
// Returned buckets are sorted ascending by Start; a minute with no
// observation is simply absent — callers must not treat that as a zero
// (plan: "A missing metric means no observation was emitted, not a
// synthetic zero").
func (s *Service) QueryRange(ctx context.Context, namespace, name string, dims []Dimension, start, end time.Time) ([]Bucket, error) {
	canon := canonicalizeDimensions(dims)
	id := seriesID(namespace, name, canon)
	startMs := floorToMinute(start).UnixMilli()
	endMs := end.UTC().UnixMilli()

	persisted, err := s.backend.rangeQuery(ctx, id, resolutionSeconds, startMs, endMs)
	if err != nil {
		return nil, err
	}
	merged := make(map[int64]Bucket, len(persisted))
	for _, b := range persisted {
		merged[b.Start.UnixMilli()] = b
	}
	for minuteUnix, b := range s.active.forSeries(id) {
		ms := minuteUnix * 1000
		if ms < startMs || ms > endMs {
			continue
		}
		merged[ms] = b
	}

	out := make([]Bucket, 0, len(merged))
	for _, b := range merged {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, nil
}
