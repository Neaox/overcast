package state_test

import (
	"context"
	"testing"

	"github.com/Neaox/overcast/internal/state"
)

func TestNamespacedStore_RoutesToOverrideStore(t *testing.T) {
	// Given a namespaced store with SQS routed to a dedicated store.
	defaultStore := state.NewMemoryStore()
	sqsStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": sqsStore,
	})

	ctx := context.Background()

	// When we write via the namespaced store using an SQS namespace…
	if err := ns.Set(ctx, "sqs:queues", "q1", `{"name":"q1"}`); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Then the value is readable through the namespaced store.
	got, found, err := ns.Get(ctx, "sqs:queues", "q1")
	if err != nil || !found {
		t.Fatalf("Get via namespaced: err=%v found=%v", err, found)
	}
	if got != `{"name":"q1"}` {
		t.Errorf("got %q, want %q", got, `{"name":"q1"}`)
	}

	// And the value landed in the SQS-specific store, not the default.
	got2, found2, _ := sqsStore.Get(ctx, "sqs:queues", "q1")
	if !found2 || got2 != `{"name":"q1"}` {
		t.Errorf("sqsStore: found=%v got=%q", found2, got2)
	}
	_, foundDefault, _ := defaultStore.Get(ctx, "sqs:queues", "q1")
	if foundDefault {
		t.Error("value should NOT be in defaultStore")
	}
}

func TestNamespacedStore_FallsBackToDefault(t *testing.T) {
	// Given a namespaced store with only SQS overridden.
	defaultStore := state.NewMemoryStore()
	sqsStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": sqsStore,
	})

	ctx := context.Background()

	// When we write using an S3 namespace (no override)…
	if err := ns.Set(ctx, "s3:buckets", "b1", `{"name":"b1"}`); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Then the value lands in the default store.
	got, found, _ := defaultStore.Get(ctx, "s3:buckets", "b1")
	if !found || got != `{"name":"b1"}` {
		t.Errorf("defaultStore: found=%v got=%q", found, got)
	}
	_, foundSQS, _ := sqsStore.Get(ctx, "s3:buckets", "b1")
	if foundSQS {
		t.Error("value should NOT be in sqsStore")
	}
}

func TestNamespacedStore_NoColonFallsBackToDefault(t *testing.T) {
	// Given a namespace without a colon separator.
	defaultStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": state.NewMemoryStore(),
	})

	ctx := context.Background()

	if err := ns.Set(ctx, "globalns", "k1", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, found, _ := defaultStore.Get(ctx, "globalns", "k1")
	if !found || got != "v1" {
		t.Errorf("defaultStore: found=%v got=%q", found, got)
	}
}

func TestNamespacedStore_ListAndScan(t *testing.T) {
	defaultStore := state.NewMemoryStore()
	sqsStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": sqsStore,
	})

	ctx := context.Background()

	ns.Set(ctx, "sqs:queues", "q1", "v1")
	ns.Set(ctx, "sqs:queues", "q2", "v2")
	ns.Set(ctx, "s3:buckets", "b1", "v3")

	// List SQS → routed store.
	keys, err := ns.List(ctx, "sqs:queues", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("List sqs: expected 2 keys, got %d", len(keys))
	}

	// Scan SQS → routed store.
	kvs, err := ns.Scan(ctx, "sqs:queues", "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(kvs) != 2 {
		t.Errorf("Scan sqs: expected 2 KVs, got %d", len(kvs))
	}

	// List S3 → default store.
	keys, err = ns.List(ctx, "s3:buckets", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("List s3: expected 1 key, got %d", len(keys))
	}
}

func TestNamespacedStore_Delete(t *testing.T) {
	sqsStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(state.NewMemoryStore(), map[string]state.Store{
		"sqs": sqsStore,
	})

	ctx := context.Background()

	ns.Set(ctx, "sqs:queues", "q1", "v1")
	if err := ns.Delete(ctx, "sqs:queues", "q1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, found, _ := sqsStore.Get(ctx, "sqs:queues", "q1")
	if found {
		t.Error("key should be deleted from sqsStore")
	}
}

func TestNamespacedStore_CloseDeduplicates(t *testing.T) {
	// Given a store shared by two route entries.
	shared := state.NewMemoryStore()
	ns := state.NewNamespacedStore(shared, map[string]state.Store{
		"sqs": shared,
		"sns": shared,
	})

	// Close must not panic from double-closing the same store.
	if err := ns.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
