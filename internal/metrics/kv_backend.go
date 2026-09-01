package metrics

// kv_backend.go implements the generic state.Store-backed metrics repository
// for WALStore and -tags nosqlite builds
// (docs/plans/service-metrics-platform.md's "Concrete storage contract across
// tiers" table; docs/plans/storage-plan.md's tier-aware backend selection).
//
// Phases 1-3 left WALStore (and hence every -tags nosqlite build, since
// SQLiteStore/HybridStore don't exist under that tag) on the pure in-memory
// backend (memBucketBackend) — correct, but with no restart continuity:
// WALStore's real durability is its append-log replay over state.Store's own
// Get/Set/Scan surface, and metrics never wrote through that surface.
//
// This backend writes through exactly that surface, using the plan's two
// versioned namespaces:
//
//	metrics:series:v1  <seriesID>                              -> kvSeriesRecord
//	metrics:buckets:v1 <seriesID>/<resolutionSec>/<startMs-20>  -> kvBucketRecord
//
// <startMs-20> is a zero-padded 20-digit decimal encoding of the bucket
// start (Unix millis) — the plan's "fixed-width, lexically sortable UTC
// epoch encoding" — achieved here with plain Sprintf padding rather than a
// dedicated SQL column/index, since a prefix scan is already this backend's
// query shape (see rangeQuery/deleteOlderThan below).
//
// A WALStore replays its append log into its in-memory maps on startup
// before serving any request (wal.go's NewWALStore/replay), so a bucket
// flushed through this backend before a restart is visible again
// immediately after one — exactly the property memBucketBackend cannot
// offer. MemoryStore itself deliberately keeps using memBucketBackend (see
// newBucketBackendFor): the two would be equivalent map operations for a
// store with nothing to restart into, so round-tripping through JSON
// encode/decode there would be pure overhead with no durability benefit —
// the "tier-aware" part of this design is spending that cost only where it
// buys something real.
//
// Retention/list/query here Scan an entire small versioned namespace rather
// than using a real SQL range/index (contrast sqlite_backend.go's indexed
// queries). That is a deliberately proportionate trade for this tier: the
// stored resolution ladder plus its retention bounds total row count to at
// most a few hundred/thousand rows per series, nowhere near the cardinality
// that justified SQS's dedicated-table graduation (storage-plan.md's
// graduation rule) for its unbounded message namespace. If a future
// high-cardinality dimension catalogue changes that, graduate this backend
// the same way, with the same kind of benchmark gate — do not preemptively
// build a paged index for a namespace this bounded.
//
// One corrupt/malformed record (bad JSON, or a series/bucket key whose
// decoded identity fields don't match what the key implies) is skipped, not
// fatal — CLAUDE.md's rule that a single bad persisted record must not fail
// an otherwise-healthy read, matching memBucketBackend and sqlBucketBackend's
// own "continue past one bad row" behavior exactly.
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/overcast-sh/overcast/internal/state"
)

const (
	kvSeriesNamespace  = "metrics:series:v1"
	kvBucketsNamespace = "metrics:buckets:v1"
	kvRecordVersion    = "v1"
)

// kvSeriesRecord is the durable form of one series' canonical identity.
type kvSeriesRecord struct {
	Version     string      `json:"version"`
	Namespace   string      `json:"namespace"`
	MetricName  string      `json:"metricName"`
	Dimensions  []Dimension `json:"dimensions"`
	Unit        string      `json:"unit"`
	FirstSeenMs int64       `json:"firstSeenMs"`
	LastSeenMs  int64       `json:"lastSeenMs"`
}

// kvBucketRecord is the durable form of one aggregate bucket, plus enough of
// its own identity to detect a corrupt/mismatched record on read rather than
// trust the key alone.
type kvBucketRecord struct {
	Version       string  `json:"version"`
	SeriesID      string  `json:"seriesId"`
	ResolutionSec int     `json:"resolutionSec"`
	StartMs       int64   `json:"startMs"`
	Count         float64 `json:"count"`
	Sum           float64 `json:"sum"`
	Min           float64 `json:"min"`
	Max           float64 `json:"max"`
	Unit          string  `json:"unit"`
}

// bucketKVKey builds the lexically-sortable-by-time key for one series'
// bucket at one resolution. Exported at package scope (not a method) so
// rangeQuery's prefix (built without a start value) and a fully-specified
// key share the same seriesID/resolutionSec formatting exactly.
func bucketKVKey(seriesID string, resolutionSec int, startMs int64) string {
	return fmt.Sprintf("%s/%d/%020d", seriesID, resolutionSec, startMs)
}

