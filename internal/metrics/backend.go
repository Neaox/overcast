package metrics

// backend.go selects and defines the metrics repository's persistence layer.
//
// Two implementations, mirroring the SQS messages / DynamoDB items / CloudWatch
// Logs events precedent (internal/services/sqs/message_backend.go,
// internal/services/dynamodb/item_store.go,
// internal/services/cloudwatch/logs/event_backend.go):
//
//	memBucketBackend — in-process maps; disappears on restart (MemoryStore,
//	                   WALStore, and -tags nosqlite builds, none of which
//	                   satisfy state.SQLiteDBProvider).
//	sqlBucketBackend  — dedicated SQLite tables (SQLiteStore, HybridStore).
//
// Phase 1 deliberately does not add a third, generically-persisted-K/V
// backend for WALStore the way the plan's "Concrete storage contract across
// tiers" section sketches (metrics:series:v1 / metrics:buckets:v1 versioned
// records) — see docs/plans/service-metrics-platform.md's Phase 1 status note
// for why this is called out as an explicit, disclosed reduction rather than
// a silent gap: WALStore and nosqlite builds get correct, fully in-memory
// automatic-metrics behavior (Observe/query/alarm evaluation all work), they
// just do not survive a process restart. A user-supplied custom PutMetricData
// point is unaffected either way — that data still goes through
// internal/services/cloudwatch's own existing storage, untouched by this
// package.
import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// persistedBucket is one durable minute-resolution aggregate, plus enough of
// its series' canonical identity to serve ListMetrics/dimension filters
// without a join back to an in-memory-only structure.
type persistedBucket struct {
	SeriesID      string
	Identity      Metric
	ResolutionSec int
	StartMs       int64
	Count         float64
	Sum           float64
	Min           float64
	Max           float64
	Unit          string
}

// bucketBackend is the narrow interface every metrics storage backend must
// implement. All methods are safe for concurrent use.
type bucketBackend interface {
	// upsertBuckets durably writes a batch of buckets, replacing whatever
	// was previously stored for the same (seriesID, resolution, start) —
	// see activeBucket's doc comment for why replace (not additive-delta) is
	// the correct semantics given this package's flush model.
	upsertBuckets(ctx context.Context, buckets []persistedBucket) error

	// rangeQuery returns every persisted bucket for one series within
	// [startMs, endMs], at the given resolution.
	rangeQuery(ctx context.Context, seriesID string, resolutionSec int, startMs, endMs int64) ([]Bucket, error)

	// listSeries returns the canonical identity of every known series,
	// optionally filtered to one namespace (empty = all).
	listSeries(ctx context.Context, namespace string) ([]Metric, error)

	// deleteOlderThan physically removes buckets at resolutionSec whose
	// start is before cutoffMs. Returns the number removed, for logging.
	deleteOlderThan(ctx context.Context, resolutionSec int, cutoffMs int64) (int, error)
}

// newBucketBackendFor selects the storage backend based on the store's
// concrete type, mirroring internal/services/sqs/message_backend.go's
// newMessageBackendFor. Callers must pass a store already resolved with
// state.Unwrap, or an OVERCAST_STATE_<SVC> override on an unrelated service
// would silently downgrade metrics to the in-memory backend (the same
// interface-erasure hazard documented there).
func newBucketBackendFor(store state.Store) bucketBackend {
	if provider, ok := store.(state.SQLiteDBProvider); ok {
		return newSQLBucketBackend(provider.DB)
	}
	return newMemBucketBackend()
}

// ─── In-memory backend ───────────────────────────────────────────────────

type memSeriesEntry struct {
	identity Metric
	buckets  map[int]map[int64]persistedBucket // resolutionSec -> startMs -> bucket
}

// memBucketBackend is a plain in-process map. Used for MemoryStore, WALStore,
// and any -tags nosqlite build, matching the plan's "In-memory series/
// dimension/bucket maps; disappears on restart" tier row.
type memBucketBackend struct {
	mu     sync.RWMutex
	series map[string]*memSeriesEntry
}

func newMemBucketBackend() *memBucketBackend {
	return &memBucketBackend{series: make(map[string]*memSeriesEntry)}
}

func (b *memBucketBackend) upsertBuckets(_ context.Context, buckets []persistedBucket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, pb := range buckets {
		entry, ok := b.series[pb.SeriesID]
		if !ok {
			entry = &memSeriesEntry{identity: pb.Identity, buckets: make(map[int]map[int64]persistedBucket)}
			b.series[pb.SeriesID] = entry
		}
		byStart, ok := entry.buckets[pb.ResolutionSec]
		if !ok {
			byStart = make(map[int64]persistedBucket)
			entry.buckets[pb.ResolutionSec] = byStart
		}
		byStart[pb.StartMs] = pb
	}
	return nil
}

func (b *memBucketBackend) rangeQuery(_ context.Context, seriesID string, resolutionSec int, startMs, endMs int64) ([]Bucket, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry, ok := b.series[seriesID]
	if !ok {
		return nil, nil
	}
	byStart, ok := entry.buckets[resolutionSec]
	if !ok {
		return nil, nil
	}
	out := make([]Bucket, 0, len(byStart))
	for start, pb := range byStart {
		if start < startMs || start > endMs {
			continue
		}
		out = append(out, Bucket{
			Start: time.UnixMilli(pb.StartMs).UTC(),
			Count: pb.Count, Sum: pb.Sum, Min: pb.Min, Max: pb.Max, Unit: pb.Unit,
		})
	}
	return out, nil
}

func (b *memBucketBackend) listSeries(_ context.Context, namespace string) ([]Metric, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Metric, 0, len(b.series))
	for _, entry := range b.series {
		if namespace != "" && entry.identity.Namespace != namespace {
			continue
		}
		out = append(out, entry.identity)
	}
	return out, nil
}

func (b *memBucketBackend) deleteOlderThan(_ context.Context, resolutionSec int, cutoffMs int64) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	removed := 0
	for _, entry := range b.series {
		byStart, ok := entry.buckets[resolutionSec]
		if !ok {
			continue
		}
		for start := range byStart {
			if start < cutoffMs {
				delete(byStart, start)
				removed++
			}
		}
	}
	return removed, nil
}

// ─── SQLite-backed dimension JSON helper (shared with migrations.go) ───────

// dimensionsJSON renders dims (already canonicalized) as the ordered JSON
// array persisted in metric_series.dimensions_json — lossless and stable so
// two writes of the same series produce byte-identical JSON, matching
// (namespace, metric_name, dimensions_json)'s UNIQUE constraint.
func dimensionsJSON(dims []Dimension) (string, error) {
	raw, err := json.Marshal(dims)
	if err != nil {
		return "", fmt.Errorf("metrics: encode dimensions: %w", err)
	}
	return string(raw), nil
}

func dimensionsFromJSON(raw string) ([]Dimension, error) {
	if raw == "" {
		return nil, nil
	}
	var dims []Dimension
	if err := json.Unmarshal([]byte(raw), &dims); err != nil {
		return nil, err
	}
	return dims, nil
}
