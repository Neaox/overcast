//go:build !nosqlite

package state_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	_ "modernc.org/sqlite"
)

// ---- HybridStore contract tests --------------------------------------------
//
// These mirror the MemoryStore and SQLiteStore tests to verify the hybrid
// implementation satisfies the same Store contract.

func newHybridStore(t *testing.T) *state.HybridStore {
	t.Helper()
	s, err := state.NewHybridStore(t.TempDir(), 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestHybridStore_GetSetDelete(t *testing.T) {
	s := newHybridStore(t)
	ctx := context.Background()

	// Get missing key returns not-found.
	_, found, err := s.Get(ctx, "ns", "key")
	if err != nil || found {
		t.Fatalf("Get missing: err=%v found=%v", err, found)
	}

	// Set and retrieve.
	if err := s.Set(ctx, "ns", "key", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, found, err := s.Get(ctx, "ns", "key")
	if err != nil || !found || got != "value" {
		t.Fatalf("Get after Set: err=%v found=%v got=%q", err, found, got)
	}

	// Delete.
	if err := s.Delete(ctx, "ns", "key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, found, _ = s.Get(ctx, "ns", "key")
	if found {
		t.Fatal("key should not exist after delete")
	}
}

func TestHybridStore_Delete_nonExistent(t *testing.T) {
	s := newHybridStore(t)
	if err := s.Delete(context.Background(), "ns", "does-not-exist"); err != nil {
		t.Fatalf("Delete non-existent key returned error: %v", err)
	}
}

func TestHybridStore_Set_overwrite(t *testing.T) {
	s := newHybridStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "ns", "key", "v1"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := s.Set(ctx, "ns", "key", "v2"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	got, _, _ := s.Get(ctx, "ns", "key")
	if got != "v2" {
		t.Errorf("expected overwritten value v2, got %q", got)
	}
}

func TestHybridStore_List_prefix(t *testing.T) {
	s := newHybridStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ns", "a/1", "v")
	_ = s.Set(ctx, "ns", "a/2", "v")
	_ = s.Set(ctx, "ns", "b/1", "v")

	keys, err := s.List(ctx, "ns", "a/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys with prefix 'a/', got %d: %v", len(keys), keys)
	}
}

func TestHybridStore_Scan(t *testing.T) {
	s := newHybridStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "ns", "x/1", "hello")
	_ = s.Set(ctx, "ns", "x/2", "world")
	_ = s.Set(ctx, "ns", "y/1", "other")

	pairs, err := s.Scan(ctx, "ns", "x/")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(pairs) != 2 {
		t.Errorf("expected 2 pairs, got %d", len(pairs))
	}
}

// TestHybridStore_Persistence verifies that data written before Close is
// available in a new HybridStore opened against the same directory.
func TestHybridStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Write some data and close (triggers final flush).
	s1, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (1): %v", err)
	}
	if err := s1.Set(ctx, "svc", "key", "persisted"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close (1): %v", err)
	}

	// Re-open and verify the value was loaded from SQLite.
	s2, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (2): %v", err)
	}
	defer s2.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		got, found, err := s2.Get(ctx, "svc", "key")
		if err != nil {
			t.Fatalf("Get after reopen: %v", err)
		}
		if found && got == "persisted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Get after reopen: found=%v got=%q", found, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestHybridStore_DeletePersistence verifies that a delete is flushed and the
// key is absent in a new store opened against the same directory.
func TestHybridStore_DeletePersistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (1): %v", err)
	}
	_ = s1.Set(ctx, "svc", "key", "to-delete")
	if err := s1.Close(); err != nil {
		t.Fatalf("Close (1): %v", err)
	}

	s2, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (2): %v", err)
	}
	_ = s2.Delete(ctx, "svc", "key")
	if err := s2.Close(); err != nil {
		t.Fatalf("Close (2): %v", err)
	}

	s3, err := state.NewHybridStore(dir, 10*time.Second)
	if err != nil {
		t.Fatalf("NewHybridStore (3): %v", err)
	}
	defer s3.Close()

	_, found, err := s3.Get(ctx, "svc", "key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Error("key should be absent after delete + reopen")
	}
}

