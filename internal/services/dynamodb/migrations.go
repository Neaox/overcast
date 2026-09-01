//go:build !nosqlite

// Package dynamodb: migration registration for the dynamodb_items and
// dynamodb_stream_records dedicated tables (docs/plans/storage-plan.md Phase 3
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
	"encoding/json"
	"fmt"

	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
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
//
// migrationDynamoDBReencodeNumericKeysVersion (22) is a data migration, not
// pure schema setup: it re-encodes existing dynamodb_items rows for tables
// whose declared hash or sort key is Number-typed, using
// encodeOrderableNumber (numeric_encoding.go) — see that migration's own doc
// comment below for why and how.
//
// NOTE on version 22: it was originally proposed for the GSI index-entries
// table (docs/plans/dynamodb-gsi-design.md §3/§7, "dynamodb_index_entries...
// migration: version 22") — that document was written before this
// prerequisite migration's landing order was fixed. This migration lands
// first and claims 22; the GSI index-entries table (phase 2) takes the next
// free slot, 23 (migrationDynamoDBIndexEntriesTableVersion below).
//
// migrationDynamoDBIndexEntriesTableVersion (23) is dynamodb-gsi-design.md
// §3's index-structure landing: creates dynamodb_index_entries (Option A)
// and backfills it for every GSI already declared on an existing table's
// schema — see migrateCreateIndexEntriesTable's own doc comment.
//
// migrationDynamoDBRegionQualifyKeysVersion (24) is issue #673's data
// migration: it rewrites table_name in all three dedicated tables from a bare
// table name to the region-qualified key dynamoStore.tableKey now mints — see
// migrateRegionQualifyTableKeys's own doc comment.
const (
	migrationDynamoDBItemsTableVersion          = 20
	migrationDynamoDBStreamsTableVersion        = 21
	migrationDynamoDBReencodeNumericKeysVersion = 22
	migrationDynamoDBIndexEntriesTableVersion   = 23
	migrationDynamoDBRegionQualifyKeysVersion   = 24
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

	state.RegisterMigration(state.Migration{
		Version: migrationDynamoDBReencodeNumericKeysVersion,
		Name:    "re-encode Number-typed base-table keys for numeric order",
		Up:      migrateReencodeNumericKeys,
	})

	state.RegisterMigration(state.Migration{
		Version: migrationDynamoDBIndexEntriesTableVersion,
		Name:    "create dynamodb_index_entries table + backfill existing GSIs",
		Up:      migrateCreateIndexEntriesTable,
	})

	state.RegisterMigration(state.Migration{
		Version: migrationDynamoDBRegionQualifyKeysVersion,
		Name:    "region-qualify dynamodb item, index and stream table keys",
		Up:      migrateRegionQualifyTableKeys,
	})
}

// migrateRegionQualifyTableKeys is issue #673's on-disk half.
//
// Before it, dynamodb_items.table_name, dynamodb_index_entries.table_name and
// dynamodb_stream_records.table_name all held a bare DynamoDB table name,
// while the table *descriptor* in kv (namespace dynamodb:tables) was already
// keyed "<region>/<name>". dynamoStore.tableKey now qualifies all four the same
// way, so every pre-existing row has to be rewritten or it becomes unreachable.
//
// The region for each row comes from the descriptor's own kv key — the table
// record is the only thing on disk that ever knew which region a table was
// created in, and reading it inside this same transaction makes the region
// lookup and the rewrite atomic.
//
// Three cases, all deliberate:
//
//   - The ordinary case — exactly one region declares the table name. Its rows
//     are rewritten to "<region>/<name>" and stay exactly as reachable as they
//     were, from that region.
//   - The name exists in more than one region. Before this change those regions
//     shared one pile of rows — that is the bug — and nothing on disk records
//     which write came from where, so it cannot be split faithfully. Copying to
//     both would preserve today's cross-region visibility, which is the
//     behaviour being removed. The rows are assigned to the lexicographically
//     first region, deterministically, and the other regions start empty. This
//     is called out as a breaking change in the changelog fragment.
//   - Rows whose table name matches no descriptor at all (an orphan from a
//     partial delete, or a table dropped out of band). They are left untouched
//     with their bare key: guessing a region would invent data, and deleting
//     them would lose it. They are unreachable but inert — no list, scan or
//     lookup can produce them, because every read now goes through a qualified
//     key, which is exactly the isolation AGENTS.md § "Malformed persisted
//     state must be isolated" asks for.
//
// Each UPDATE matches the *bare* name exactly, so an already-qualified row is
// never touched twice: AWS restricts table names to [a-zA-Z0-9_.-], so a "/"
// in table_name can only be a region prefix this migration (or a post-#673
// write) already applied. That makes the migration safe to re-run and safe
// against a database written by a mix of versions.
//
// The memory backend needs no equivalent: memItemBackend/memStreamBackend only
// exist for state.MemoryStore, which has no persistence to migrate (same
// reasoning as migrateReencodeNumericKeys).
func migrateRegionQualifyTableKeys(ctx context.Context, tx *sql.Tx) error {
	regionByTable, err := tableRegionsFromDescriptors(ctx, tx)
	if err != nil {
		return err
	}
	if len(regionByTable) == 0 {
		return nil // no tables declared — nothing to qualify
	}

	for name, region := range regionByTable {
		qualified := region + "/" + name
		for _, table := range []string{"dynamodb_items", "dynamodb_index_entries", "dynamodb_stream_records"} {
			// OR IGNORE, because dynamodb_items and dynamodb_index_entries key
			// on table_name as part of their primary key. A database that
			// somehow holds both a bare and an already-qualified row for the
			// same item would otherwise abort this migration — and with it
			// startup — on a PRIMARY KEY conflict. Keeping the qualified row
			// and leaving the stale bare one as an inert orphan is the same
			// call the no-descriptor case makes, and it is strictly better
			// than refusing to boot. (dynamodb_stream_records has no such
			// constraint, so OR IGNORE is a no-op there.)
			if _, err := tx.ExecContext(ctx,
				`UPDATE OR IGNORE `+table+` SET table_name = ? WHERE table_name = ?`,
				qualified, name,
			); err != nil {
				return fmt.Errorf("region-qualify %s rows for table %q: %w", table, name, err)
			}
		}
	}
	return nil
}

