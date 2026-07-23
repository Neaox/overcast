//go:build !nosqlite

// Package logs: migration registration for the logs_events dedicated table
// (docs/storage-plan.md Phase 2 item 2.3).
//
// Build-tagged out of `-tags nosqlite` builds because it depends on
// state.Migration / state.RegisterMigration, which are themselves only
// defined for !nosqlite builds (compare internal/state/migrate.go against
// internal/state/sqlite_hybrid_nosqlite.go). This is safe: under
// -tags nosqlite, NewHybridStore and NewSQLiteStore both unconditionally
// return an error and cmd/overcast/cmd_serve.go's buildStore falls back to
// state.NewMemoryStore, so no real store ever satisfies
// state.SQLiteDBProvider at runtime in a nosqlite build — newEventBackendFor
// (event_backend.go, not build-tagged) always selects memEventBackend there
// regardless of whether this migration ever registered. DynamoDB's item/
// stream backends rely on the identical reasoning (see
// internal/services/dynamodb/item_store.go).
package logs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// Migration versions 10-19 are reserved for the CloudWatch Logs events table
// — see internal/state/migrate.go's Migration doc comment for the
// reserved-range convention.
//
// Two migrations, not one, deliberately:
//   - migrationLogsEventsTableVersion creates the schema. Needed
//     unconditionally, even for a brand-new installation with no legacy data.
//   - migrationLogsEventsConversionVersion does the one-time blob→row data
//     conversion. A no-op after the first successful run (nothing left in
//     the legacy logs:events kv namespace to convert), and conceptually a
//     distinct concern — a fresh database has nothing to convert, so
//     separating it keeps the "add the table" step trivially idempotent and
//     independent of whether any legacy data exists.
const (
	migrationLogsEventsTableVersion      = 10
	migrationLogsEventsConversionVersion = 11
)

func init() {
	state.RegisterMigration(state.Migration{
		Version: migrationLogsEventsTableVersion,
		Name:    "create logs_events table",
		Up: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `
				CREATE TABLE IF NOT EXISTS logs_events (
					region       TEXT    NOT NULL,
					group_name   TEXT    NOT NULL,
					stream_name  TEXT    NOT NULL,
					ts           INTEGER NOT NULL,
					seq          INTEGER NOT NULL,
					ingestion_ts INTEGER NOT NULL,
					message      TEXT    NOT NULL,
					PRIMARY KEY (region, group_name, stream_name, ts, seq)
				)
			`); err != nil {
				return fmt.Errorf("create logs_events table: %w", err)
			}
			// Supports a group-wide (region, group_name) range scan ordered
			// by ts, without a specific stream — FilterLogEvents currently
			// loops per-stream (see handler.go), but this index means a
			// future group-wide query path (storage-plan.md 3.1/3.13-style
			// follow-up) doesn't need a schema change to be efficient.
			if _, err := tx.ExecContext(ctx, `
				CREATE INDEX IF NOT EXISTS idx_logs_events_group
				ON logs_events (region, group_name, ts)
			`); err != nil {
				return fmt.Errorf("create idx_logs_events_group: %w", err)
			}
			return nil
		},
	})

	state.RegisterMigration(state.Migration{
		Version: migrationLogsEventsConversionVersion,
		Name:    "convert logs:events blobs to logs_events rows",
		Up:      migrateLogsEventBlobsToRows,
	})
}

// storedEvents is the legacy on-disk shape of a logs:events kv blob (one per
// stream, holding that stream's entire event history as of the old
// one-blob-per-stream design). Migration-only now that event storage lives
// in the dedicated logs_events table (see event_backend.go) — kept here
// solely so migrateLogsEventBlobsToRows can decode old data during the
// one-time conversion.
type storedEvents struct {
	Events []LogEvent `json:"events"`
}

