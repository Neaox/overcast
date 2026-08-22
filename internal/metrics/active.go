package metrics

import (
	"sync"
	"time"
)

// activeShardCount bounds the sharded-lock fan-out for the in-memory active
// bucket map. Observe only ever holds one shard's lock, so contention is
// spread across concurrent series rather than serialized behind one mutex.
const activeShardCount = 256

// activeBucketKey identifies one (series, minute) in-memory bucket.
type activeBucketKey struct {
	series string // seriesID
	minute int64  // UTC unix-minute (floorToMinute(t).Unix() / 60, see minuteIndex)
}

// activeBucket is the full running aggregate for one series' current minute.
//
// It deliberately never resets while resident: unlike a delta-accumulator, a
// flush snapshot always carries the *complete* total observed so far for that
// minute, and the SQL/backend upsert (backend.go) always REPLACES rather than
// adds. This trades the plan's more general generation-counted delta merge
// for a simpler invariant that is easy to prove race-free: whichever flush
// for a given (series, minute) lands last always carries a total that is
// greater than or equal to every earlier flush of the same bucket, so a later
// write can never regress an earlier one. The cost is that eviction (see
// (*Service) retention sweep) must not drop a bucket from the active map
// until it is both flushed and outside the window new observations could
// still land in.
type activeBucket struct {
	identity Metric // canonical namespace/name/dims, for persistence + query formatting
	start    time.Time
	count    float64
	sum      float64
	min      float64
	max      float64
	unit     string
	dirty    bool
}

func (b *activeBucket) snapshot() Bucket {
	return Bucket{Start: b.start, Count: b.count, Sum: b.sum, Min: b.min, Max: b.max, Unit: b.unit}
}

type activeShard struct {
	mu      sync.Mutex
	buckets map[activeBucketKey]*activeBucket
}

// activeStore is the process-local, sharded set of dirty/recent minute
// buckets — the only structure Observe touches (plan: "Observe is
// synchronous only through a short sharded-lock in-memory update").
type activeStore struct {
	shards [activeShardCount]*activeShard
}

func newActiveStore() *activeStore {
	s := &activeStore{}
	for i := range s.shards {
		s.shards[i] = &activeShard{buckets: make(map[activeBucketKey]*activeBucket)}
	}
	return s
}

// observe updates (or creates) the active bucket for id's current minute.
// identity is only used the first time this (series, minute) is seen; unit
// is likewise only recorded once per bucket ("first non-empty unit wins",
// mirroring cloudwatch's putMetricDataPoint merge).
func (s *activeStore) observe(id string, identity Metric, minute time.Time, unit string, value float64) {
	shard := s.shards[shardIndex(id)]
	key := activeBucketKey{series: id, minute: minute.Unix()}

	shard.mu.Lock()
	b, ok := shard.buckets[key]
	if !ok {
		b = &activeBucket{identity: identity, start: minute, min: value, max: value}
		shard.buckets[key] = b
	} else {
		if value < b.min {
			b.min = value
		}
		if value > b.max {
			b.max = value
		}
	}
	if b.unit == "" {
		b.unit = unit
	}
	b.count++
	b.sum += value
	b.dirty = true
	shard.mu.Unlock()
}

// flushEntry is one dirty bucket captured by snapshotDirty, carrying enough
// of its series' canonical identity to persist without a second lookup.
type flushEntry struct {
	key      activeBucketKey
	identity Metric
	bucket   Bucket
}

// snapshotDirty returns a copy of every currently-dirty bucket and clears
// each one's dirty flag. Buckets stay resident in the map (see
// activeBucket's doc comment) — a concurrent Observe that lands after this
// snapshot is taken re-dirties its bucket for the next flush cycle rather
// than being lost.
func (s *activeStore) snapshotDirty() []flushEntry {
	out := make([]flushEntry, 0)
	for _, shard := range s.shards {
		shard.mu.Lock()
		for key, b := range shard.buckets {
			if !b.dirty {
				continue
			}
			out = append(out, flushEntry{key: key, identity: b.identity, bucket: b.snapshot()})
			b.dirty = false
		}
		shard.mu.Unlock()
	}
	return out
}

// redirtyKeys re-marks the given keys dirty after a failed flush, so the
// next flush cycle retries them. It never modifies count/sum/min/max — the
// resident bucket already holds the full up-to-date total the failed flush
// tried to persist (activeBucket's replace-not-add contract), so simply
// re-arming dirty is enough to make the next flush resend it. A key whose
// bucket has since been evicted (should not happen inside
// activeEvictionGrace, but defensively handled) is silently skipped: there
// is nothing left to retry.
func (s *activeStore) redirtyKeys(keys []activeBucketKey) {
	for _, key := range keys {
		shard := s.shards[shardIndex(key.series)]
		shard.mu.Lock()
		if b, ok := shard.buckets[key]; ok {
			b.dirty = true
		}
		shard.mu.Unlock()
	}
}

// forSeries returns every resident active-overlay bucket for one series,
// used by QueryRange to merge with persisted history. Only buckets already
// known to the caller's series identity are considered — callers resolve
// the seriesID once via canonicalizeDimensions+seriesID before calling this.
func (s *activeStore) forSeries(id string) map[int64]Bucket {
	shard := s.shards[shardIndex(id)]
	shard.mu.Lock()
	defer shard.mu.Unlock()
	out := make(map[int64]Bucket)
	for key, b := range shard.buckets {
		if key.series != id {
			continue
		}
		out[key.minute] = b.snapshot()
	}
	return out
}

// distinctSeries returns the canonical identity of every series with a
// resident active bucket, deduplicated. Used to merge automatically-recorded
// series into ListMetrics even before their first flush.
func (s *activeStore) distinctSeries() []Metric {
	seen := make(map[string]bool)
	out := make([]Metric, 0)
	for _, shard := range s.shards {
		shard.mu.Lock()
		for key, b := range shard.buckets {
			if seen[key.series] {
				continue
			}
			seen[key.series] = true
			out = append(out, b.identity)
		}
		shard.mu.Unlock()
	}
	return out
}

// evictOlderThan removes active buckets whose minute started before cutoff,
// but only when they are not dirty — an unflushed bucket is never evicted,
// so a slow flusher cannot lose data (back-pressure, not silent drop, per the
// plan's performance model). Returns how many buckets were evicted, for
// logging/health.
func (s *activeStore) evictOlderThan(cutoff time.Time) int {
	cutoffUnix := cutoff.Unix()
	evicted := 0
	for _, shard := range s.shards {
		shard.mu.Lock()
		for key, b := range shard.buckets {
			if b.dirty {
				continue
			}
			if key.minute < cutoffUnix {
				delete(shard.buckets, key)
				evicted++
			}
		}
		shard.mu.Unlock()
	}
	return evicted
}
