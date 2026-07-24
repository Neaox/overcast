//go:build !nosqlite

package sqs

// Acceptance benchmark for docs/plans/storage-plan.md item 3.10 (SQS
// dedicated table + visible_at index).
//
// This file originally benchmarked the OLD listMessages shape (full-queue
// Scan + JSON decode of every message per poll) — that benchmark is what
// tripped the plan's gate (see the qualifying commit "bench(state,sqs): run
// storage-plan 3.10/3.12 qualifying gates, record verdicts": memory
// 254us/2.7ms/28ms and hybrid 587us/5.3ms/85ms per poll at 100/1k/10k
// messages, linear in depth). It is adapted here to benchmark the
// REPLACEMENT: messageBackend.receiveCandidates, fetched at a fixed batch
// size (10 — SQS's real MaxNumberOfMessages ceiling) regardless of queue
// depth.
//
// Acceptance shape: for a STANDARD queue, the SQL backend's query has no
// ORDER BY (see message_backend.go's sqlMessageBackend.receiveCandidates
// doc comment) and answers straight off idx_sqs_messages_visible, so its
// curve should be flat-ish vs depth. For a FIFO queue, ORDER BY
// sequence_number is correctness-required and SQLite must sort every
// visible row before applying LIMIT — a real, documented limitation (see
// the same doc comment) — so the FIFO benchmark is expected to show growth
// vs depth in this benchmark's adversarial "every message visible" shape,
// even though it is still a large improvement over the pre-3.10 design
// (which additionally paid full JSON-decode cost for invisible messages
// too, and for every message regardless of whether it could ever be
// returned).
//
// Measurement conventions per docs/plans/storage-test-plan.md: allocs/op and
// B/op are the deterministic signals; wall time is machine/load-dependent.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// receiveBatchSize mirrors SQS's real MaxNumberOfMessages ceiling — the
// realistic fixed batch size a ReceiveMessage call asks for, regardless of
// how deep the queue is.
const receiveBatchSize = 10

// preloadMessages seeds depth messages as fast as the backend allows.
// Setup speed matters here: it is NOT what's being measured (b.ResetTimer
// runs after this), and an unbatched per-row write loop against a real
// SQLite file is dominated by per-statement overhead, not by anything
// storage-plan.md 3.10 changed. For the SQL backend this batches every
// preload row into a single transaction (mirroring the qualifying-gate
// benchmark's own preload-via-store.Set-then-one-Flush shape, just at the
// row-insert level instead of the kv level, since messages no longer route
// through kv at all). For the memory backend, putMessage is already O(1)
// per call, so no batching is needed.
func preloadMessages(b *testing.B, backend messageBackend, region, queue string, depth int, fifo bool, now time.Time) {
	b.Helper()
	ctx := context.Background()

	if sqlBackend, ok := backend.(*sqlMessageBackend); ok {
		if err := sqlBackend.init(); err != nil {
			b.Fatalf("init sql backend: %v", err)
		}
		tx, err := sqlBackend.db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatalf("begin preload tx: %v", err)
		}
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO sqs_messages (region, queue_name, message_id, visible_at, message_group_id, sequence_number, message_json)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			b.Fatalf("prepare preload insert: %v", err)
		}
		for i := 0; i < depth; i++ {
			msg, group, seq := benchMessage(i, fifo, now)
			raw, err := json.Marshal(msg)
			if err != nil {
				b.Fatalf("marshal preload message: %v", err)
			}
			if _, err := stmt.ExecContext(ctx, region, queue, msg.MessageID, now.UnixMilli(), group, seq, string(raw)); err != nil {
				b.Fatalf("preload insert: %v", err)
			}
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			b.Fatalf("commit preload tx: %v", err)
		}
		return
	}

	for i := 0; i < depth; i++ {
		msg, _, _ := benchMessage(i, fifo, now)
		if err := backend.putMessage(ctx, region, queue, msg); err != nil {
			b.Fatalf("preload putMessage: %v", err)
		}
	}
}

