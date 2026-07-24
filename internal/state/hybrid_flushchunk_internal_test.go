//go:build !nosqlite

package state

// Failing-first coverage for storage-pressure-handling item 2: chunked
// flush transactions. White-box (package state) so the pure splitting
// function chunkHybridFlushOps can be tested directly without touching
// SQLite — the end-to-end "actually commits in multiple transactions and
// every row survives" behavior is covered separately in
// hybrid_pressure_test.go (package state_test, exported API only).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func opsOfLen(n int) []hybridPendingOp {
	ops := make([]hybridPendingOp, n)
	for i := range ops {
		ops[i] = hybridPendingOp{seq: int64(i), op: walSet, namespace: "ns", key: "k", value: "v"}
	}
	return ops
}

func TestChunkHybridFlushOps_empty(t *testing.T) {
	if got := chunkHybridFlushOps(nil, 10, 0); got != nil {
		t.Fatalf("chunkHybridFlushOps(nil) = %v, want nil", got)
	}
}

func TestChunkHybridFlushOps_bothBoundsDisabled_singleChunk(t *testing.T) {
	ops := opsOfLen(1234)
	chunks := chunkHybridFlushOps(ops, 0, 0)
	if len(chunks) != 1 || len(chunks[0]) != 1234 {
		t.Fatalf("chunkHybridFlushOps with both bounds disabled = %d chunks (sizes %v), want 1 chunk of 1234", len(chunks), chunkSizes(chunks))
	}
}

func TestChunkHybridFlushOps_opCountBound_splitsEvenlyAndPreservesOrder(t *testing.T) {
	ops := opsOfLen(1201)
	for i := range ops {
		ops[i].seq = int64(i) // distinguishable per-op identity for order verification
	}
	chunks := chunkHybridFlushOps(ops, 500, 0)

	wantSizes := []int{500, 500, 201}
	if got := chunkSizes(chunks); !equalInts(got, wantSizes) {
		t.Fatalf("chunk sizes = %v, want %v", got, wantSizes)
	}

	// Order must be preserved: concatenating the chunks reproduces the
	// original sequence exactly.
	var flat []hybridPendingOp
	for _, c := range chunks {
		flat = append(flat, c...)
	}
	for i := range ops {
		if flat[i].seq != ops[i].seq {
			t.Fatalf("order not preserved at index %d: got seq %d, want %d", i, flat[i].seq, ops[i].seq)
		}
	}
}

func TestChunkHybridFlushOps_byteBound_splitsIndependentlyOfOpCount(t *testing.T) {
	// Ten ops of 100 bytes each ("ns"+"key"+ 94-byte value), byte cap 250 ->
	// a chunk closes every ~2-3 ops well before the (disabled) op-count bound.
	ops := make([]hybridPendingOp, 10)
	for i := range ops {
		ops[i] = hybridPendingOp{seq: int64(i), op: walSet, namespace: "ns", key: "key", value: string(make([]byte, 94))}
	}
	chunks := chunkHybridFlushOps(ops, 0, 250) // maxOps disabled; ~100 bytes/op
	if len(chunks) < 3 {
		t.Fatalf("chunkHybridFlushOps with a 250-byte cap over 10x~100-byte ops = %d chunks (sizes %v), want several small chunks", len(chunks), chunkSizes(chunks))
	}
	total := 0
	for _, c := range chunks {
		total += len(c)
	}
	if total != len(ops) {
		t.Fatalf("total ops across chunks = %d, want %d (no ops lost or duplicated)", total, len(ops))
	}
}

func TestChunkHybridFlushOps_exactMultipleOfCap_noTrailingEmptyChunk(t *testing.T) {
	ops := opsOfLen(1000)
	chunks := chunkHybridFlushOps(ops, 500, 0)
	wantSizes := []int{500, 500}
	if got := chunkSizes(chunks); !equalInts(got, wantSizes) {
		t.Fatalf("chunk sizes = %v, want %v (no trailing empty chunk)", got, wantSizes)
	}
}

