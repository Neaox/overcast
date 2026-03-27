package state_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/your-org/overcast/internal/state"
)

// Benchmarks establish a baseline for MemoryStore hot-path performance.
// Run with: go test -bench=. -benchmem ./internal/state/...
//
// Expected order of magnitude on a modern laptop:
//   BenchmarkMemoryStore_Get    ~100 ns/op,   0 allocs/op
//   BenchmarkMemoryStore_Set    ~150 ns/op,   1 alloc/op  (string copy)
//   BenchmarkMemoryStore_List   ~500 ns/op, varies by store size

func BenchmarkMemoryStore_Get(b *testing.B) {
	s := state.NewMemoryStore()
	ctx := context.Background()
	s.Set(ctx, "s3:objects", "bucket/key.txt", `{"key":"key.txt","size":1024}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Get(ctx, "s3:objects", "bucket/key.txt")
	}
}

func BenchmarkMemoryStore_Set(b *testing.B) {
	s := state.NewMemoryStore()
	ctx := context.Background()
	value := `{"key":"key.txt","body":"aGVsbG8gd29ybGQ=","size":11}`

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Set(ctx, "s3:objects", fmt.Sprintf("bucket/key%d.txt", i), value)
	}
}

func BenchmarkMemoryStore_List_SmallStore(b *testing.B) {
	s := state.NewMemoryStore()
	ctx := context.Background()
	// Seed 10 objects in one bucket
	for i := 0; i < 10; i++ {
		s.Set(ctx, "s3:objects", fmt.Sprintf("my-bucket/object%d", i), `{}`)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.List(ctx, "s3:objects", "my-bucket/")
	}
}

func BenchmarkMemoryStore_List_LargeStore(b *testing.B) {
	s := state.NewMemoryStore()
	ctx := context.Background()
	// Seed 1000 objects across two buckets
	for i := 0; i < 500; i++ {
		s.Set(ctx, "s3:objects", fmt.Sprintf("bucket-a/object%d", i), `{}`)
		s.Set(ctx, "s3:objects", fmt.Sprintf("bucket-b/object%d", i), `{}`)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.List(ctx, "s3:objects", "bucket-a/")
	}
}

func BenchmarkMemoryStore_ConcurrentReads(b *testing.B) {
	s := state.NewMemoryStore()
	ctx := context.Background()
	s.Set(ctx, "sqs:messages", "my-queue/msg-1", `{"body":"hello"}`)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Get(ctx, "sqs:messages", "my-queue/msg-1")
		}
	})
}
