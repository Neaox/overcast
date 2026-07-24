//go:build !nosqlite

package sqs

// send_bench_test.go records the graduation cost storage-plan.md's
// "Settled decisions" section requires accepting honestly: putMessage
// (SendMessage's storage write, also used by ReceiveMessage's in-flight
// mutation, ChangeMessageVisibility, and DLQ moves) now writes synchronously
// to the dedicated sqs_messages SQLite table on hybrid/persistent, instead
// of the old generic-kv write — which, in HybridStore, only appended a JSON
// line to an unsynced pending log and returned, with the real SQLite write
// happening later in a batched background flush.
//
// This file benchmarks BOTH sides of that trade-off directly against a real
// *state.HybridStore, at the same 100/1k/10k pre-existing-messages depths as
// receive_bench_test.go, so the cost is measured, not asserted:
//
//   - BenchmarkSQSSend_OldKVPath_Hybrid_*: a plain HybridStore.Set into a kv
//     namespace (simulating the pre-3.10 write shape — an async, batched
//     pending-log append).
//   - BenchmarkSQSSend_NewTableInsert_Hybrid_*: sqlMessageBackend.putMessage
//     (the actual new code path — a synchronous SQLite INSERT/UPSERT).
//
// LOAD CAVEAT (per docs/plans/storage-test-plan.md): allocs/op is the
// deterministic signal; wall-clock ns/op is machine- and load-dependent —
// this repo's benchmarks are frequently run alongside other concurrent
// agents' work on the same machine, so treat absolute ns/op numbers as
// indicative only. The allocs/op delta between the two paths is the honest
// measure of what this graduation costs on the write side.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

const sendBenchNamespace = "sqs:messages-bench-old-path" // isolated kv namespace; never read by production code

func benchmarkSendOldKVPath(b *testing.B, depth int) {
	b.Helper()
	ctx := context.Background()
	store, err := state.NewHybridStore(b.TempDir(), time.Hour)
	if err != nil {
		b.Fatalf("NewHybridStore: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })
	if err := store.WaitReady(ctx); err != nil {
		b.Fatalf("WaitReady: %v", err)
	}

	for i := 0; i < depth; i++ {
		msg := &Message{MessageID: fmt.Sprintf("preload-%08d", i), Body: "payload-payload-payload-payload-payload-payload"}
		raw := mustMarshalBenchMessage(b, msg)
		key := fmt.Sprintf("us-east-1/bench-queue/%s", msg.MessageID)
		if err := store.Set(ctx, sendBenchNamespace, key, raw); err != nil {
			b.Fatalf("preload Set: %v", err)
		}
	}
	if err := state.Flush(ctx, store); err != nil {
		b.Fatalf("flush preload: %v", err)
	}

	msg := &Message{MessageID: "send-me", Body: "payload-payload-payload-payload-payload-payload"}
	raw := mustMarshalBenchMessage(b, msg)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("us-east-1/bench-queue/send-me-%d", i)
		if err := store.Set(ctx, sendBenchNamespace, key, raw); err != nil {
			b.Fatalf("Set: %v", err)
		}
	}
}

func benchmarkSendNewTableInsert(b *testing.B, depth int) {
	b.Helper()
	ctx := context.Background()
	store, err := state.NewHybridStore(b.TempDir(), time.Hour)
	if err != nil {
		b.Fatalf("NewHybridStore: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })
	if err := store.WaitReady(ctx); err != nil {
		b.Fatalf("WaitReady: %v", err)
	}

	backend := newMessageBackendFor(state.Unwrap(store, serviceName))
	const region, queue = "us-east-1", "bench-queue"
	now := time.Now()
	preloadMessages(b, backend, region, queue, depth, false, now)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := &Message{
			MessageID:    fmt.Sprintf("send-me-%d", i),
			Body:         "payload-payload-payload-payload-payload-payload",
			VisibleAfter: now,
		}
		if err := backend.putMessage(ctx, region, queue, msg); err != nil {
			b.Fatalf("putMessage: %v", err)
		}
	}
}

func mustMarshalBenchMessage(b *testing.B, msg *Message) string {
	b.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		b.Fatalf("marshal bench message: %v", err)
	}
	return string(raw)
}

// ---- Old kv path (async pending-log append) --------------------------------

func BenchmarkSQSSend_OldKVPath_Hybrid_100(b *testing.B) {
	benchmarkSendOldKVPath(b, 100)
}

func BenchmarkSQSSend_OldKVPath_Hybrid_1000(b *testing.B) {
	benchmarkSendOldKVPath(b, 1000)
}

func BenchmarkSQSSend_OldKVPath_Hybrid_10000(b *testing.B) {
	benchmarkSendOldKVPath(b, 10000)
}

// ---- New table insert (synchronous SQLite write) ---------------------------

func BenchmarkSQSSend_NewTableInsert_Hybrid_100(b *testing.B) {
	benchmarkSendNewTableInsert(b, 100)
}

func BenchmarkSQSSend_NewTableInsert_Hybrid_1000(b *testing.B) {
	benchmarkSendNewTableInsert(b, 1000)
}

func BenchmarkSQSSend_NewTableInsert_Hybrid_10000(b *testing.B) {
	benchmarkSendNewTableInsert(b, 10000)
}
