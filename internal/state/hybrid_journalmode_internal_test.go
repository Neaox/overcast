//go:build !nosqlite

package state

import (
	"context"
	"testing"
	"time"
)

// TestHybridStore_JournalModeIsWAL pins the storage-pressure-handling item 0
// root-cause fix: both the writer connection (sqlite.go's newSQLiteStoreFile)
// and HybridStore's dedicated read pool (openReadPool) must actually be in
// SQLite WAL mode, not the rollback-journal default ("delete").
//
// Before the fix, both DSNs built their journal-mode/synchronous pragmas as
// bare "_journal_mode=WAL&_synchronous=NORMAL" query parameters — a
// go-sqlite3-style spelling that modernc.org/sqlite (this project's driver)
// does not recognize at all. Unrecognized DSN parameters are silently
// ignored by that driver (no error), so every persistent backend had been
// running in rollback-journal mode since inception despite every doc
// comment in this package claiming WAL. That mattered a great deal for the
// read pool specifically: rollback-journal mode's writer takes a brief but
// real EXCLUSIVE file lock at every commit, which blocks concurrent readers
// process-wide — the exact opposite of the "readers never block on the
// writer" guarantee WAL mode (and therefore the whole point of a separate
// read pool) is supposed to provide. See
// TestHybridStore_PacedWriteLoadDoesNotStarveReads for the observable
// consequence this caused under realistic write pressure.
func TestHybridStore_JournalModeIsWAL(t *testing.T) {
	s, err := NewHybridStore(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	var writerMode string
	if err := s.sqlite.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&writerMode); err != nil {
		t.Fatalf("query writer journal_mode: %v", err)
	}
	if writerMode != "wal" {
		t.Fatalf("writer connection journal_mode = %q, want %q", writerMode, "wal")
	}

	if s.sqliteRead == nil {
		t.Fatal("expected the dedicated read pool to be open")
	}
	var readerMode string
	if err := s.sqliteRead.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&readerMode); err != nil {
		t.Fatalf("query read pool journal_mode: %v", err)
	}
	if readerMode != "wal" {
		t.Fatalf("read pool journal_mode = %q, want %q", readerMode, "wal")
	}
}

// TestSQLiteStore_JournalModeIsWAL is TestHybridStore_JournalModeIsWAL's
// counterpart for the plain persistent backend (NewSQLiteStore), which
// shares newSQLiteStoreFile's DSN construction with HybridStore's writer.
func TestSQLiteStore_JournalModeIsWAL(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.ensureReady(ctx); err != nil {
		t.Fatalf("ensureReady: %v", err)
	}

	var mode string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want %q", mode, "wal")
	}
}
