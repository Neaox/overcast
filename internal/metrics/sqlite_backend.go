package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// sqlBucketBackend stores series/dimensions/buckets in the dedicated tables
// migrations.go registers (metric_series, metric_series_dimensions,
// metric_buckets), used whenever the configured state.Store satisfies
// state.SQLiteDBProvider (SQLiteStore, HybridStore) — see backend.go's
// newBucketBackendFor.
type sqlBucketBackend struct {
	dbFn func() *sql.DB
	db   *sql.DB
	once sync.Once
	err  error // set by init; sticky
}

func newSQLBucketBackend(dbFn func() *sql.DB) *sqlBucketBackend {
	return &sqlBucketBackend{dbFn: dbFn}
}

// init lazily resolves the *sql.DB on first use, mirroring
// internal/services/sqs/message_backend.go's sqlMessageBackend: dbFn (backed
// by state.SQLiteDBProvider.DB()) blocks until the migration runner has
// finished, and that wait must happen on first real use, not at construction
// (AGENTS.md § startup budget — NewRecorder does no I/O).
func (b *sqlBucketBackend) init() error {
	b.once.Do(func() {
		b.db = b.dbFn()
		if b.db == nil {
			b.err = fmt.Errorf("metrics: sqlite DB unavailable")
		}
	})
	return b.err
}

// upsertBuckets batches every series upsert, dimension insert, and bucket
// upsert into one bounded transaction (plan: "batch series upserts,
// dimension inserts, and bucket upserts in one bounded SQLite transaction ...
// avoids one transaction per invocation while making a flushed bucket
// atomically discoverable with its dimensions").
func (b *sqlBucketBackend) upsertBuckets(ctx context.Context, buckets []persistedBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	if err := b.init(); err != nil {
		return err
	}

	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metrics: begin flush tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	seriesStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metric_series (series_id, namespace, metric_name, dimensions_json, unit, first_seen_ms, last_seen_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(series_id) DO UPDATE SET
			last_seen_ms = MAX(last_seen_ms, excluded.last_seen_ms),
			unit = CASE WHEN metric_series.unit = '' THEN excluded.unit ELSE metric_series.unit END
	`)
	if err != nil {
		return fmt.Errorf("metrics: prepare series upsert: %w", err)
	}
	defer seriesStmt.Close()

	dimStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metric_series_dimensions (series_id, name, value)
		VALUES (?, ?, ?)
		ON CONFLICT(series_id, name, value) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("metrics: prepare dimension insert: %w", err)
	}
	defer dimStmt.Close()

	bucketStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metric_buckets (series_id, resolution_sec, bucket_start_ms, sample_count, sum_value, min_value, max_value, unit)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(series_id, resolution_sec, bucket_start_ms) DO UPDATE SET
			sample_count = excluded.sample_count,
			sum_value    = excluded.sum_value,
			min_value    = excluded.min_value,
			max_value    = excluded.max_value,
			unit         = excluded.unit
	`)
	if err != nil {
		return fmt.Errorf("metrics: prepare bucket upsert: %w", err)
	}
	defer bucketStmt.Close()

	nowMs := time.Now().UnixMilli()
	seenSeries := make(map[string]bool)
	for _, pb := range buckets {
		if !seenSeries[pb.SeriesID] {
			seenSeries[pb.SeriesID] = true
			dimsJSON, err := dimensionsJSON(pb.Identity.Dimensions)
			if err != nil {
				return err
			}
			if _, err := seriesStmt.ExecContext(ctx, pb.SeriesID, pb.Identity.Namespace, pb.Identity.Name, dimsJSON, pb.Unit, nowMs, nowMs); err != nil {
				return fmt.Errorf("metrics: upsert series [%s]: %w", pb.SeriesID, err)
			}
			for _, d := range pb.Identity.Dimensions {
				if _, err := dimStmt.ExecContext(ctx, pb.SeriesID, d.Name, d.Value); err != nil {
					return fmt.Errorf("metrics: insert dimension [%s]: %w", pb.SeriesID, err)
				}
			}
		}
		if _, err := bucketStmt.ExecContext(ctx, pb.SeriesID, pb.ResolutionSec, pb.StartMs, pb.Count, pb.Sum, pb.Min, pb.Max, pb.Unit); err != nil {
			return fmt.Errorf("metrics: upsert bucket [%s]: %w", pb.SeriesID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metrics: commit flush tx: %w", err)
	}
	committed = true
	return nil
}

// rangeQuery serves the dominant chart/statistics query directly off the
// bucket primary key: series_id = ? AND resolution_sec = ? AND
// bucket_start_ms BETWEEN ? AND ?.
func (b *sqlBucketBackend) rangeQuery(ctx context.Context, seriesID string, resolutionSec int, startMs, endMs int64) ([]Bucket, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx, `
		SELECT bucket_start_ms, sample_count, sum_value, min_value, max_value, unit
		FROM metric_buckets
		WHERE series_id = ? AND resolution_sec = ? AND bucket_start_ms BETWEEN ? AND ?
		ORDER BY bucket_start_ms ASC
	`, seriesID, resolutionSec, startMs, endMs)
	if err != nil {
		return nil, fmt.Errorf("metrics: range query [%s]: %w", seriesID, err)
	}
	defer rows.Close()

	out := make([]Bucket, 0)
	for rows.Next() {
		var startMs int64
		var count, sum, min, max float64
		var unit string
		if err := rows.Scan(&startMs, &count, &sum, &min, &max, &unit); err != nil {
			// A malformed row is skipped, not fatal — CLAUDE.md's rule that one
			// corrupt persisted record must not fail the whole read.
			continue
		}
		out = append(out, Bucket{Start: time.UnixMilli(startMs).UTC(), Count: count, Sum: sum, Min: min, Max: max, Unit: unit})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metrics: range query [%s]: %w", seriesID, err)
	}
	return out, nil
}

// listSeries serves the metric picker / ListMetrics discovery path off
// idx_metric_series_namespace_name, never scanning bucket rows.
func (b *sqlBucketBackend) listSeries(ctx context.Context, namespace string) ([]Metric, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	query := `SELECT series_id, namespace, metric_name, dimensions_json FROM metric_series`
	args := []any{}
	if namespace != "" {
		query += ` WHERE namespace = ?`
		args = append(args, namespace)
	}
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("metrics: list series: %w", err)
	}
	defer rows.Close()

	out := make([]Metric, 0)
	for rows.Next() {
		var seriesID, ns, name, dimsJSON string
		if err := rows.Scan(&seriesID, &ns, &name, &dimsJSON); err != nil {
			continue
		}
		dims, err := dimensionsFromJSON(dimsJSON)
		if err != nil {
			// Malformed dimensions_json: skip this series rather than failing
			// the whole listing.
			continue
		}
		out = append(out, Metric{Namespace: ns, Name: name, Dimensions: dims})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metrics: list series: %w", err)
	}
	return out, nil
}

// deleteOlderThan backs retention with an indexed range delete
// (idx_metric_buckets_expiry) rather than a full-table scan.
func (b *sqlBucketBackend) deleteOlderThan(ctx context.Context, resolutionSec int, cutoffMs int64) (int, error) {
	if err := b.init(); err != nil {
		return 0, err
	}
	res, err := b.db.ExecContext(ctx, `
		DELETE FROM metric_buckets WHERE resolution_sec = ? AND bucket_start_ms < ?
	`, resolutionSec, cutoffMs)
	if err != nil {
		return 0, fmt.Errorf("metrics: retention delete: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
