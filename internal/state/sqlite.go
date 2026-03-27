package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver — no CGO, works on all platforms
)

// SQLiteStore is the persistent Store implementation.
// State survives process restarts, stored in a single SQLite file under DataDir.
//
// Schema is a single key-value table — deliberately simple. We don't need
// relational features; we need durable K/V storage with prefix scanning.
//
// The file path is: <DataDir>/overcast.db.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens (or creates) the SQLite database at dataDir/overcast.db.
// The data directory is created if it doesn't exist.
func NewSQLiteStore(dataDir string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("sqlite store: create data dir %q: %w", dataDir, err)
	}

	dbPath := filepath.Join(dataDir, "overcast.db")
	// modernc pure-Go driver — no CGO required, works on Mac/Linux/Windows
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("sqlite store: open %q: %w", dbPath, err)
	}

	// WAL mode + a connection pool of 1 writer + N readers is the standard
	// SQLite configuration for concurrent access.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite store: migrate: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// migrate creates the schema if it doesn't already exist.
// We use IF NOT EXISTS so this is idempotent — safe to call on every startup.
func migrate(db *sql.DB) error {
	const schema = `
	CREATE TABLE IF NOT EXISTS kv (
		namespace TEXT NOT NULL,
		key       TEXT NOT NULL,
		value     TEXT NOT NULL,
		PRIMARY KEY (namespace, key)
	);
	CREATE INDEX IF NOT EXISTS idx_kv_ns_key ON kv (namespace, key);
	`
	_, err := db.Exec(schema)
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, namespace, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM kv WHERE namespace = ? AND key = ?`,
		namespace, key,
	).Scan(&value)

	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite get [%s/%s]: %w", namespace, key, err)
	}
	return value, true, nil
}

func (s *SQLiteStore) Set(ctx context.Context, namespace, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		// INSERT OR REPLACE is SQLite's upsert — insert if not exists, replace if exists.
		`INSERT OR REPLACE INTO kv (namespace, key, value) VALUES (?, ?, ?)`,
		namespace, key, value,
	)
	if err != nil {
		return fmt.Errorf("sqlite set [%s/%s]: %w", namespace, key, err)
	}
	return nil
}

func (s *SQLiteStore) Delete(ctx context.Context, namespace, key string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM kv WHERE namespace = ? AND key = ?`,
		namespace, key,
	)
	if err != nil {
		return fmt.Errorf("sqlite delete [%s/%s]: %w", namespace, key, err)
	}
	return nil
}

func (s *SQLiteStore) List(ctx context.Context, namespace, prefix string) ([]string, error) {
	// LIKE with % suffix performs a prefix scan. We escape any literal % or _
	// in the prefix to avoid them being treated as LIKE wildcards.
	likePattern := escapeLike(prefix) + "%"

	rows, err := s.db.QueryContext(ctx,
		`SELECT key FROM kv WHERE namespace = ? AND key LIKE ? ESCAPE '\'`,
		namespace, likePattern,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite list [%s/%s*]: %w", namespace, prefix, err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("sqlite list scan: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite list rows: %w", err)
	}
	if keys == nil {
		keys = []string{}
	}
	return keys, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// escapeLike escapes LIKE special characters (% and _) in a string so it can
// safely be used as a LIKE prefix.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