// tableRegionsFromDescriptors reads the dynamodb:tables kv namespace and
// returns, for each bare table name, the region its rows should be assigned
// to — the lexicographically first region declaring that name, so a database
// where the same name exists in several regions migrates the same way every
// time. Keys that carry no region prefix are ignored: there is nothing to
// learn from them.
func tableRegionsFromDescriptors(ctx context.Context, tx *sql.Tx) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key FROM kv WHERE namespace = ?`, nsTables)
	if err != nil {
		return nil, fmt.Errorf("read dynamodb table keys: %w", err)
	}
	defer rows.Close()

	regionByTable := make(map[string]string)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan dynamodb table key: %w", err)
		}
		region, name := serviceutil.SplitRegionKey(key)
		if region == "" || name == "" {
			continue
		}
		if existing, ok := regionByTable[name]; !ok || region < existing {
			regionByTable[name] = region
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dynamodb table keys: %w", err)
	}
	return regionByTable, nil
}

// migrateReencodeNumericKeys is dynamodb-gsi-design.md §2/§7 phase 1's
// storage-layer retrofit, applied to data already on disk: before this
// migration, dynamodb_items.hash_key/sort_key stored a Number-typed key
// attribute as its raw decimal text, which sorts lexicographically ("10" <
// "5") instead of numerically. putItem/getItem/deleteItem/scanItemsPage now
// write and look up encodeOrderableNumber-encoded keys instead (store.go's
// resolveStorageKeys) — this migration brings pre-existing rows in line so
// they're reachable and correctly ordered under the new scheme, exactly
// once, at startup.
//
// Table schemas live in the kv table (namespace dynamodb:tables, JSON
// Table records) — read here inside the same migration transaction so the
// re-encoding decision and the row rewrite are atomic with the schema read.
// A row whose owning table JSON fails to decode is skipped (CLAUDE.md's
// isolation rule: one malformed table record must not fail the whole
// migration) — its dynamodb_items rows are simply left unencoded, same as
// they were before this migration (a correctness gap only for that one
// broken table, not a new failure mode).
//
// No-op for the overwhelmingly common case: a table whose hash and sort key
// are both String/Binary is skipped entirely — its dynamodb_items rows are
// never even read, let alone rewritten.
//
// The memory backend needs no equivalent migration: memItemBackend only
// exists for a process using state.MemoryStore (see
// newItemBackendFor/newStreamBackendFor in service.go), which has no
// on-disk persistence at all — there is nothing to re-encode because there
// is nothing surviving a restart to begin with. Migrations only ever run
// against a real SQLite file (state.RunMigrations is wired into
// SQLite/Hybrid store startup), so this function only needs to handle the
// SQL rows.
func migrateReencodeNumericKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT value FROM kv WHERE namespace = ?`, nsTables)
	if err != nil {
		return fmt.Errorf("read dynamodb table schemas: %w", err)
	}
	var schemas []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("scan dynamodb table schema row: %w", err)
		}
		schemas = append(schemas, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate dynamodb table schemas: %w", err)
	}
	rows.Close()

	// De-duplicate by TableName. At this migration version item storage is
	// still keyed by the bare Table.TableName — region qualification does not
	// arrive until version 24 (migrateRegionQualifyTableKeys) — so if the same
	// table name appears in more than one region's schema record, processing it
	// again would just repeat the same (idempotent) rewrite. Skip repeats
	// rather than doing pointless work.
	seen := make(map[string]bool, len(schemas))

	for _, raw := range schemas {
		var t Table
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			continue // malformed table record — isolate, skip (CLAUDE.md isolation rule)
		}
		if t.TableName == "" || seen[t.TableName] {
			continue
		}
		seen[t.TableName] = true

		hashName := t.hashKeyName()
		sortName := t.sortKeyName()
		hashIsN := keyAttrType(&t, hashName) == "N"
		sortIsN := sortName != "" && keyAttrType(&t, sortName) == "N"
		if !hashIsN && !sortIsN {
			continue // pure String/Binary keyed table — no-op, the common case
		}

		if err := reencodeTableItemKeys(ctx, tx, t.TableName, hashIsN, sortIsN); err != nil {
			return fmt.Errorf("re-encode numeric keys for table %q: %w", t.TableName, err)
		}
	}
	return nil
}