func bucketKVPrefix(seriesID string, resolutionSec int) string {
	return fmt.Sprintf("%s/%d/", seriesID, resolutionSec)
}

// kvBucketBackend is the generic state.Store-backed bucketBackend
// implementation, selected by newBucketBackendFor for *state.WALStore (see
// backend.go). Safe for concurrent use — it does no locking of its own,
// relying entirely on state.Store's own concurrency contract (every
// implementation in internal/state is safe for concurrent use).
type kvBucketBackend struct {
	store state.Store
}

func newKVBucketBackend(store state.Store) *kvBucketBackend {
	return &kvBucketBackend{store: store}
}

// upsertBuckets mirrors sqlBucketBackend's semantics exactly (same batch,
// same conflict rule) over Get/Set instead of a SQL transaction: a series
// record's FirstSeenMs is preserved from its first write, LastSeenMs only
// ever advances, Unit is filled in once and then kept, and each bucket
// record is a full-value replace — never an additive merge — matching
// activeBucket's doc comment (a flush always carries the bucket's complete
// running total, so replacing is correct, not merely convenient).
//
// This is not one atomic transaction the way sqlBucketBackend's flush is:
// WALStore's Set calls are individually durable (each appends to the WAL
// before returning), but a crash between two of this method's Set calls
// mid-batch can still leave a bucket persisted without its series record (or
// vice versa) having advanced. That is the disclosed "K/V fallback must
// tolerate an interrupted batch by skipping an orphan or malformed bucket
// until the next flush repairs it" trade-off the plan's "Concrete storage
// contract" section calls out — a bucket without a discoverable series
// record still round-trips through rangeQuery/deleteOlderThan (both are
// keyed directly, not via the series listing), and the next successful
// flush for that series repairs metric_series/kvSeriesRecord regardless.
func (b *kvBucketBackend) upsertBuckets(ctx context.Context, buckets []persistedBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	// time.Now() (not an injected clock) matches sqlBucketBackend's own
	// convention for this same first/last-seen bookkeeping field.
	nowMs := time.Now().UnixMilli()
	seenSeries := make(map[string]bool, len(buckets))
	for _, pb := range buckets {
		if !seenSeries[pb.SeriesID] {
			seenSeries[pb.SeriesID] = true
			if err := b.upsertSeries(ctx, pb, nowMs); err != nil {
				return err
			}
		}
		rec := kvBucketRecord{
			Version: kvRecordVersion, SeriesID: pb.SeriesID, ResolutionSec: pb.ResolutionSec,
			StartMs: pb.StartMs, Count: pb.Count, Sum: pb.Sum, Min: pb.Min, Max: pb.Max, Unit: pb.Unit,
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("metrics: encode bucket [%s]: %w", pb.SeriesID, err)
		}
		key := bucketKVKey(pb.SeriesID, pb.ResolutionSec, pb.StartMs)
		if err := b.store.Set(ctx, kvBucketsNamespace, key, string(raw)); err != nil {
			return fmt.Errorf("metrics: write bucket [%s]: %w", pb.SeriesID, err)
		}
	}
	return nil
}

func (b *kvBucketBackend) upsertSeries(ctx context.Context, pb persistedBucket, nowMs int64) error {
	rec := kvSeriesRecord{
		Version: kvRecordVersion, Namespace: pb.Identity.Namespace, MetricName: pb.Identity.Name,
		Dimensions: pb.Identity.Dimensions, Unit: pb.Unit, FirstSeenMs: nowMs, LastSeenMs: nowMs,
	}
	if existingRaw, found, err := b.store.Get(ctx, kvSeriesNamespace, pb.SeriesID); err != nil {
		return fmt.Errorf("metrics: read series [%s]: %w", pb.SeriesID, err)
	} else if found {
		var existing kvSeriesRecord
		if err := json.Unmarshal([]byte(existingRaw), &existing); err == nil {
			rec.FirstSeenMs = existing.FirstSeenMs
			if existing.LastSeenMs > rec.LastSeenMs {
				rec.LastSeenMs = existing.LastSeenMs
			}
			if existing.Unit != "" {
				rec.Unit = existing.Unit
			}
		}
		// A malformed existing record falls through and is overwritten with
		// this write's own identity — self-healing, matching the plan's "an
		// orphan or malformed bucket [is fixed] until the next flush repairs
		// it" tolerance.
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("metrics: encode series [%s]: %w", pb.SeriesID, err)
	}
	if err := b.store.Set(ctx, kvSeriesNamespace, pb.SeriesID, string(raw)); err != nil {
		return fmt.Errorf("metrics: write series [%s]: %w", pb.SeriesID, err)
	}
	return nil
}

