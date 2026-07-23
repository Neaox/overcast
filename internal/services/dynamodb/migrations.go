//go:build !nosqlite

// Package dynamodb: migration registration for the dynamodb_items and
// dynamodb_stream_records dedicated tables (docs/storage-plan.md Phase 3
// item 3.9).
//
// Build-tagged out of `-tags nosqlite` builds because it depends on
// state.Migration / state.RegisterMigration, which are themselves only
// defined for !nosqlite builds (compare internal/state/migrate.go against
// internal/state/sqlite_hybrid_nosqlite.go). This is safe: under
// -tags nosqlite, NewHybridStore and NewSQLiteStore both unconditionally
// return an error and cmd/overcast/cmd_serve.go's buildStore falls back to
// state.NewMemoryStore, so no real store ever satisfies
// state.SQLiteDBProvider at runtime in a nosqlite build —
// newItemBackendFor/newStreamBackendFor (service.go, not build-tagged)
// always select the memory backend there regardless of whether these
// migrations ever registered. CloudWatch Logs' event backend relies on the
// identical reasoning (see internal/services/cloudwatch/logs/migrations.go).
package dynamodb

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Neaox/overcast/internal/state"
)

// Migration versions 20-29 are reserved for the DynamoDB items/stream-
// records tables — see internal/state/migrate.go's Migration doc comment
// for the reserved-range convention.
//
// Both migrations are pure schema setup: the DDL here is copied verbatim
// from what item_store.go's sqlItemBackend.init() and stream_store.go's
// sqlStreamBackend.init() used to run themselves, inside a sync.Once, on
// first use, before this migration runner existed. Because both statements
// are `CREATE TABLE IF NOT EXISTS`, running them against a database that
// already has the tables (created by that old lazy-init path) is a clean
// no-op — no data conversion needed, unlike the CloudWatch Logs blob→row
// migration. The only effect on such a database is that user_version
// advances past these versions, so the runner stops re-checking them on
// every future startup.
const (
	migrationDynamoDBItemsTableVersion   = 20
	migrationDynamoDBStreamsTableVersion = 21
)

func init() {
	state.RegisterMigration(state.Migration{
		Version: migrationDynamoDBItemsTableVersion,
		Name:    "create dynamodb_items table",
		Up: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `
				CREATE TABLE IF NOT EXISTS dynamodb_items (
					table_name  TEXT NOT NULL,
					hash_key    TEXT NOT NULL,
					sort_key    TEXT NOT NULL DEFAULT '',
					item_json   TEXT NOT NULL,
					PRIMARY KEY (table_name, hash_key, sort_key)
				)
			`); err != nil {
				return fmt.Errorf("create dynamodb_items table: %w", err)
			}
			return nil
		},
	})

	state.RegisterMigration(state.Migration{
		Version: migrationDynamoDBStreamsTableVersion,
		Name:    "create dynamodb_stream_records table",
		Up: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, createStreamRecordsTable); err != nil {
				return fmt.Errorf("create dynamodb_stream_records table: %w", err)
			}
			return nil
		},
	})
}