// reencodeTableItemKeys rewrites every dynamodb_items row for tableName,
// re-encoding whichever of hash_key/sort_key the table declares as
// Number-typed. Uses rowid (implicit on this non-WITHOUT-ROWID table) to
// target each row precisely: a delete-then-insert-or-replace per row rather
// than an in-place UPDATE, so that if two pre-existing rows held different
// textual representations of the same numeric value (e.g. "5" and "05" —
// already an inconsistency real AWS would never have allowed, since it
// treats them as the same key), the encoding collision resolves via
// last-write-wins (whichever row this loop reaches second) instead of a
// PRIMARY KEY constraint failure aborting the whole migration.
func reencodeTableItemKeys(ctx context.Context, tx *sql.Tx, tableName string, hashIsN, sortIsN bool) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT rowid, hash_key, sort_key, item_json FROM dynamodb_items WHERE table_name = ?
	`, tableName)
	if err != nil {
		return fmt.Errorf("select rows: %w", err)
	}
	type itemRow struct {
		rowid            int64
		hashKey, sortKey string
		itemJSON         string
	}
	var toProcess []itemRow
	for rows.Next() {
		var r itemRow
		if err := rows.Scan(&r.rowid, &r.hashKey, &r.sortKey, &r.itemJSON); err != nil {
			rows.Close()
			return fmt.Errorf("scan row: %w", err)
		}
		toProcess = append(toProcess, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate rows: %w", err)
	}
	rows.Close()

	for _, r := range toProcess {
		newHash, newSort := r.hashKey, r.sortKey
		if hashIsN {
			// A malformed/non-numeric persisted hash_key (shouldn't happen for
			// a well-formed table, but CLAUDE.md's isolation rule applies to
			// migrations too) is left as-is rather than aborting the migration.
			if enc, encErr := encodeOrderableNumber(r.hashKey); encErr == nil {
				newHash = enc
			}
		}
		if sortIsN {
			if enc, encErr := encodeOrderableNumber(r.sortKey); encErr == nil {
				newSort = enc
			}
		}
		if newHash == r.hashKey && newSort == r.sortKey {
			continue // already canonical (or left alone above) — skip a no-op write
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM dynamodb_items WHERE rowid = ?`, r.rowid); err != nil {
			return fmt.Errorf("delete pre-encoding row (rowid %d): %w", r.rowid, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO dynamodb_items (table_name, hash_key, sort_key, item_json)
			VALUES (?, ?, ?, ?)
		`, tableName, newHash, newSort, r.itemJSON); err != nil {
			return fmt.Errorf("insert re-encoded row (rowid %d): %w", r.rowid, err)
		}
	}
	return nil
}

// migrateCreateIndexEntriesTable is dynamodb-gsi-design.md section-3's index-
// structure landing (phase 2): creates the dynamodb_index_entries table
// (Option A -- see the design doc's schema-choice discussion) and backfills
// it for every GSI already declared on an existing table's schema.
//
// Backfill matters here specifically because a table can have GSIs recorded
// in its schema (via CreateTable's GlobalSecondaryIndexes, or a prior
// UpdateTable Create) from *before* this migration -- and therefore before
// any index storage existed at all -- with items already written against
// it. Without this step those GSIs would silently start out with an empty
// index in the new structure despite having items that satisfy the index's
// key, which is exactly the kind of divergence CLAUDE.md's isolation rule
// and this design's own backfill section (section 3, "UpdateTable adding a
// GSI... does Overcast support this today?") both call out as unacceptable.
// This migration reuses the exact same helpers (resolveStorageKeys,
// indexKeyComponents, projectForIndex) as the UpdateTable-Create backfill
// path (handler.go's backfillIndex) and the write-path diff
// (diffIndexMutations), so a table migrated at startup and a table that
// gets a GSI added at runtime land byte-identical index rows for the same
// data -- one code path for "compute what a GSI's index rows should be for
// this item," landed once, reused by both backfill triggers.
//
// Table schemas and item rows are both read here, inside the same migration
// transaction, so schema discovery and the backfill itself are atomic with
// each other and with the table-creation DDL above. A table record that
// fails to decode, or an item row whose JSON fails to decode, is skipped --
// CLAUDE.md's isolation rule: one malformed record must not fail the whole
// migration, or leave every other table's GSIs unbackfilled because of one
// unrelated table's bad data.
//
// No-op for the common case: a table with no GlobalSecondaryIndexes is
// skipped entirely -- its dynamodb_items rows are never even read.
func migrateCreateIndexEntriesTable(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS dynamodb_index_entries (
			table_name   TEXT NOT NULL,
			index_name   TEXT NOT NULL,
			index_hash   TEXT NOT NULL,
			index_sort   TEXT NOT NULL DEFAULT '',
			base_hash    TEXT NOT NULL,
			base_sort    TEXT NOT NULL DEFAULT '',
			item_json    TEXT NOT NULL,
			PRIMARY KEY (table_name, index_name, index_hash, index_sort, base_hash, base_sort)
		)
	`); err != nil {
		return fmt.Errorf("create dynamodb_index_entries table: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT value FROM kv WHERE namespace = ?`, nsTables)
	if err != nil {
		return fmt.Errorf("read dynamodb table schemas: %w", err)
	}
	var schemas []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("scan dynamodb table schema row: %w", err)
		}
		schemas = append(schemas, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate dynamodb table schemas: %w", err)
	}
	rows.Close()

	// De-duplicate by TableName for the same reason migrateReencodeNumericKeys
	// does: at this version item storage is still keyed by the bare
	// Table.TableName, not by region (see version 24).
	seen := make(map[string]bool, len(schemas))
	for _, raw := range schemas {
		var t Table
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			continue // malformed table record -- isolate, skip (CLAUDE.md isolation rule)
		}
		if t.TableName == "" || seen[t.TableName] {
			continue
		}
		seen[t.TableName] = true
		if len(t.GlobalSecondaryIndexes) == 0 {
			continue // nothing to backfill -- the common case
		}
		if err := backfillIndexEntriesForTable(ctx, tx, &t); err != nil {
			return fmt.Errorf("backfill GSI index entries for table %q: %w", t.TableName, err)
		}
	}
	return nil
}

