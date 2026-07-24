package state_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/state"
)

// fakeSQLiteStore is a minimal state.Store that also implements
// state.SQLiteDBProvider, standing in for *state.SQLiteStore / *state.HybridStore
// without pulling in real SQLite machinery for these unit tests.
type fakeSQLiteStore struct {
	*state.MemoryStore
}

func newFakeSQLiteStore() *fakeSQLiteStore {
	return &fakeSQLiteStore{MemoryStore: state.NewMemoryStore()}
}

func (f *fakeSQLiteStore) DB() *sql.DB { return nil }

// blockingReadyStore is a state.Store that also implements state.ReadyAwaiter,
// blocking WaitReady until the ready channel is closed or ctx is cancelled.
type blockingReadyStore struct {
	*state.MemoryStore
	ready chan struct{}
}

func newBlockingReadyStore() *blockingReadyStore {
	return &blockingReadyStore{MemoryStore: state.NewMemoryStore(), ready: make(chan struct{})}
}

func (b *blockingReadyStore) WaitReady(ctx context.Context) error {
	select {
	case <-b.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// notReadyStore is a state.Store that also implements state.NotReadyReporter
// with a directly settable return value, standing in for a real store's
// migration-in-progress window without any real SQLite timing.
type notReadyStore struct {
	*state.MemoryStore
	notReady bool
}

func newNotReadyStore(notReady bool) *notReadyStore {
	return &notReadyStore{MemoryStore: state.NewMemoryStore(), notReady: notReady}
}

func (n *notReadyStore) NotReady() bool { return n.notReady }

func TestNamespacedStore_NotReady_TrueIfAnyUnderlyingStoreNotReady(t *testing.T) {
	defaultStore := newNotReadyStore(false)
	sqsStore := newNotReadyStore(true) // still migrating
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{"sqs": sqsStore})

	if !ns.NotReady() {
		t.Fatal("expected NotReady() = true when one underlying store is still migrating")
	}
}

func TestNamespacedStore_NotReady_FalseWhenAllUnderlyingStoresReady(t *testing.T) {
	defaultStore := newNotReadyStore(false)
	sqsStore := newNotReadyStore(false)
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{"sqs": sqsStore})

	if ns.NotReady() {
		t.Fatal("expected NotReady() = false when every underlying store is ready")
	}
}

func TestNamespacedStore_NotReady_FalseForStoresWithoutTheInterface(t *testing.T) {
	// state.MemoryStore doesn't implement NotReadyReporter at all — the
	// same "absence means always ready" convention as ReadyAwaiter.
	ns := state.NewNamespacedStore(state.NewMemoryStore(), map[string]state.Store{
		"s3": state.NewMemoryStore(),
	})
	if ns.NotReady() {
		t.Fatal("expected NotReady() = false for underlying stores that never implement NotReadyReporter")
	}
}

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

