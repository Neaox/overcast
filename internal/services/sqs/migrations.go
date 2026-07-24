//go:build !nosqlite

// Package sqs: migration registration for the sqs_messages dedicated table
// (docs/plans/storage-plan.md item 3.10).
//
// Build-tagged out of `-tags nosqlite` builds because it depends on
// state.Migration / state.RegisterMigration, which are themselves only
// defined for !nosqlite builds (compare internal/state/migrate.go against
// internal/state/sqlite_hybrid_nosqlite.go). This is safe: under
// -tags nosqlite, NewHybridStore and NewSQLiteStore both unconditionally
// return an error and cmd/overcast/cmd_serve.go's buildStore falls back to
// state.NewMemoryStore, so no real store ever satisfies
// state.SQLiteDBProvider at runtime in a nosqlite build — newMessageBackendFor
// (message_backend.go, not build-tagged) always selects memMessageBackend
// there regardless of whether this migration ever registered. CloudWatch
// Logs' event backend and DynamoDB's item/stream backends rely on the
// identical reasoning (see internal/services/cloudwatch/logs/migrations.go
// and internal/services/dynamodb/migrations.go).
package sqs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Neaox/overcast/internal/state"
)

// Migration versions 30-39 are reserved for the SQS messages table — see
// internal/state/migrate.go's Migration doc comment for the reserved-range
// convention.
//
// Two migrations, not one, mirroring the CloudWatch Logs precedent
// (internal/services/cloudwatch/logs/migrations.go):
//   - migrationSQSMessagesTableVersion creates the schema. Needed
//     unconditionally, even for a brand-new installation with no legacy data.
//   - migrationSQSMessagesConversionVersion does the one-time blob→row data
//     conversion. A no-op after the first successful run (nothing left in
//     the legacy sqs:messages kv namespace to convert).
const (
	migrationSQSMessagesTableVersion      = 30
	migrationSQSMessagesConversionVersion = 31
)

func init() {
	state.RegisterMigration(state.Migration{
		Version: migrationSQSMessagesTableVersion,
		Name:    "create sqs_messages table",
		Up: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `
				CREATE TABLE IF NOT EXISTS sqs_messages (
					region             TEXT    NOT NULL,
					queue_name         TEXT    NOT NULL,
					message_id         TEXT    NOT NULL,
					visible_at         INTEGER NOT NULL,
					message_group_id   TEXT    NOT NULL DEFAULT '',
					sequence_number    INTEGER NOT NULL DEFAULT 0,
					message_json       TEXT    NOT NULL,
					PRIMARY KEY (region, queue_name, message_id)
				)
			`); err != nil {
				return fmt.Errorf("create sqs_messages table: %w", err)
			}
			// The crucial index storage-plan.md 3.10 calls for: the receive
			// path (ReceiveMessage's candidate fetch) filters and orders by
			// exactly this triple. message_group_id/sequence_number are not
			// part of the index — FIFO's blockedGroups query filters on
			// (region, queue_name, visible_at) too (invisible = "blocked
			// candidate"), so this one index serves both the visible-message
			// scan and the blocked-group lookup; sequence_number ordering
			// within the matched row set is cheap at emulator scale (see
			// message_backend.go's sqlMessageBackend.receiveCandidates).
			if _, err := tx.ExecContext(ctx, `
				CREATE INDEX IF NOT EXISTS idx_sqs_messages_visible
				ON sqs_messages (region, queue_name, visible_at)
			`); err != nil {
				return fmt.Errorf("create idx_sqs_messages_visible: %w", err)
			}
			return nil
		},
	})

	state.RegisterMigration(state.Migration{
		Version: migrationSQSMessagesConversionVersion,
		Name:    "convert sqs:messages blobs to sqs_messages rows",
		Up:      migrateSQSMessageBlobsToRows,
	})
}