func TestHybridStore_RestoreLargeState(t *testing.T) {
	if testing.Short() {
		t.Skip("large restore regression test")
	}

	// Given: a value-heavy persisted hybrid store. Prefix reads only need keys,
	// so they should not wait for the full value payload to hydrate memory.
	dir := t.TempDir()
	ctx := context.Background()
	const rows = 10000
	value := strings.Repeat("x", 4096)
	seedHybridSQLiteNamespace(t, dir, "svc:metadata", rows, value)

	// When: the store is reopened and the first state-backed read runs while
	// the cache seed is still in progress.
	core, logs := observer.New(zap.DebugLevel)
	s2, err := state.NewHybridStoreWithLogger(dir, time.Hour, zap.New(core))
	if err != nil {
		t.Fatalf("NewHybridStore (2): %v", err)
	}
	defer s2.Close()

	_, err = s2.List(ctx, "svc:metadata", "queue/")
	if err != nil {
		t.Fatalf("List after restore: %v", err)
	}

	// Then: the read must not block on cache hydration. A no-op seed may complete
	// first, but it must not scan or block on the persisted payload rows. Avoid a
	// hard wall-clock threshold here: -race/-cover overhead in CI can make an
	// indexed SQLite key query over 10k rows exceed an arbitrary sub-second bound.
	if logs.FilterMessage("hybrid wait blocked").Len() != 0 {
		t.Fatalf("first read blocked on seed, got logs: %#v", logs.All())
	}
	if logs.FilterMessage("hybrid seed complete").Len() > 0 {
		loaded := observedIntField(t, logs, "hybrid seed complete", "loaded")
		if loaded != 0 {
			t.Fatalf("first read waited for seeded rows = %d, got logs: %#v", loaded, logs.All())
		}
	}
	waitForObservedLog(t, logs, "hybrid seed complete")
	keys, err := s2.List(ctx, "svc:metadata", "queue/")
	if err != nil {
		t.Fatalf("List after sqlite ready: %v", err)
	}
	if len(keys) != rows {
		t.Fatalf("restored rows after sqlite ready = %d, want %d", len(keys), rows)
	}
}

func TestHybridStore_RestoreSkipsStartupKVScan(t *testing.T) {
	if testing.Short() {
		t.Skip("large restore regression test")
	}

	// Given: mostly bulk SQS messages plus a small metadata namespace.
	dir := t.TempDir()
	ctx := context.Background()
	seedHybridSQLiteNamespace(t, dir, "sqs:queues", 2, "queue-metadata")
	seedHybridSQLiteNamespace(t, dir, "sqs:messages", 5000, strings.Repeat("x", 4096))

	// When: hybrid restore completes.
	core, logs := observer.New(zap.DebugLevel)
	s, err := state.NewHybridStoreWithLogger(dir, time.Hour, zap.New(core))
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	waitForObservedLog(t, logs, "hybrid seed complete")

	// Then: startup does not hydrate any KV rows, but both metadata and bulk
	// messages remain readable via SQLite-backed hybrid reads.
	loaded := observedIntField(t, logs, "hybrid seed complete", "loaded")
	if loaded != 0 {
		t.Fatalf("seeded rows = %d, want no startup KV scan", loaded)
	}
	metadataKeys, err := s.List(ctx, "sqs:queues", "queue/")
	if err != nil {
		t.Fatalf("List metadata after seed: %v", err)
	}
	if len(metadataKeys) != 2 {
		t.Fatalf("metadata rows = %d, want 2", len(metadataKeys))
	}
	keys, err := s.List(ctx, "sqs:messages", "queue/")
	if err != nil {
		t.Fatalf("List bulk messages after seed: %v", err)
	}
	if len(keys) != 5000 {
		t.Fatalf("bulk message rows = %d, want 5000", len(keys))
	}
}

func seedHybridSQLiteNamespace(t *testing.T, dir string, namespace string, rows int, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "overcast.db")+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		t.Fatalf("open seed sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS kv (
		namespace TEXT NOT NULL,
		key       TEXT NOT NULL,
		value     TEXT NOT NULL,
		PRIMARY KEY (namespace, key)
	)`); err != nil {
		t.Fatalf("create kv: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin seed tx: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO kv (namespace, key, value) VALUES (?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare seed: %v", err)
	}
	defer stmt.Close()
	for i := 0; i < rows; i++ {
		if _, err := stmt.Exec(namespace, fmt.Sprintf("queue/%08d", i), value); err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}
}

func waitForObservedLog(t *testing.T, logs *observer.ObservedLogs, message string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if logs.FilterMessage(message).Len() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log %q, got logs: %#v", message, logs.All())
}

func observedIntField(t *testing.T, logs *observer.ObservedLogs, message, field string) int {
	t.Helper()
	entries := logs.FilterMessage(message).All()
	if len(entries) == 0 {
		t.Fatalf("missing log %q", message)
	}
	for _, f := range entries[0].Context {
		if f.Key == field {
			return int(f.Integer)
		}
	}
	t.Fatalf("missing field %q in log %q: %#v", field, message, entries[0].Context)
	return 0
}
