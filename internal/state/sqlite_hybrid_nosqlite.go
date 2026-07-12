//go:build nosqlite

package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// SQLiteStore is a stub that satisfies the Store interface for builds
// where SQLite support is excluded (-tags nosqlite).
type SQLiteStore struct {
	*MemoryStore
}

// NewSQLiteStore returns an error — SQLite support was excluded at build time.
func NewSQLiteStore(_ string) (*SQLiteStore, error) {
	return nil, fmt.Errorf("sqlite store: not compiled with SQLite support (build without -tags nosqlite)")
}

// NewSQLiteStoreWAL returns an error — SQLite support was excluded at build time.
func NewSQLiteStoreWAL(_ string) (*SQLiteStore, error) {
	return nil, fmt.Errorf("sqlite store: not compiled with SQLite support (build without -tags nosqlite)")
}

// DB returns nil — the stub has no database connection.
func (s *SQLiteStore) DB() *sql.DB { return nil }

// HybridStore is a stub that satisfies the Store interface for builds
// where SQLite support is excluded (-tags nosqlite).
type HybridStore struct {
	*MemoryStore
}

// NewHybridStore returns an error — SQLite support was excluded at build time.
func NewHybridStore(_ string, _ time.Duration) (*HybridStore, error) {
	return nil, fmt.Errorf("hybrid store: not compiled with SQLite support (build without -tags nosqlite)")
}

// NewHybridStoreWithLogger returns an error — SQLite support was excluded at build time.
func NewHybridStoreWithLogger(_ string, _ time.Duration, _ *zap.Logger) (*HybridStore, error) {
	return nil, fmt.Errorf("hybrid store: not compiled with SQLite support (build without -tags nosqlite)")
}

// DB returns nil — the stub has no database connection.
func (s *HybridStore) DB() *sql.DB { return nil }

// WaitReady returns nil immediately — the nosqlite stub is always ready
// (it delegates to MemoryStore which has no async initialisation).
func (s *HybridStore) WaitReady(_ context.Context) error { return nil }