func chunkSizes(chunks [][]hybridPendingOp) []int {
	sizes := make([]int, len(chunks))
	for i, c := range chunks {
		sizes[i] = len(c)
	}
	return sizes
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestHybridStore_FlushCrash_PartialChunkCommitReplaysSafely is the
// crash-window test for storage-pressure-handling item 2's truncation
// decision: the pending log is compacted only after every chunk in a flush
// batch commits, never chunk-by-chunk (see flushOnce's crash-durability
// comment). This proves the consequence of that choice end to end: if the
// process is interrupted after chunk 0 commits but before chunk 1 does, (a)
// chunk 0's rows are already durable in SQLite, (b) the on-disk pending log
// still holds the FULL original batch (nothing was compacted away), and (c)
// retrying the flush from scratch — replaying chunk 0's already-applied ops
// again alongside the rest — reproduces the correct end state rather than
// corrupting it, because every op type (INSERT OR REPLACE / DELETE ... WHERE)
// is idempotent under exact replay.
func TestHybridStore_FlushCrash_PartialChunkCommitReplaysSafely(t *testing.T) {
	dir := t.TempDir()
	s, err := NewHybridStore(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	waitForHybridSeedLoaded(t, s)
	// seedFromSQLite closes s.loaded (what waitForHybridSeedLoaded polls) and
	// THEN calls signalFlush() as a one-time nudge — those two steps are not
	// atomic, so without this pause the nudge can still be in flight when
	// the Set loop below starts, race in, and steal (and compact away) the
	// first handful of pending ops before this test ever installs its
	// chunk-1-fails hook, throwing off the exact row count the assertions
	// below depend on. The nudge is a harmless no-op once there's nothing
	// pending (see flushOnce's early return), so a short pause is enough to
	// let it settle deterministically before writes begin.
	time.Sleep(100 * time.Millisecond)

	const ns = "s3:objects"
	const rows = 1200 // hybridFlushChunkMaxOps=500 -> chunks of 500, 500, 200
	key := func(i int) string { return fmt.Sprintf("us-east-1/bucket/obj-%04d", i) }
	value := func(i int) string { return fmt.Sprintf(`{"i":%d}`, i) }
	for i := 0; i < rows; i++ {
		if err := s.Set(ctx, ns, key(i), value(i)); err != nil {
			t.Fatalf("Set %d: %v", i, err)
		}
	}

	// Simulate a crash between chunk 0's commit and chunk 1's: let chunk 0
	// through, fail deterministically right before chunk 1 begins.
	simulatedCrash := errors.New("simulated crash between chunks")
	s.testFlushChunkHook = func(chunkIndex int) error {
		if chunkIndex == 1 {
			return simulatedCrash
		}
		return nil
	}
	if err := s.flush(ctx); !errors.Is(err, simulatedCrash) {
		t.Fatalf("flush() = %v, want it to fail with the simulated crash at chunk 1", err)
	}

	// (a) Chunk 0's rows already reached SQLite directly (bypassing the
	// in-memory overlay, which still has everything regardless of flush
	// state — reading raw SQLite is the only way to observe what actually
	// committed).
	for i := 0; i < 500; i++ {
		got, found, err := hybridSQLiteRawGet(ctx, s.sqlite.db, ns, key(i))
		if err != nil || !found || got != value(i) {
			t.Fatalf("chunk 0 row %d after simulated crash: got=%q found=%v err=%v, want %q found=true", i, got, found, err, value(i))
		}
	}
	// Chunk 1's rows must NOT have reached SQLite yet.
	if _, found, err := hybridSQLiteRawGet(ctx, s.sqlite.db, ns, key(600)); err != nil || found {
		t.Fatalf("chunk 1 row 600 after simulated crash: found=%v err=%v, want found=false (chunk 1 never committed)", found, err)
	}

	// (b) The pending log must still hold the FULL original batch — proof
	// compaction did not run (it only runs after every chunk commits).
	pendingBytes, err := os.ReadFile(s.pendingPath)
	if err != nil {
		t.Fatalf("read pending log after simulated crash: %v", err)
	}
	if lineCount := countNonEmptyLines(pendingBytes); lineCount != rows {
		t.Fatalf("pending log has %d entries after simulated crash, want all %d (compaction must not have run)", lineCount, rows)
	}

	// (c) Retry the flush (the "restart" case) without the hook: this
	// replays the whole batch, including chunk 0's already-committed ops.
	s.testFlushChunkHook = nil
	if err := s.flush(ctx); err != nil {
		t.Fatalf("retry flush after simulated crash: %v", err)
	}
	for i := 0; i < rows; i++ {
		got, found, err := hybridSQLiteRawGet(ctx, s.sqlite.db, ns, key(i))
		if err != nil || !found || got != value(i) {
			t.Fatalf("row %d after recovery flush: got=%q found=%v err=%v, want %q found=true", i, got, found, err, value(i))
		}
	}
}

func countNonEmptyLines(b []byte) int {
	count := 0
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				count++
			}
			start = i + 1
		}
	}
	if start < len(b) {
		count++
	}
	return count
}