// TestNamespacedStore_ColonlessNamespaceRoutesToOverride is the regression
// test for the colonless-namespace storage-override bug: services such as
// ssm pass their bare service name as the namespace with no colon at all
// (namespace == "ssm"), so storeFor's old "return defaultStore whenever
// there's no colon" fallthrough meant OVERCAST_STATE_SSM (and the same for
// kms, stepfunctions, appsync, cloudfront) could never route to its
// override store. storeFor must now match a colonless namespace by its
// whole name, exactly like the prefix-before-colon rule does for namespaced
// keys — while still refusing to prefix-match unrelated colonless names.
func TestNamespacedStore_ColonlessNamespaceRoutesToOverride(t *testing.T) {
	defaultStore := state.NewMemoryStore()
	ssmStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"ssm": ssmStore,
	})

	ctx := context.Background()

	// Set/Get on the bare colonless namespace "ssm" must hit the override.
	if err := ns.Set(ctx, "ssm", "k1", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, found, err := ns.Get(ctx, "ssm", "k1")
	if err != nil || !found || got != "v1" {
		t.Fatalf("Get via namespaced: err=%v found=%v got=%q", err, found, got)
	}
	if got2, found2, _ := ssmStore.Get(ctx, "ssm", "k1"); !found2 || got2 != "v1" {
		t.Errorf("value should be in ssmStore: found=%v got=%q", found2, got2)
	}
	if _, foundDefault, _ := defaultStore.Get(ctx, "ssm", "k1"); foundDefault {
		t.Error("value should NOT be in defaultStore")
	}

	// List/Scan/ScanPage/Delete/DeletePrefix on "ssm" must also hit the
	// override store.
	if err := ns.Set(ctx, "ssm", "k2", "v2"); err != nil {
		t.Fatalf("Set k2: %v", err)
	}
	keys, err := ns.List(ctx, "ssm", "")
	if err != nil || len(keys) != 2 {
		t.Fatalf("List: err=%v keys=%v, want 2 keys", err, keys)
	}
	kvs, err := ns.Scan(ctx, "ssm", "")
	if err != nil || len(kvs) != 2 {
		t.Fatalf("Scan: err=%v kvs=%v, want 2 KVs", err, kvs)
	}
	page, _, err := ns.ScanPage(ctx, "ssm", "", "", 10)
	if err != nil || len(page) != 2 {
		t.Fatalf("ScanPage: err=%v page=%v, want 2 KVs", err, page)
	}
	if err := ns.DeletePrefix(ctx, "ssm", "k"); err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	if remaining, err := ssmStore.List(ctx, "ssm", ""); err != nil || len(remaining) != 0 {
		t.Fatalf("DeletePrefix should have cleared ssmStore, remaining=%v err=%v", remaining, err)
	}
	if err := ns.Set(ctx, "ssm", "k3", "v3"); err != nil {
		t.Fatalf("Set k3: %v", err)
	}
	if err := ns.Delete(ctx, "ssm", "k3"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, _ := ssmStore.Get(ctx, "ssm", "k3"); found {
		t.Error("key should be deleted from ssmStore")
	}

	// The colon form "ssm:sub" must route to the same override (prefix rule).
	if err := ns.Set(ctx, "ssm:sub", "k4", "v4"); err != nil {
		t.Fatalf("Set (colon form): %v", err)
	}
	if got4, found4, _ := ssmStore.Get(ctx, "ssm:sub", "k4"); !found4 || got4 != "v4" {
		t.Errorf("colon-form namespace should route to ssmStore: found=%v got=%q", found4, got4)
	}

	// A different colonless namespace that merely shares a prefix with "ssm"
	// ("ssmother") must NOT accidentally prefix-match the "ssm" route — it
	// has no route of its own, so it must fall through to the default store.
	if err := ns.Set(ctx, "ssmother", "k5", "v5"); err != nil {
		t.Fatalf("Set (ssmother): %v", err)
	}
	if _, foundSSM, _ := ssmStore.Get(ctx, "ssmother", "k5"); foundSSM {
		t.Error("\"ssmother\" must not route to the \"ssm\" override store")
	}
	if got5, foundDefault, _ := defaultStore.Get(ctx, "ssmother", "k5"); !foundDefault || got5 != "v5" {
		t.Errorf("\"ssmother\" should route to defaultStore: found=%v got=%q", foundDefault, got5)
	}
}

// TestNamespacedStore_NoRouteFallsBackToDefault covers the three
// no-matching-route shapes that must keep falling through to the default
// store after the colonless-namespace fix: a colon namespace whose prefix
// has no route, a colonless namespace whose whole name has no route, and
// the empty namespace.
func TestNamespacedStore_NoRouteFallsBackToDefault(t *testing.T) {
	defaultStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": state.NewMemoryStore(),
	})

	ctx := context.Background()

	cases := []struct {
		name      string
		namespace string
	}{
		{"colon namespace, no route", "nosuch:x"},
		{"colonless namespace, no route", "nosuch"},
		{"empty namespace", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "k-" + tc.name
			if err := ns.Set(ctx, tc.namespace, key, "v"); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, found, err := defaultStore.Get(ctx, tc.namespace, key)
			if err != nil || !found || got != "v" {
				t.Fatalf("defaultStore.Get: err=%v found=%v got=%q", err, found, got)
			}
		})
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

