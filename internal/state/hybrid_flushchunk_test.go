//go:build !nosqlite

package state_test

// End-to-end regression coverage for storage-pressure-handling item 2:
// chunked flush transactions. Exported-API only (package state_test) — the
// pure chunk-splitting logic itself (chunkHybridFlushOps) is unit-tested
// directly in hybrid_flushchunk_internal_test.go (package state).

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/state"
)

// TestHybridStore_FlushChunksLargeBatchesAndPersistsAllRows proves a flush
// batch larger than the internal per-transaction chunk cap (500 ops,
// internal/state/hybrid.go's hybridFlushChunkMaxOps) commits as multiple
// SQLite transactions — observable via DebugMetrics.FlushHistory's Chunks
// field — and that every row is still durable and readable afterward, from
// a freshly reopened store (proving the data actually reached disk, not
// just this process's in-memory overlay).
func TestHybridStore_FlushChunksLargeBatchesAndPersistsAllRows(t *testing.T) {
	dir := t.TempDir()
	s, err := state.NewHybridStore(dir, time.Hour) // long interval: only the explicit Flush below should fire
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}

	const ns = "s3:objects" // TierCached — Get after flush reads straight from SQLite
	const rows = 1300       // > hybridFlushChunkMaxOps (500), forcing multiple chunks
	key := func(i int) string { return fmt.Sprintf("us-east-1/bucket/obj-%04d", i) }
	value := func(i int) string { return fmt.Sprintf(`{"i":%d}`, i) }

	for i := 0; i < rows; i++ {
		if err := s.Set(ctx, ns, key(i), value(i)); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	if err := state.Flush(ctx, s); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	snapshots, ok := state.DebugMetricsSnapshot(ctx, s, state.DebugMetricsOptions{})
	if !ok || len(snapshots) != 1 {
		t.Fatalf("DebugMetricsSnapshot: ok=%v snapshots=%d, want one entry", ok, len(snapshots))
	}
	// NOTE: HybridStore's background seed-completion nudge (seedFromSQLite's
	// one-time signalFlush call) can race with the Set loop above and cause
	// an extra implicit flush of whatever happened to be pending at that
	// instant, on top of the explicit Flush call below — so this asserts
	// over the SUM of flush history entries rather than assuming exactly
	// one record, which would be flaky against that (pre-existing,
	// unrelated-to-chunking) timing race.
	history := snapshots[0].FlushHistory
	if len(history) == 0 {
		t.Fatal("FlushHistory is empty after an explicit Flush")
	}
	var totalEntries int
	var sawMultiChunk bool
	for _, h := range history {
		if !h.Committed {
			t.Fatalf("flush record not committed: %+v", h)
		}
		totalEntries += h.Entries
		if h.Chunks > 1 {
			sawMultiChunk = true
		}
	}
	if totalEntries != rows {
		t.Fatalf("sum of FlushHistory.Entries = %d, want %d (history: %+v)", totalEntries, rows, history)
	}
	if !sawMultiChunk {
		t.Fatalf("no FlushHistory record shows Chunks > 1 for a %d-row batch (chunking not observed): %+v", rows, history)
	}
	t.Logf("flush history for %d rows: %+v", rows, history)

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen against the same data dir: every row must be durable and
	// readable, proving the chunked commits actually reached disk in full —
	// not just an in-memory illusion of success.
	s2, err := state.NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore (reopen): %v", err)
	}
	defer s2.Close()
	if err := s2.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady (reopen): %v", err)
	}
	for i := 0; i < rows; i++ {
		got, found, err := s2.Get(ctx, ns, key(i))
		if err != nil {
			t.Fatalf("Get %d after reopen: %v", i, err)
		}
		if !found {
			t.Fatalf("Get %d after reopen: not found (row lost across a chunk boundary?)", i)
		}
		if got != value(i) {
			t.Fatalf("Get %d after reopen = %q, want %q", i, got, value(i))
		}
	}
}

// TestHybridStore_FlushSmallBatchStaysSingleChunk is the negative case: a
// batch well under the chunk cap must still flush in exactly one
// transaction — chunking must not add overhead to the common case.
func TestHybridStore_FlushSmallBatchStaysSingleChunk(t *testing.T) {
	s := newHybridStore(t) // 10s flush interval; helper from hybrid_test.go
	ctx := context.Background()

	const rows = 10
	for i := 0; i < rows; i++ {
		if err := s.Set(ctx, "sqs:queues", fmt.Sprintf("queue-%d", i), "v"); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}
	if err := state.Flush(ctx, s); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	snapshots, ok := state.DebugMetricsSnapshot(ctx, s, state.DebugMetricsOptions{})
	if !ok || len(snapshots) != 1 {
		t.Fatalf("DebugMetricsSnapshot: ok=%v snapshots=%d, want one entry", ok, len(snapshots))
	}
	history := snapshots[0].FlushHistory
	if len(history) == 0 {
		t.Fatal("FlushHistory is empty after an explicit Flush")
	}
	last := history[len(history)-1]
	if last.Chunks != 1 {
		t.Fatalf("last flush record Chunks = %d, want 1 for a %d-row batch", last.Chunks, rows)
	}
}
