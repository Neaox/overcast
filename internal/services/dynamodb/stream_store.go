package dynamodb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// StreamRecord is a single DynamoDB Streams change record.
// It is exported so the dynamodbstreams service can use it directly.
type StreamRecord struct {
	// SequenceNumber is a monotonic integer used as the primary ordering key.
	// Exported as a zero-padded string in API responses.
	SequenceNumber int64  `json:"seq"`
	EventName      string `json:"eventName"` // INSERT, MODIFY, REMOVE
	Keys           Item   `json:"Keys"`
	NewImage       Item   `json:"NewImage,omitempty"`
	OldImage       Item   `json:"OldImage,omitempty"`
	CreatedAt      int64  `json:"createdAt"` // UnixMilli
}

// streamBackend is the narrow interface for stream record storage.
// Implementations: memStreamBackend (tests / default), sqlStreamBackend (SQLite).
type streamBackend interface {
	// append stores a new stream record for the given table.
	// The backend assigns a unique monotonically-increasing SequenceNumber.
	append(ctx context.Context, tableName string, r *StreamRecord) error

	// since returns records for the table with SequenceNumber > afterSeq,
	// ordered ascending, capped at limit. limit ≤ 0 means no cap.
	since(ctx context.Context, tableName string, afterSeq int64, limit int) ([]*StreamRecord, error)

	// latest returns the highest SequenceNumber stored for the table,
	// or 0 if there are none.
	latest(ctx context.Context, tableName string) (int64, error)
}

// ─── Memory implementation ────────────────────────────────────────────────────

// globalSeq is a process-wide monotonic counter shared across all memory stream
// backends so records from different tables are globally orderable.
var globalSeq atomic.Int64

// memStreamBackend is an in-memory stream record store.
type memStreamBackend struct {
	mu      sync.RWMutex
	records map[string][]*StreamRecord // tableName → ordered slice
}

func newMemStreamBackend() *memStreamBackend {
	return &memStreamBackend{records: make(map[string][]*StreamRecord)}
}

func (b *memStreamBackend) append(_ context.Context, tableName string, r *StreamRecord) error {
	r.SequenceNumber = globalSeq.Add(1)
	b.mu.Lock()
	b.records[tableName] = append(b.records[tableName], r)
	b.mu.Unlock()
	return nil
}

func (b *memStreamBackend) since(_ context.Context, tableName string, afterSeq int64, limit int) ([]*StreamRecord, error) {
	b.mu.RLock()
	all := b.records[tableName]
	b.mu.RUnlock()

	var result []*StreamRecord
	for _, r := range all {
		if r.SequenceNumber > afterSeq {
			result = append(result, r)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (b *memStreamBackend) latest(_ context.Context, tableName string) (int64, error) {
	b.mu.RLock()
	all := b.records[tableName]
	b.mu.RUnlock()

	if len(all) == 0 {
		return 0, nil
	}
	return all[len(all)-1].SequenceNumber, nil
}

// ─── SQLite implementation ────────────────────────────────────────────────────

const createStreamRecordsTable = `
CREATE TABLE IF NOT EXISTS dynamodb_stream_records (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  table_name   TEXT    NOT NULL,
  event_name   TEXT    NOT NULL,
  keys_json    BLOB    NOT NULL,
  new_image_json BLOB,
  old_image_json BLOB,
  created_at   INTEGER NOT NULL
)`

type sqlStreamBackend struct {
	dbFn func() *sql.DB
	db   *sql.DB
	once sync.Once
	err  error // set by init; sticky
}

// newSQLStreamBackend returns a backend that lazily resolves the *sql.DB and
// creates the stream records table on first use. Deferring DB resolution
// avoids blocking startup when the underlying store opens SQLite
// asynchronously.
func newSQLStreamBackend(dbFn func() *sql.DB) *sqlStreamBackend {
	return &sqlStreamBackend{dbFn: dbFn}
}

func (b *sqlStreamBackend) init() error {
	b.once.Do(func() {
		b.db = b.dbFn()
		if b.db == nil {
			b.err = fmt.Errorf("dynamodb stream_store: sqlite DB unavailable")
			return
		}
		if _, err := b.db.Exec(createStreamRecordsTable); err != nil {
			b.err = fmt.Errorf("dynamodb stream_store: create table: %w", err)
		}
	})
	return b.err
}

func (b *sqlStreamBackend) append(ctx context.Context, tableName string, r *StreamRecord) error {
	if err := b.init(); err != nil {
		return err
	}
	keysJSON, err := json.Marshal(r.Keys)
	if err != nil {
		return fmt.Errorf("stream append marshal keys: %w", err)
	}
	newJSON, err := json.Marshal(r.NewImage)
	if err != nil {
		return fmt.Errorf("stream append marshal new image: %w", err)
	}
	oldJSON, err := json.Marshal(r.OldImage)
	if err != nil {
		return fmt.Errorf("stream append marshal old image: %w", err)
	}

	res, err := b.db.ExecContext(ctx,
		`INSERT INTO dynamodb_stream_records (table_name, event_name, keys_json, new_image_json, old_image_json, created_at) VALUES (?,?,?,?,?,?)`,
		tableName, r.EventName, string(keysJSON), string(newJSON), string(oldJSON), r.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("stream append insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("stream append last insert id: %w", err)
	}
	r.SequenceNumber = id
	return nil
}

func (b *sqlStreamBackend) since(ctx context.Context, tableName string, afterSeq int64, limit int) ([]*StreamRecord, error) {
	if err := b.init(); err != nil {
		return nil, err
	}
	query := `SELECT id, event_name, keys_json, new_image_json, old_image_json, created_at
              FROM dynamodb_stream_records
              WHERE table_name = ? AND id > ?
              ORDER BY id ASC`
	args := []any{tableName, afterSeq}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("stream since query: %w", err)
	}
	defer rows.Close()

	var records []*StreamRecord
	for rows.Next() {
		var (
			r            StreamRecord
			keysJSON     string
			newImageJSON string
			oldImageJSON string
		)
		if err := rows.Scan(&r.SequenceNumber, &r.EventName, &keysJSON, &newImageJSON, &oldImageJSON, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("stream since scan: %w", err)
		}
		if err := json.Unmarshal([]byte(keysJSON), &r.Keys); err != nil {
			return nil, fmt.Errorf("stream since unmarshal keys: %w", err)
		}
		if newImageJSON != "" && newImageJSON != "null" {
			if err := json.Unmarshal([]byte(newImageJSON), &r.NewImage); err != nil {
				return nil, fmt.Errorf("stream since unmarshal new image: %w", err)
			}
		}
		if oldImageJSON != "" && oldImageJSON != "null" {
			if err := json.Unmarshal([]byte(oldImageJSON), &r.OldImage); err != nil {
				return nil, fmt.Errorf("stream since unmarshal old image: %w", err)
			}
		}
		records = append(records, &r)
	}
	return records, rows.Err()
}

func (b *sqlStreamBackend) latest(ctx context.Context, tableName string) (int64, error) {
	if err := b.init(); err != nil {
		return 0, err
	}
	var id int64
	err := b.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(id), 0) FROM dynamodb_stream_records WHERE table_name = ?`,
		tableName,
	).Scan(&id)
	return id, err
}
