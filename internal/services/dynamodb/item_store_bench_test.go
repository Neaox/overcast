//go:build !nosqlite

// Benchmarks storage-access-plan.md A3's acceptance criterion: Scan Limit=25
// stays flat as table size grows, instead of degrading with the number of
// items already in the table (the old scanAll-then-slice behavior, which
// read and decoded every item on every call regardless of Limit).
//
// Convention follows internal/services/cloudwatch/metric_burst_bench_test.go:
// preload writes items directly via itemBackend.put (not through the
// handler), so setup cost stays linear regardless of what the read path
// does; b.ReportAllocs() makes allocs/op — not wall-clock ns/op, which is
// noisy on a shared machine — the signal to compare across table sizes.
package dynamodb

import (
	"context"
	"fmt"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

func benchmarkScanPageLimit25(b *testing.B, backend itemBackend, preExisting int) {
	b.Helper()
	ctx := context.Background()
	const table = "bench-scan-page"

	for i := 0; i < preExisting; i++ {
		hk := fmt.Sprintf("h%08d", i)
		item := Item{"hk": attrValue{"S": hk}, "payload": attrValue{"S": "some-attribute-value"}}
		if err := backend.put(ctx, table, hk, "", item); err != nil {
			b.Fatalf("preload put: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := backend.scanPage(ctx, table, false, "", "", 25); err != nil {
			b.Fatalf("scanPage: %v", err)
		}
	}
}

func newSQLItemBackendForBench(b *testing.B) itemBackend {
	b.Helper()
	dir := b.TempDir()
	store, err := state.NewSQLiteStore(dir)
	if err != nil {
		b.Fatalf("state.NewSQLiteStore: %v", err)
	}
	b.Cleanup(func() {
		if err := store.Close(); err != nil {
			b.Logf("store.Close: %v", err)
		}
	})
	backend := newItemBackendFor(store)
	if _, ok := backend.(*sqlItemBackend); !ok {
		b.Fatalf("expected sqlItemBackend, got %T", backend)
	}
	return backend
}

func BenchmarkDynamoDB_ScanPageLimit25_Memory_0Items(b *testing.B) {
	benchmarkScanPageLimit25(b, newMemItemBackend(), 0)
}

func BenchmarkDynamoDB_ScanPageLimit25_Memory_2000Items(b *testing.B) {
	benchmarkScanPageLimit25(b, newMemItemBackend(), 2000)
}

func BenchmarkDynamoDB_ScanPageLimit25_Memory_8000Items(b *testing.B) {
	benchmarkScanPageLimit25(b, newMemItemBackend(), 8000)
}

// SQL preload sizes are smaller than the memory backend's: itemBackend.put
// has no bulk-insert path (one INSERT per call, matching the production
// write path exactly), so preloading thousands of rows one at a time through
// modernc.org/sqlite is itself slow — that setup cost is deliberately not
// counted (b.ResetTimer runs after preload), but it still has to complete
// before the timed loop starts. 1500 items is enough to demonstrate the flat
// vs. table-size property the SQL row-value query provides.
func BenchmarkDynamoDB_ScanPageLimit25_SQL_0Items(b *testing.B) {
	benchmarkScanPageLimit25(b, newSQLItemBackendForBench(b), 0)
}

func BenchmarkDynamoDB_ScanPageLimit25_SQL_500Items(b *testing.B) {
	benchmarkScanPageLimit25(b, newSQLItemBackendForBench(b), 500)
}

func BenchmarkDynamoDB_ScanPageLimit25_SQL_1500Items(b *testing.B) {
	benchmarkScanPageLimit25(b, newSQLItemBackendForBench(b), 1500)
}
