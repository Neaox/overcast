package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/state"
	"go.uber.org/zap"
)

// TestKVBackend_SurvivesRestart is the failing-first proof for phase 4 item 2:
// automatic metrics recorded against a WALStore must be readable again after
// the store (and the metrics.Service on top of it) is closed and reopened
// against the same data directory — the property memBucketBackend can never
// offer, and that phases 1-3 explicitly left ungiven for WALStore/nosqlite
// builds (see backend.go's file doc comment history).
func TestKVBackend_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	openStore := func() *state.WALStore {
		wal, err := state.NewWALStore(dir, state.WALOptions{SyncMode: state.WALSyncAlways})
		if err != nil {
			t.Fatalf("NewWALStore: %v", err)
		}
		return wal
	}

	// First process lifetime: record a Lambda-shaped observation and force a
	// flush (as Stop would on real shutdown), then close.
	wal1 := openStore()
	svc1 := NewRecorder(wal1, clock.New(), zap.NewNop())
	if err := svc1.Observe(ctx, Observation{
		Namespace: "AWS/Lambda", Name: "Invocations", Unit: "Count", Value: 1,
		Dimensions: []Dimension{{Name: "FunctionName", Value: "restart-fn"}},
	}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	svc1.Stop(stopCtx) // flushes dirty buckets before returning
	cancel()
	if err := wal1.Close(); err != nil {
		t.Fatalf("wal1.Close: %v", err)
	}

	// Second process lifetime: a fresh WALStore over the same data dir
	// replays the append log, and a fresh metrics.Service over it must see
	// the prior observation without a new Observe call.
	wal2 := openStore()
	t.Cleanup(func() {
		if err := wal2.Close(); err != nil {
			t.Logf("wal2.Close: %v", err)
		}
	})
	svc2 := NewRecorder(wal2, clock.New(), zap.NewNop())
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		svc2.Stop(stopCtx)
	})

	now := time.Now().UTC()
	buckets, err := svc2.QueryRange(ctx, "AWS/Lambda", "Invocations",
		[]Dimension{{Name: "FunctionName", Value: "restart-fn"}},
		now.Add(-time.Hour), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange after restart: %v", err)
	}
	var total float64
	for _, b := range buckets {
		total += b.Count
	}
	if total != 1 {
		t.Fatalf("expected the pre-restart observation to survive (total count 1), got buckets=%+v", buckets)
	}
}
