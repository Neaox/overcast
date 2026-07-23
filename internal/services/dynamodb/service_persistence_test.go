package dynamodb

import (
	"context"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// newWrappedHybridStore builds a *state.NamespacedStore backed by a real
// *state.HybridStore rooted at dir, with an override for an UNRELATED
// service ("s3"). This mirrors how cmd/overcast/cmd_serve.go wires the store
// when any single OVERCAST_STATE_<SVC> override is configured — DynamoDB
// itself is never routed away from SQLite, but the whole store still gets
// wrapped in *state.NamespacedStore.
func newWrappedHybridStore(t *testing.T, dir string) *state.NamespacedStore {
	t.Helper()
	hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() {
		if err := hybrid.Close(); err != nil {
			t.Logf("hybrid.Close: %v", err)
		}
	})
	return state.NewNamespacedStore(hybrid, map[string]state.Store{
		"s3": state.NewMemoryStore(),
	})
}

// TestNewItemBackendFor_SurvivesRestart_WithUnrelatedNamespacedOverride is the
// storage-plan 1.1 regression test: an OVERCAST_STATE_S3 override must not
// silently downgrade DynamoDB item persistence to memory-only.
//
// Without state.Unwrap in New()/newItemBackendFor's call site, the item
// backend selection type-asserts the *state.NamespacedStore directly against
// state.SQLiteDBProvider, which always fails (NamespacedStore doesn't
// implement it), so DynamoDB silently falls back to newMemItemBackend() and
// this test fails: the item does not survive the simulated restart.
func TestNewItemBackendFor_SurvivesRestart_WithUnrelatedNamespacedOverride(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// "Process 1": open the store, resolve the item backend exactly like
	// Service.New does, and write an item.
	store1 := newWrappedHybridStore(t, dir)
	backend1 := newItemBackendFor(state.Unwrap(store1, serviceName))
	if _, ok := backend1.(*sqlItemBackend); !ok {
		t.Fatalf("expected sqlItemBackend when default store is SQLite-backed, got %T", backend1)
	}

	item := Item{"pk": attrValue{"S": "artist-1"}, "title": attrValue{"S": "Overcast"}}
	if err := backend1.put(ctx, "Music", "artist-1", "", item); err != nil {
		t.Fatalf("put: %v", err)
	}

	// "Restart": a brand-new store + item backend pointed at the same data
	// directory, exactly as would happen after `overcast serve` restarts.
	store2 := newWrappedHybridStore(t, dir)
	backend2 := newItemBackendFor(state.Unwrap(store2, serviceName))

	got, found, err := backend2.get(ctx, "Music", "artist-1", "")
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if !found {
		t.Fatal("item did not survive restart — DynamoDB persistence was silently lost under an unrelated store override")
	}
	if got["title"]["S"] != "Overcast" {
		t.Fatalf("unexpected item after restart: %#v", got)
	}
}

// TestNewItemBackendFor_WithoutUnwrap_FallsBackToMemory documents the bug
// this fix closes: passing an unresolved *state.NamespacedStore straight to
// newItemBackendFor silently selects the in-memory backend even though the
// default store is SQLite-backed.
func TestNewItemBackendFor_WithoutUnwrap_FallsBackToMemory(t *testing.T) {
	dir := t.TempDir()
	store := newWrappedHybridStore(t, dir)

	backend := newItemBackendFor(store) // deliberately NOT unwrapped
	if _, ok := backend.(*memItemBackend); !ok {
		t.Fatalf("expected memItemBackend when passing a raw NamespacedStore (pre-fix behavior), got %T", backend)
	}
}