// benchMessage builds preload message #i and its structural (group,
// sequence) columns, matching what a real SendMessage would have produced.
func benchMessage(i int, fifo bool, now time.Time) (msg *Message, group string, seq int64) {
	m := &Message{
		MessageID:     fmt.Sprintf("m-%08d", i),
		Body:          "payload-payload-payload-payload-payload-payload",
		MD5OfBody:     "d41d8cd98f00b204e9800998ecf8427e",
		SentTimestamp: now.UnixMilli(),
		VisibleAfter:  now, // every message visible — the adversarial shape
	}
	if fifo {
		m.MessageGroupId = fmt.Sprintf("group-%d", i%50) // spread across groups so batches aren't dominated by one group's lock
		m.SequenceNumber = fmt.Sprintf("%d", i)
		return m, m.MessageGroupId, int64(i)
	}
	return m, "", 0
}

func benchmarkReceiveCandidates(b *testing.B, newStore func(b *testing.B) state.Store, depth int, fifo bool) {
	b.Helper()
	backing := newStore(b)
	ctx := context.Background()
	const region = "us-east-1"
	queue := "bench-queue"
	if fifo {
		queue = "bench-queue.fifo"
	}

	backend := newMessageBackendFor(state.Unwrap(backing, serviceName))
	now := time.Now()
	preloadMessages(b, backend, region, queue, depth, fifo, now)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msgs, err := backend.receiveCandidates(ctx, region, queue, now, receiveBatchSize, fifo)
		if err != nil {
			b.Fatalf("receiveCandidates: %v", err)
		}
		if len(msgs) != receiveBatchSize {
			b.Fatalf("expected %d messages, got %d", receiveBatchSize, len(msgs))
		}
	}
}

func memoryBackedStore(b *testing.B) state.Store {
	b.Helper()
	return state.NewMemoryStore()
}

func hybridBackedStore(b *testing.B) state.Store {
	b.Helper()
	s, err := state.NewHybridStore(b.TempDir(), time.Hour)
	if err != nil {
		b.Fatalf("NewHybridStore: %v", err)
	}
	b.Cleanup(func() { _ = s.Close() })
	if err := s.WaitReady(context.Background()); err != nil {
		b.Fatalf("WaitReady: %v", err)
	}
	return s
}

// ---- Standard queue (no ORDER BY needed) -----------------------------------

func BenchmarkSQSReceiveCandidates_Standard_Memory_100(b *testing.B) {
	benchmarkReceiveCandidates(b, memoryBackedStore, 100, false)
}

func BenchmarkSQSReceiveCandidates_Standard_Memory_1000(b *testing.B) {
	benchmarkReceiveCandidates(b, memoryBackedStore, 1000, false)
}

func BenchmarkSQSReceiveCandidates_Standard_Memory_10000(b *testing.B) {
	benchmarkReceiveCandidates(b, memoryBackedStore, 10000, false)
}

func BenchmarkSQSReceiveCandidates_Standard_Hybrid_100(b *testing.B) {
	benchmarkReceiveCandidates(b, hybridBackedStore, 100, false)
}

func BenchmarkSQSReceiveCandidates_Standard_Hybrid_1000(b *testing.B) {
	benchmarkReceiveCandidates(b, hybridBackedStore, 1000, false)
}

func BenchmarkSQSReceiveCandidates_Standard_Hybrid_10000(b *testing.B) {
	benchmarkReceiveCandidates(b, hybridBackedStore, 10000, false)
}

// ---- FIFO queue (ORDER BY sequence_number required) ------------------------

func BenchmarkSQSReceiveCandidates_FIFO_Memory_100(b *testing.B) {
	benchmarkReceiveCandidates(b, memoryBackedStore, 100, true)
}

func BenchmarkSQSReceiveCandidates_FIFO_Memory_1000(b *testing.B) {
	benchmarkReceiveCandidates(b, memoryBackedStore, 1000, true)
}

func BenchmarkSQSReceiveCandidates_FIFO_Memory_10000(b *testing.B) {
	benchmarkReceiveCandidates(b, memoryBackedStore, 10000, true)
}

func BenchmarkSQSReceiveCandidates_FIFO_Hybrid_100(b *testing.B) {
	benchmarkReceiveCandidates(b, hybridBackedStore, 100, true)
}

func BenchmarkSQSReceiveCandidates_FIFO_Hybrid_1000(b *testing.B) {
	benchmarkReceiveCandidates(b, hybridBackedStore, 1000, true)
}

func BenchmarkSQSReceiveCandidates_FIFO_Hybrid_10000(b *testing.B) {
	benchmarkReceiveCandidates(b, hybridBackedStore, 10000, true)
}
