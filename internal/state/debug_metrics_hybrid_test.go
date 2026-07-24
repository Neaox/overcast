//go:build !nosqlite

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// TestDebugMetricsSnapshot_namespacedStoreReturnsOneEntryPerUnderlyingStore
// proves the deliberate design choice documented on
// state.DebugMetricsSnapshot: a *state.NamespacedStore wrapping two distinct
// reporting stores yields two separate DebugMetrics entries rather than one
// merged value (unlike PersistentHealthSnapshot's numeric merge — flush
// history and seed duration don't combine meaningfully across backends).
func TestDebugMetricsSnapshot_namespacedStoreReturnsOneEntryPerUnderlyingStore(t *testing.T) {
	defaultStore, err := state.NewHybridStore(t.TempDir(), 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore (default): %v", err)
	}
	defer defaultStore.Close()

	overrideStore, err := state.NewHybridStore(t.TempDir(), 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore (override): %v", err)
	}
	defer overrideStore.Close()

	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": overrideStore,
	})

	ctx := context.Background()
	if err := defaultStore.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady (default): %v", err)
	}
	if err := overrideStore.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady (override): %v", err)
	}

	snapshots, ok := state.DebugMetricsSnapshot(ctx, ns, state.DebugMetricsOptions{})
	if !ok {
		t.Fatal("expected ok=true for a NamespacedStore wrapping two HybridStores")
	}
	if len(snapshots) != 2 {
		t.Fatalf("expected one DebugMetrics entry per distinct underlying store, got %d: %+v", len(snapshots), snapshots)
	}
	for _, m := range snapshots {
		if m.Mode != "hybrid" {
			t.Errorf("expected mode %q, got %q", "hybrid", m.Mode)
		}
	}
}

// TestHybridStore_DebugMetrics_namespaceRowCountsOptIn proves
// NamespaceRowCounts is only populated when explicitly requested, and is
// accurate for a TierHot namespace when it is.
func TestHybridStore_DebugMetrics_namespaceRowCountsOptIn(t *testing.T) {
	store, err := state.NewHybridStore(t.TempDir(), 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if err := store.Set(ctx, "sqs:queues", "us-east-1/my-queue", `{"name":"my-queue"}`); err != nil {
		t.Fatal(err)
	}

	withoutCounts, ok := state.DebugMetricsSnapshot(ctx, store, state.DebugMetricsOptions{})
	if !ok || len(withoutCounts) != 1 {
		t.Fatalf("expected one snapshot, got ok=%v len=%d", ok, len(withoutCounts))
	}
	if withoutCounts[0].NamespaceRowCounts != nil {
		t.Fatalf("expected nil NamespaceRowCounts when not requested, got %+v", withoutCounts[0].NamespaceRowCounts)
	}

	withCounts, ok := state.DebugMetricsSnapshot(ctx, store, state.DebugMetricsOptions{IncludeNamespaceRowCounts: true})
	if !ok || len(withCounts) != 1 {
		t.Fatalf("expected one snapshot, got ok=%v len=%d", ok, len(withCounts))
	}
	if got := withCounts[0].NamespaceRowCounts["sqs:queues"]; got != 1 {
		t.Fatalf("expected sqs:queues row count 1, got %d (counts=%+v)", got, withCounts[0].NamespaceRowCounts)
	}
}