// TestNamespacedStore_ScanPage proves ScanPage routes to the correct
// underlying store by service prefix, exactly like List/Scan above, and that
// the routed store's own pagination contract still holds through the
// wrapper.
func TestNamespacedStore_ScanPage(t *testing.T) {
	defaultStore := state.NewMemoryStore()
	sqsStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": sqsStore,
	})
	assertScanPagePaginatesFullRange(t, ns, "sqs:queues", "queue/", 17, 4)

	// A namespace with no override still routes through to the default
	// store's ScanPage.
	assertScanPagePaginatesFullRange(t, ns, "s3:buckets", "bucket/", 6, 2)

	// Sanity: routing actually went to distinct stores, not just the default
	// for both.
	ctx := context.Background()
	sqsKeys, err := sqsStore.List(ctx, "sqs:queues", "")
	if err != nil {
		t.Fatalf("List (routed store): %v", err)
	}
	if len(sqsKeys) != 17 {
		t.Fatalf("expected the sqs override store to hold 17 keys directly, got %d", len(sqsKeys))
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

// TestUnwrap_UnrelatedOverrideStillResolvesSQLiteBackend is the regression
// test for the 1.1 storage-plan bug: an OVERCAST_STATE_<SVC> override on an
// UNRELATED service must not erase the SQLiteDBProvider capability of the
// default (dynamodb) store.
func TestUnwrap_UnrelatedOverrideStillResolvesSQLiteBackend(t *testing.T) {
	// Given a namespaced store whose default is SQLite-backed and only an
	// unrelated service ("s3") has a memory override.
	sqliteDefault := newFakeSQLiteStore()
	ns := state.NewNamespacedStore(sqliteDefault, map[string]state.Store{
		"s3": state.NewMemoryStore(),
	})

	// When DynamoDB resolves its backing store through Unwrap...
	resolved := state.Unwrap(ns, "dynamodb")

	// Then it still gets a store implementing SQLiteDBProvider — the
	// dedicated-table backend selection must not silently downgrade to
	// memory-only just because s3 was routed elsewhere.
	if _, ok := resolved.(state.SQLiteDBProvider); !ok {
		t.Fatalf("Unwrap(ns, \"dynamodb\") = %T, want a state.SQLiteDBProvider", resolved)
	}
	if resolved != state.Store(sqliteDefault) {
		t.Fatalf("Unwrap(ns, \"dynamodb\") should resolve to the default store")
	}
}

func TestUnwrap_MatchingOverrideResolvesToRoutedStore(t *testing.T) {
	// Given dynamodb itself has an override configured.
	dynamoStore := newFakeSQLiteStore()
	ns := state.NewNamespacedStore(state.NewMemoryStore(), map[string]state.Store{
		"dynamodb": dynamoStore,
	})

	resolved := state.Unwrap(ns, "dynamodb")
	if resolved != state.Store(dynamoStore) {
		t.Fatalf("Unwrap should resolve to the dynamodb-routed store, got %T", resolved)
	}
}

func TestUnwrap_NonNamespacedStoreReturnedUnchanged(t *testing.T) {
	// A plain (non-wrapped) store must pass through Unwrap unchanged.
	plain := newFakeSQLiteStore()
	resolved := state.Unwrap(plain, "dynamodb")
	if resolved != state.Store(plain) {
		t.Fatalf("Unwrap should return a non-namespaced store unchanged, got %T", resolved)
	}
}

func TestNamespacedStore_StoreFor(t *testing.T) {
	defaultStore := state.NewMemoryStore()
	sqsStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": sqsStore,
	})

	if got := ns.StoreFor("sqs"); got != state.Store(sqsStore) {
		t.Errorf("StoreFor(\"sqs\") = %T, want the sqs store", got)
	}
	if got := ns.StoreFor("s3"); got != state.Store(defaultStore) {
		t.Errorf("StoreFor(\"s3\") = %T, want the default store", got)
	}
}

func TestNamespacedStore_WaitReady_blocksUntilUnderlyingStoresAreReady(t *testing.T) {
	// Given a namespaced store whose default backend has an async init phase.
	blocking := newBlockingReadyStore()
	ns := state.NewNamespacedStore(blocking, map[string]state.Store{
		"sqs": state.NewMemoryStore(), // ready immediately — doesn't implement ReadyAwaiter
	})

	done := make(chan error, 1)
	go func() { done <- ns.WaitReady(context.Background()) }()

	// Then WaitReady must not return before the underlying store signals ready.
	select {
	case err := <-done:
		t.Fatalf("WaitReady returned early (err=%v) before underlying store was ready", err)
	case <-time.After(75 * time.Millisecond):
	}

	close(blocking.ready)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitReady: unexpected error %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitReady did not return after underlying store became ready")
	}
}

func TestNamespacedStore_WaitReady_respectsContextCancellation(t *testing.T) {
	blocking := newBlockingReadyStore() // never becomes ready
	ns := state.NewNamespacedStore(blocking, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ns.WaitReady(ctx) }()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitReady error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitReady did not return after context cancellation")
	}
}

func TestNamespacedStore_UnderlyingStores_deduplicates(t *testing.T) {
	shared := state.NewMemoryStore()
	other := state.NewMemoryStore()
	ns := state.NewNamespacedStore(shared, map[string]state.Store{
		"sqs": shared,
		"sns": other,
	})

	stores := ns.UnderlyingStores()
	if len(stores) != 2 {
		t.Fatalf("UnderlyingStores() = %d stores, want 2 (deduplicated)", len(stores))
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
