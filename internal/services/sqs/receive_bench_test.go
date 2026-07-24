//go:build !nosqlite

package sqs

// Qualifying benchmark for docs/plans/storage-plan.md item 3.10 (SQS
// dedicated table + visible_at index — "only if benchmarks demand").
//
// The gate is the GROWTH CURVE of the receive path's storage cost vs queue
// depth, not the absolute number: listMessages (the storage read behind
// ReceiveMessage) does a full-queue Scan + JSON decode of every message per
// poll, so cost is expected to grow linearly with depth. The plan's trigger:
// implement the dedicated table only if the curve grows at depths realistic
// for local dev/CI (≤10k). Preload writes rows via store.Set directly (same
// key/JSON layout as saveMessage) so setup stays linear.
//
// Measurement conventions per docs/plans/storage-test-plan.md: allocs/op and
// B/op are the deterministic signals; wall time is machine/load-dependent.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

func benchmarkListMessages(b *testing.B, newStore func(b *testing.B) state.Store, depth int) {
	b.Helper()
	backing := newStore(b)
	ctx := context.Background()
	const queue = "bench-queue"

	for i := 0; i < depth; i++ {
		msg := Message{
			MessageID:     fmt.Sprintf("m-%08d", i),
			Body:          "payload-payload-payload-payload-payload-payload",
			MD5OfBody:     "d41d8cd98f00b204e9800998ecf8427e",
			SentTimestamp: time.Now().UnixMilli(),
		}
		raw, err := json.Marshal(&msg)
		if err != nil {
			b.Fatalf("marshal preload message: %v", err)
		}
		key := serviceutil.RegionKey("us-east-1", messageKey(queue, msg.MessageID))
		if err := backing.Set(ctx, nsMessages, key, string(raw)); err != nil {
			b.Fatalf("preload Set: %v", err)
		}
	}
	if err := state.Flush(ctx, backing); err != nil {
		b.Fatalf("flush preload: %v", err)
	}

	st := newSQSStore(backing, clock.New(), "us-east-1")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msgs, aerr := st.listMessages(ctx, queue)
		if aerr != nil {
			b.Fatalf("listMessages: %v", aerr)
		}
		if len(msgs) != depth {
			b.Fatalf("expected %d messages, got %d", depth, len(msgs))
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

func BenchmarkSQSReceiveScan_Memory_100(b *testing.B) {
	benchmarkListMessages(b, memoryBackedStore, 100)
}

func BenchmarkSQSReceiveScan_Memory_1000(b *testing.B) {
	benchmarkListMessages(b, memoryBackedStore, 1000)
}

func BenchmarkSQSReceiveScan_Memory_10000(b *testing.B) {
	benchmarkListMessages(b, memoryBackedStore, 10000)
}

func BenchmarkSQSReceiveScan_Hybrid_100(b *testing.B) {
	benchmarkListMessages(b, hybridBackedStore, 100)
}

func BenchmarkSQSReceiveScan_Hybrid_1000(b *testing.B) {
	benchmarkListMessages(b, hybridBackedStore, 1000)
}

func BenchmarkSQSReceiveScan_Hybrid_10000(b *testing.B) {
	benchmarkListMessages(b, hybridBackedStore, 10000)
}