// rangeQuery scans the series+resolution's whole bucket prefix and filters
// by decoded StartMs — proportionate for this tier's bounded row count (see
// file doc comment); it deliberately mirrors memBucketBackend's own
// scan-then-filter shape rather than trying to encode a range into the scan
// prefix itself.
func (b *kvBucketBackend) rangeQuery(ctx context.Context, seriesID string, resolutionSec int, startMs, endMs int64) ([]Bucket, error) {
	prefix := bucketKVPrefix(seriesID, resolutionSec)
	kvs, err := b.store.Scan(ctx, kvBucketsNamespace, prefix)
	if err != nil {
		return nil, fmt.Errorf("metrics: range query [%s]: %w", seriesID, err)
	}
	out := make([]Bucket, 0, len(kvs))
	for _, kv := range kvs {
		var rec kvBucketRecord
		if err := json.Unmarshal([]byte(kv.Value), &rec); err != nil {
			continue // malformed record: skip, do not fail the whole read
		}
		if rec.StartMs < startMs || rec.StartMs > endMs {
			continue
		}
		out = append(out, Bucket{
			Start: time.UnixMilli(rec.StartMs).UTC(),
			Count: rec.Count, Sum: rec.Sum, Min: rec.Min, Max: rec.Max, Unit: rec.Unit,
		})
	}
	return out, nil
}

// listSeries scans the whole series namespace and filters by namespace in
// memory — proportionate at this tier's expected series cardinality (see
// file doc comment); ListMetrics-style callers already page/filter further
// up the stack.
func (b *kvBucketBackend) listSeries(ctx context.Context, namespace string) ([]Metric, error) {
	kvs, err := b.store.Scan(ctx, kvSeriesNamespace, "")
	if err != nil {
		return nil, fmt.Errorf("metrics: list series: %w", err)
	}
	out := make([]Metric, 0, len(kvs))
	for _, kv := range kvs {
		var rec kvSeriesRecord
		if err := json.Unmarshal([]byte(kv.Value), &rec); err != nil {
			continue
		}
		if namespace != "" && rec.Namespace != namespace {
			continue
		}
		out = append(out, Metric{Namespace: rec.Namespace, Name: rec.MetricName, Dimensions: rec.Dimensions})
	}
	return out, nil
}

// deleteOlderThan scans every bucket record (there is no per-resolution
// namespace split to narrow the scan against) and deletes those at
// resolutionSec whose start is before cutoffMs. See file doc comment for why
// a full-namespace scan is proportionate here.
func (b *kvBucketBackend) deleteOlderThan(ctx context.Context, resolutionSec int, cutoffMs int64) (int, error) {
	kvs, err := b.store.Scan(ctx, kvBucketsNamespace, "")
	if err != nil {
		return 0, fmt.Errorf("metrics: retention scan: %w", err)
	}
	removed := 0
	for _, kv := range kvs {
		var rec kvBucketRecord
		if err := json.Unmarshal([]byte(kv.Value), &rec); err != nil {
			// A record this backend cannot even parse is not safely
			// re-derivable from its key alone (the key alone doesn't carry
			// enough to confirm ResolutionSec matches without decoding), so
			// leave it — the plan's malformed-record tolerance is "skip",
			// not "guess and delete".
			continue
		}
		if rec.ResolutionSec != resolutionSec || rec.StartMs >= cutoffMs {
			continue
		}
		if !strings.HasPrefix(kv.Key, rec.SeriesID+"/") {
			// Identity/key mismatch: something else wrote this record.
			// Leave it rather than delete on an assumption that no longer
			// holds — the same "detect, don't silently merge" posture the
			// plan's canonical-identity section requires of seriesID itself.
			continue
		}
		if err := b.store.Delete(ctx, kvBucketsNamespace, kv.Key); err != nil {
			return removed, fmt.Errorf("metrics: retention delete [%s]: %w", kv.Key, err)
		}
		removed++
	}
	return removed, nil
}