// migrateSQSMessageBlobsToRows converts every legacy sqs:messages kv blob
// into an sqs_messages row, then deletes the migrated kv rows. Runs inside
// the migration runner's own transaction (internal/state/migrate.go), same
// shape as the CloudWatch Logs blob→row conversion
// (internal/services/cloudwatch/logs/migrations.go's
// migrateLogsEventBlobsToRows) — see that function's doc comment for why a
// one-time migration (rather than an inline runtime fallback, as
// CloudFormation's 1.9 fix used) is the right shape when data is moving to a
// dedicated table.
//
// Unlike CloudWatch Logs, SQS message keys need no cross-referencing to
// unambiguously split apart: messageKey/serviceutil.RegionKey produce
// "<region>/<queueName>/<messageID>", and SQS queue names are restricted by
// AWS to alphanumerics/hyphens/underscores (no "/"), so a plain
// strings.SplitN(key, "/", 3) always recovers the correct triple.
//
// Per CLAUDE.md's malformed-persisted-state rule, a blob that fails to
// JSON-decode, or a key that doesn't split into exactly 3 parts, is logged
// and skipped rather than treated as a fatal migration error.
func migrateSQSMessageBlobsToRows(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT key, value FROM kv WHERE namespace = ?`, nsMessages)
	if err != nil {
		return fmt.Errorf("sqs messages migration: query %s: %w", nsMessages, err)
	}

	type pendingRow struct {
		key    string
		region string
		queue  string
		msg    Message
	}
	var toMigrate []pendingRow
	var malformedDecode, badKey int
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return fmt.Errorf("sqs messages migration: scan %s row: %w", nsMessages, err)
		}
		parts := strings.SplitN(key, "/", 3)
		if len(parts) != 3 {
			badKey++
			fmt.Fprintf(os.Stderr, "overcast: sqs messages migration: skipping malformed-key %s row %q (expected region/queue/messageID)\n", nsMessages, key)
			continue
		}
		region, queueName, messageID := parts[0], parts[1], parts[2]
		var msg Message
		if err := json.Unmarshal([]byte(value), &msg); err != nil {
			malformedDecode++
			fmt.Fprintf(os.Stderr, "overcast: sqs messages migration: skipping undecodable %s blob %q: %v\n", nsMessages, key, err)
			continue
		}
		if msg.MessageID == "" {
			msg.MessageID = messageID
		}
		toMigrate = append(toMigrate, pendingRow{key: key, region: region, queue: queueName, msg: msg})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("sqs messages migration: %s rows: %w", nsMessages, err)
	}
	rows.Close()

	skipped := malformedDecode + badKey
	if len(toMigrate) == 0 {
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "overcast: sqs messages migration: 0 messages migrated; skipped %d row(s) (%d malformed, %d bad key)\n",
				skipped, malformedDecode, badKey)
		}
		return nil
	}

	insertStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO sqs_messages (region, queue_name, message_id, visible_at, message_group_id, sequence_number, message_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (region, queue_name, message_id) DO UPDATE SET
			visible_at = excluded.visible_at,
			message_group_id = excluded.message_group_id,
			sequence_number = excluded.sequence_number,
			message_json = excluded.message_json
	`)
	if err != nil {
		return fmt.Errorf("sqs messages migration: prepare insert: %w", err)
	}
	defer insertStmt.Close()

	deleteStmt, err := tx.PrepareContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key = ?`)
	if err != nil {
		return fmt.Errorf("sqs messages migration: prepare delete: %w", err)
	}
	defer deleteStmt.Close()

	var migrated int
	for _, p := range toMigrate {
		raw, err := json.Marshal(&p.msg)
		if err != nil {
			return fmt.Errorf("sqs messages migration: remarshal [%s]: %w", p.key, err)
		}
		seqNum, _ := strconv.ParseInt(p.msg.SequenceNumber, 10, 64) // 0 for non-FIFO or unparsable
		visibleAt := p.msg.VisibleAfter.UnixMilli()
		if _, err := insertStmt.ExecContext(ctx, p.region, p.queue, p.msg.MessageID, visibleAt, p.msg.MessageGroupId, seqNum, string(raw)); err != nil {
			return fmt.Errorf("sqs messages migration: insert [%s]: %w", p.key, err)
		}
		if _, err := deleteStmt.ExecContext(ctx, nsMessages, p.key); err != nil {
			return fmt.Errorf("sqs messages migration: delete migrated blob [%s]: %w", p.key, err)
		}
		migrated++
	}

	fmt.Fprintf(os.Stderr, "overcast: sqs messages migration: migrated %d message(s); skipped %d row(s) (%d malformed, %d bad key)\n",
		migrated, skipped, malformedDecode, badKey)
	return nil
}