// migrateLogsEventBlobsToRows converts every legacy logs:events kv blob into
// individual logs_events rows, then deletes the migrated kv rows. Runs
// inside the migration runner's own transaction (internal/state/migrate.go),
// which is why it has direct SQL access to the generic kv table via tx —
// this makes the conversion fully self-contained as a one-time step, unlike
// the CloudFormation cfn:events fix in Phase 1 (storage-plan.md 1.9), whose
// data stayed in the generic kv namespace and so used an inline runtime
// fallback instead of a migration. Here the data is moving to a dedicated
// table, so a one-time migration is the cleaner fit: store.go/
// event_backend.go carry no "check the old namespace too" logic in the hot
// read path.
//
// Per CLAUDE.md's malformed-persisted-state rule, a blob that fails to
// JSON-decode is logged and skipped, not treated as a fatal migration error.
// The same applies to an events key with no corresponding logs:streams
// record — needed to unambiguously split the key into group/stream (see
// below) — which is logged and skipped as an orphan rather than guessed at.
func migrateLogsEventBlobsToRows(ctx context.Context, tx *sql.Tx) error {
	metaByKey, err := loadLogsStreamMeta(ctx, tx)
	if err != nil {
		return err
	}

	eventRows, err := tx.QueryContext(ctx, `SELECT key, value FROM kv WHERE namespace = ?`, nsEvents)
	if err != nil {
		return fmt.Errorf("logs events migration: query %s: %w", nsEvents, err)
	}
	type pendingBlob struct {
		key    string
		meta   streamMeta
		events []LogEvent
	}
	var toMigrate []pendingBlob
	var malformedDecode, noStreamMatch int
	for eventRows.Next() {
		var key, value string
		if err := eventRows.Scan(&key, &value); err != nil {
			eventRows.Close()
			return fmt.Errorf("logs events migration: scan %s row: %w", nsEvents, err)
		}
		meta, ok := metaByKey[key]
		if !ok {
			noStreamMatch++
			fmt.Fprintf(os.Stderr, "overcast: logs events migration: skipping orphaned %s blob %q (no matching %s record)\n", nsEvents, key, nsStreams)
			continue
		}
		var se storedEvents
		if err := json.Unmarshal([]byte(value), &se); err != nil {
			malformedDecode++
			fmt.Fprintf(os.Stderr, "overcast: logs events migration: skipping undecodable %s blob %q: %v\n", nsEvents, key, err)
			continue
		}
		toMigrate = append(toMigrate, pendingBlob{key: key, meta: meta, events: se.Events})
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return fmt.Errorf("logs events migration: %s rows: %w", nsEvents, err)
	}
	eventRows.Close()

	skipped := malformedDecode + noStreamMatch
	if len(toMigrate) == 0 {
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "overcast: logs events migration: 0 events migrated; skipped %d blob(s) (%d malformed, %d orphaned)\n",
				skipped, malformedDecode, noStreamMatch)
		}
		return nil
	}

	insertStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO logs_events (region, group_name, stream_name, ts, seq, ingestion_ts, message)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("logs events migration: prepare insert: %w", err)
	}
	defer insertStmt.Close()

	deleteStmt, err := tx.PrepareContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key = ?`)
	if err != nil {
		return fmt.Errorf("logs events migration: prepare delete: %w", err)
	}
	defer deleteStmt.Close()

	var migratedEvents int
	for _, p := range toMigrate {
		// seq = the event's index within its (already timestamp-sorted, per
		// the old design's invariant — see the previous store.go's
		// eventCache doc comment) blob, so insertion order and therefore
		// ORDER BY ts, seq is preserved exactly.
		for seq, e := range p.events {
			if _, err := insertStmt.ExecContext(ctx, p.meta.region, p.meta.group, p.meta.stream, e.Timestamp, int64(seq), e.IngestionTime, e.Message); err != nil {
				return fmt.Errorf("logs events migration: insert event [%s]: %w", p.key, err)
			}
		}
		if _, err := deleteStmt.ExecContext(ctx, nsEvents, p.key); err != nil {
			return fmt.Errorf("logs events migration: delete migrated blob [%s]: %w", p.key, err)
		}
		migratedEvents += len(p.events)
	}

	fmt.Fprintf(os.Stderr, "overcast: logs events migration: migrated %d event(s) across %d stream(s); skipped %d blob(s) (%d malformed, %d orphaned)\n",
		migratedEvents, len(toMigrate), skipped, malformedDecode, noStreamMatch)
	return nil
}

// streamMeta is the (region, group, stream) triple recovered for one
// logs:streams (equivalently logs:events) kv key.
type streamMeta struct {
	region, group, stream string
}

// loadLogsStreamMeta builds a lookup from raw logs:streams kv key to its
// decoded (region, group, stream) triple.
//
// logs:streams and logs:events keys are both
// "<region>/<groupName>/<streamName>" (see eventsKey/streamKey in store.go)
// — ambiguous to split blindly on "/" since group and stream names may
// themselves contain "/" (e.g. group "/aws/lambda/my-fn"). The logs:streams
// record's JSON value carries the stream's exact name (LogStream.Name), so
// stripping "/<streamName>" off the remainder after the region prefix
// unambiguously recovers the group name, whatever it contains.
func loadLogsStreamMeta(ctx context.Context, tx *sql.Tx) (map[string]streamMeta, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key, value FROM kv WHERE namespace = ?`, nsStreams)
	if err != nil {
		return nil, fmt.Errorf("logs events migration: query %s: %w", nsStreams, err)
	}
	defer rows.Close()

	meta := make(map[string]streamMeta)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("logs events migration: scan %s row: %w", nsStreams, err)
		}
		var ls LogStream
		if err := json.Unmarshal([]byte(value), &ls); err != nil || ls.Name == "" {
			fmt.Fprintf(os.Stderr, "overcast: logs events migration: skipping undecodable %s record %q: %v\n", nsStreams, key, err)
			continue
		}
		region, rest := serviceutil.SplitRegionKey(key)
		suffix := "/" + ls.Name
		if !strings.HasSuffix(rest, suffix) {
			fmt.Fprintf(os.Stderr, "overcast: logs events migration: %s record %q does not end in its own stream name %q, skipping\n", nsStreams, key, ls.Name)
			continue
		}
		group := strings.TrimSuffix(rest, suffix)
		meta[key] = streamMeta{region: region, group: group, stream: ls.Name}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("logs events migration: %s rows: %w", nsStreams, err)
	}
	return meta, nil
}
