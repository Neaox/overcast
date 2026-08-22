//go:build !nosqlite

// Package metrics: migration registration for the dedicated metric_series /
// metric_series_dimensions / metric_buckets tables
// (docs/plans/service-metrics-platform.md "Dedicated SQLite schema and
// indexes"). Migration versions 40-49 are reserved for this package — see
// internal/state/migrate.go's Migration doc comment for the reserved-range
// convention; this is the first package to claim the 40s decade.
//
// Build-tagged out of -tags nosqlite builds for the same reason
// internal/services/sqs/migrations.go is: it depends on state.Migration /
// state.RegisterMigration, themselves only defined for !nosqlite builds. This
// is safe because under -tags nosqlite, NewHybridStore and NewSQLiteStore
// both unconditionally fail and cmd/overcast/cmd_serve.go's buildStore falls
// back to state.NewMemoryStore, so newBucketBackendFor's
// state.SQLiteDBProvider check never succeeds at runtime in a nosqlite build
// regardless of whether this migration ever registered — see that file's
// newMessageBackendFor doc comment for the identical reasoning applied there.
package metrics

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Neaox/overcast/internal/state"
)

// migrationMetricTablesVersion creates the schema unconditionally — there is
// no legacy blob data to convert (unlike the SQS/CloudWatch Logs/DynamoDB
// precedents): this package is new, so only one migration is needed.
const migrationMetricTablesVersion = 40

func init() {
	state.RegisterMigration(state.Migration{
		Version: migrationMetricTablesVersion,
		Name:    "create metric_series/metric_series_dimensions/metric_buckets tables",
		Up: func(ctx context.Context, tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS metric_series (
					series_id       BLOB PRIMARY KEY,
					namespace       TEXT NOT NULL,
					metric_name     TEXT NOT NULL,
					dimensions_json TEXT NOT NULL,
					unit            TEXT NOT NULL DEFAULT '',
					first_seen_ms   INTEGER NOT NULL,
					last_seen_ms    INTEGER NOT NULL,
					UNIQUE(namespace, metric_name, dimensions_json)
				)`,
				`CREATE INDEX IF NOT EXISTS idx_metric_series_namespace_name
					ON metric_series(namespace, metric_name, series_id)`,
				`CREATE TABLE IF NOT EXISTS metric_series_dimensions (
					series_id BLOB NOT NULL,
					name      TEXT NOT NULL,
					value     TEXT NOT NULL,
					PRIMARY KEY (series_id, name, value)
				)`,
				`CREATE INDEX IF NOT EXISTS idx_metric_series_dimensions_lookup
					ON metric_series_dimensions(name, value, series_id)`,
				`CREATE TABLE IF NOT EXISTS metric_buckets (
					series_id        BLOB NOT NULL,
					resolution_sec   INTEGER NOT NULL,
					bucket_start_ms  INTEGER NOT NULL,
					sample_count     REAL NOT NULL,
					sum_value        REAL NOT NULL,
					min_value        REAL NOT NULL,
					max_value        REAL NOT NULL,
					unit             TEXT NOT NULL DEFAULT '',
					PRIMARY KEY (series_id, resolution_sec, bucket_start_ms)
				)`,
				`CREATE INDEX IF NOT EXISTS idx_metric_buckets_expiry
					ON metric_buckets(resolution_sec, bucket_start_ms)`,
			}
			for _, stmt := range stmts {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("metrics: %s: %w", stmt, err)
				}
			}
			return nil
		},
	})
}