// backfillIndexEntriesForTable scans every item already stored for t and
// inserts the corresponding dynamodb_index_entries row for each of t's GSIs
// that the item's attributes satisfy -- the sparse-index write rule
// (dynamodb-gsi-design.md section 3): an item missing the GSI's key
// attribute(s) gets no row, exactly like a live write would produce via
// diffIndexMutations.
func backfillIndexEntriesForTable(ctx context.Context, tx *sql.Tx, t *Table) error {
	rows, err := tx.QueryContext(ctx,
		`SELECT item_json FROM dynamodb_items WHERE table_name = ?`, t.TableName,
	)
	if err != nil {
		return fmt.Errorf("select items: %w", err)
	}
	var itemsJSON []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return fmt.Errorf("scan item row: %w", err)
		}
		itemsJSON = append(itemsJSON, raw)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate items: %w", err)
	}
	rows.Close()

	for _, raw := range itemsJSON {
		var item Item
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			continue // malformed item row -- isolate, skip (CLAUDE.md isolation rule)
		}
		baseHash, baseSort, aerr := resolveStorageKeys(t, item)
		if aerr != nil {
			continue // item missing/malformed key attributes -- isolate, skip
		}
		for i := range t.GlobalSecondaryIndexes {
			idx := &t.GlobalSecondaryIndexes[i]
			indexHash, indexSort, ok := indexKeyComponents(t, idx, item)
			if !ok {
				continue // sparse -- item doesn't satisfy this GSI's key
			}
			projected := projectForIndex(t, idx, item)
			projRaw, err := json.Marshal(projected)
			if err != nil {
				continue // shouldn't happen for a value already round-tripped through JSON -- isolate, skip
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT OR REPLACE INTO dynamodb_index_entries
					(table_name, index_name, index_hash, index_sort, base_hash, base_sort, item_json)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`, t.TableName, idx.IndexName, indexHash, indexSort, baseHash, baseSort, string(projRaw)); err != nil {
				return fmt.Errorf("insert index entry [%s/%s]: %w", t.TableName, idx.IndexName, err)
			}
		}
	}
	return nil
}
