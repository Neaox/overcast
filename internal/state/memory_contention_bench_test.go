package state_test

// Qualifying benchmark for docs/plans/storage-plan.md item 3.12 (MemoryStore
// per-namespace locking — "only if benchmarks show contention").
//
// MemoryStore guards all namespaces with one RWMutex, so cross-service write
// bursts serialize. Sharding the lock can only help writers targeting
// DIFFERENT namespaces, so that is what this measures: N goroutines, each
// writing to its own namespace, at N = 1/4/16. Values are pre-built strings —
// services marshal JSON in their own store.go BEFORE calling Set, so
// including marshal cost here would measure the wrong thing (the plan
// predicts marshaling dominates; that happens outside the lock).
//
// The gate is the SCALING RATIO, not absolute ns/op: if aggregate throughput
// scales near-linearly from 1→16 writers (per-op cost roughly flat), the
// mutex is not the bottleneck and 3.12 closes as won't-do. For attribution,
// run with -mutexprofile (see the plan's capture-method note):
//
//	go test -run '^$' -bench MemoryStore_CrossNamespaceWrites \
//	  -mutexprofile mutex.out ./internal/state/
//
// Measurement conventions per docs/plans/storage-test-plan.md.

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

func benchmarkCrossNamespaceWrites(b *testing.B, writers int) {
	b.Helper()
	runtime.SetMutexProfileFraction(1)
	defer runtime.SetMutexProfileFraction(0)

	store := state.NewMemoryStore()
	ctx := context.Background()
	value := "pre-marshaled-value-0123456789-0123456789-0123456789-0123456789"

	b.ReportAllocs()
	b.ResetTimer()

	perWriter := b.N / writers
	if perWriter == 0 {
		perWriter = 1
	}
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			namespace := fmt.Sprintf("svc%d:items", w)
			for i := 0; i < perWriter; i++ {
				if err := store.Set(ctx, namespace, fmt.Sprintf("k-%d", i), value); err != nil {
					b.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

func BenchmarkMemoryStore_CrossNamespaceWrites_1(b *testing.B) {
	benchmarkCrossNamespaceWrites(b, 1)
}

func BenchmarkMemoryStore_CrossNamespaceWrites_4(b *testing.B) {
	benchmarkCrossNamespaceWrites(b, 4)
}

func BenchmarkMemoryStore_CrossNamespaceWrites_16(b *testing.B) {
	benchmarkCrossNamespaceWrites(b, 16)
}
