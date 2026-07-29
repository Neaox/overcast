//go:build !nosqlite

package logs

// The backend-selection half of event_backend_test.go, split out because both
// tests assert that newEventBackendFor picks *sqlEventBackend — a choice that
// only exists in a build with SQLite compiled in. Under -tags nosqlite,
// state.NewHybridStore is a stub that always errors
// (internal/state/sqlite_hybrid_nosqlite.go) and every store is memory-backed,
// so there is nothing here left to assert. The parity tests in
// event_backend_test.go stay untagged and degrade to memory-only instead —
// see newTestBackends and tests/AGENTS.md § Build-tag-sensitive tests.

import (
	"context"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// TestNewEventBackendFor_SurvivesRestart_WithUnrelatedNamespacedOverride
// mirrors DynamoDB's identically-named test (item_store.go /
// service_persistence_test.go): an OVERCAST_STATE_<OTHER> override wrapping
// the store in *state.NamespacedStore must not silently downgrade Logs event
// persistence to memory-only (storage-plan.md 1.1's bug class).
func TestNewEventBackendFor_SurvivesRestart_WithUnrelatedNamespacedOverride(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	newWrapped := func() *state.NamespacedStore {
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

	store1 := newWrapped()
	backend1 := newEventBackendFor(state.Unwrap(store1, serviceName))
	if _, ok := backend1.(*sqlEventBackend); !ok {
		t.Fatalf("expected sqlEventBackend when default store is SQLite-backed, got %T", backend1)
	}
	if err := backend1.appendEvents(ctx, "us-east-1", "g", "s", []LogEvent{{Timestamp: 1, Message: "hello", IngestionTime: 1}}); err != nil {
		t.Fatalf("appendEvents: %v", err)
	}

	store2 := newWrapped()
	backend2 := newEventBackendFor(state.Unwrap(store2, serviceName))
	got, err := backend2.getEvents(ctx, "us-east-1", "g", "s")
	if err != nil {
		t.Fatalf("getEvents after restart: %v", err)
	}
	if len(got) != 1 || got[0].Message != "hello" {
		t.Fatalf("event did not survive restart — Logs event persistence was silently lost under an unrelated store override, got %+v", got)
	}
}

// TestNewEventBackendFor_WithoutUnwrap_FallsBackToMemory documents the bug
// class this guards against: passing an unresolved *state.NamespacedStore
// straight to newEventBackendFor silently selects the in-memory backend even
// though the default store is SQLite-backed.
func TestNewEventBackendFor_WithoutUnwrap_FallsBackToMemory(t *testing.T) {
	dir := t.TempDir()
	hybrid, err := state.NewHybridStore(dir, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewHybridStore: %v", err)
	}
	t.Cleanup(func() { hybrid.Close() })
	store := state.NewNamespacedStore(hybrid, map[string]state.Store{"s3": state.NewMemoryStore()})

	backend := newEventBackendFor(store) // deliberately NOT unwrapped
	if _, ok := backend.(*memEventBackend); !ok {
		t.Fatalf("expected memEventBackend when passing a raw NamespacedStore (pre-fix behavior), got %T", backend)
	}
}
