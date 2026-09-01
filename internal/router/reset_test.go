package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// resetCountingDynamoDebugProvider and resetCountingLogsDebugProvider wrap
// the fakeDynamoDebugProvider/fakeLogsDebugProvider fixtures declared in
// debug_test.go, counting DebugResetState calls for the tests in this file.

type resetCountingDynamoDebugProvider struct {
	fakeDynamoDebugProvider
	resets int
}

func (p *resetCountingDynamoDebugProvider) DebugResetState(context.Context) error {
	p.resets++
	return nil
}

type resetCountingLogsDebugProvider struct {
	fakeLogsDebugProvider
	resets int
}

func (p *resetCountingLogsDebugProvider) DebugResetState(context.Context) error {
	p.resets++
	return nil
}

func TestResetAllNamespaces_deletesAppSyncAndAPIGatewayState(t *testing.T) {
	// Given: AppSync and API Gateway state exists.
	store := state.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "appsync", "us-east-1:ds:api-id:NamespaceDS", `{"name":"NamespaceDS"}`); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "apigw:restapis", "us-east-1:api-id", `{"id":"api-id"}`); err != nil {
		t.Fatal(err)
	}

	// When: reset-all deletes known namespaces.
	resetAllNamespaces(ctx, store)

	// Then: both services are actually cleared.
	if _, found, err := store.Get(ctx, "appsync", "us-east-1:ds:api-id:NamespaceDS"); err != nil || found {
		t.Fatalf("expected appsync state deleted, found=%v err=%v", found, err)
	}
	if _, found, err := store.Get(ctx, "apigw:restapis", "us-east-1:api-id"); err != nil || found {
		t.Fatalf("expected apigw state deleted, found=%v err=%v", found, err)
	}
}

func TestResetService_dynamodbClearsVirtualItems(t *testing.T) {
	// Given: DynamoDB has table metadata in state.Store and item data in its virtual backend.
	store := state.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "dynamodb:tables", "us-east-1/Music", `{"TableName":"Music"}`); err != nil {
		t.Fatal(err)
	}
	dynamo := &resetCountingDynamoDebugProvider{}

	// When: the DynamoDB per-service reset endpoint runs.
	req := httptest.NewRequest(http.MethodPost, "/_overcast/reset/dynamodb", nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", "dynamodb")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	resetServiceHandler(store, []DebugStateProvider{dynamo}).ServeHTTP(rec, req)

	// Then: store-backed metadata and virtual item state are both cleared.
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, body)
	}
	if dynamo.resets != 1 {
		t.Fatalf("expected one DynamoDB virtual reset, got %d", dynamo.resets)
	}
	if _, found, err := store.Get(ctx, "dynamodb:tables", "us-east-1/Music"); err != nil || found {
		t.Fatalf("expected dynamodb table state deleted, found=%v err=%v", found, err)
	}
}

func TestReset_clearsStateAcrossNamespacedStoreOverrides(t *testing.T) {
	// Given a namespaced store where an unrelated service (s3) is routed to a
	// dedicated store, distinct from the default store used by everything else.
	defaultStore := state.NewMemoryStore()
	s3Store := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"s3": s3Store,
	})
	ctx := context.Background()
	if err := ns.Set(ctx, "sqs:queues", "q1", `{"name":"q1"}`); err != nil {
		t.Fatal(err)
	}
	if err := ns.Set(ctx, "s3:buckets", "b1", `{"name":"b1"}`); err != nil {
		t.Fatal(err)
	}

	// When the global reset endpoint is invoked with the wrapped store...
	req := httptest.NewRequest(http.MethodPost, "/_overcast/reset", nil)
	rec := httptest.NewRecorder()
	resetHandler(ns, nil).ServeHTTP(rec, req)

	// Then both the default store's data and the routed store's data are
	// cleared — reset must not silently miss data because the top-level
	// store is a *state.NamespacedStore rather than a bare *state.MemoryStore.
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, body)
	}
	if _, found, _ := defaultStore.Get(ctx, "sqs:queues", "q1"); found {
		t.Error("expected default store state cleared after reset")
	}
	if _, found, _ := s3Store.Get(ctx, "s3:buckets", "b1"); found {
		t.Error("expected routed (s3) store state cleared after reset")
	}
}

func TestResetStore_recursesIntoNamespacedStoreUnderlyingStores(t *testing.T) {
	// Direct unit test of the resetStore helper used by resetHandler — proves
	// it doesn't rely on a concrete `*state.MemoryStore` assertion against the
	// (possibly wrapped) top-level store.
	defaultStore := state.NewMemoryStore()
	sqsStore := state.NewMemoryStore()
	ns := state.NewNamespacedStore(defaultStore, map[string]state.Store{
		"sqs": sqsStore,
	})
	ctx := context.Background()
	ns.Set(ctx, "sqs:queues", "q1", "v1")
	ns.Set(ctx, "appsync", "ds1", "v2")

	resetStore(ctx, ns)

	if _, found, _ := sqsStore.Get(ctx, "sqs:queues", "q1"); found {
		t.Error("expected sqs-routed store cleared")
	}
	if _, found, _ := defaultStore.Get(ctx, "appsync", "ds1"); found {
		t.Error("expected default store cleared")
	}
}

// TestResetService_logsClearsVirtualEvents mirrors
// TestResetService_dynamodbClearsVirtualItems for the "logs" service
// prefix, proving /_overcast/reset/logs clears the dedicated event backend too.
func TestResetService_logsClearsVirtualEvents(t *testing.T) {
	// Given: CloudWatch Logs has group metadata in state.Store and event data
	// in its virtual backend.
	store := state.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "logs:groups", "us-east-1/my-group", `{"name":"my-group"}`); err != nil {
		t.Fatal(err)
	}
	logsProvider := &resetCountingLogsDebugProvider{}

	// When: the logs per-service reset endpoint runs.
	req := httptest.NewRequest(http.MethodPost, "/_overcast/reset/logs", nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", "logs")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	resetServiceHandler(store, []DebugStateProvider{logsProvider}).ServeHTTP(rec, req)

	// Then: store-backed metadata and virtual event state are both cleared.
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, body)
	}
	if logsProvider.resets != 1 {
		t.Fatalf("expected one logs virtual reset, got %d", logsProvider.resets)
	}
	if _, found, err := store.Get(ctx, "logs:groups", "us-east-1/my-group"); err != nil || found {
		t.Fatalf("expected logs group state deleted, found=%v err=%v", found, err)
	}
}

// TestReset_clearsMultipleProviders proves /_overcast/reset clears every
// registered DebugStateProvider, not just one hardcoded service — the
// generalization this test file's other multi-provider tests underwrite.
func TestReset_clearsMultipleProviders(t *testing.T) {
	store := state.NewMemoryStore()
	dynamo := &resetCountingDynamoDebugProvider{}
	logsProvider := &resetCountingLogsDebugProvider{}

	req := httptest.NewRequest(http.MethodPost, "/_overcast/reset", nil)
	rec := httptest.NewRecorder()
	resetHandler(store, []DebugStateProvider{dynamo, logsProvider}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if dynamo.resets != 1 {
		t.Fatalf("expected DynamoDB provider reset once, got %d", dynamo.resets)
	}
	if logsProvider.resets != 1 {
		t.Fatalf("expected logs provider reset once, got %d", logsProvider.resets)
	}
}

// TestResetService_dynamodbUnaffectedByOtherProviders is a regression test
// for the debugDynamoDBProvider → []DebugStateProvider generalization:
// resetting "dynamodb" must reset only the DynamoDB provider, even when other
// providers (e.g. logs) are also registered.
func TestResetService_dynamodbUnaffectedByOtherProviders(t *testing.T) {
	store := state.NewMemoryStore()
	dynamo := &resetCountingDynamoDebugProvider{}
	logsProvider := &resetCountingLogsDebugProvider{}

	req := httptest.NewRequest(http.MethodPost, "/_overcast/reset/dynamodb", nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", "dynamodb")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	resetServiceHandler(store, []DebugStateProvider{dynamo, logsProvider}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if dynamo.resets != 1 {
		t.Fatalf("expected DynamoDB provider reset once, got %d", dynamo.resets)
	}
	if logsProvider.resets != 0 {
		t.Fatalf("expected logs provider untouched, got %d resets", logsProvider.resets)
	}
}

// ---- Always-on wiring (full router, cfg.Debug=false) ----------------------
//
// The tests above exercise the handlers directly (matching this package's
// existing style for debug.go). These two go through the real router.New()
// wiring, which is what actually proves the design decision this file
// implements: reset moved OUT of the debug gate. A handler-level test alone
// cannot show that — it would pass whether or not the route were still
// nested under a cfg.Debug-gated chi sub-router.

func newResetRouterTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := &config.Config{
		Host:      "127.0.0.1",
		Port:      0,
		Region:    "us-east-1",
		AccountID: "000000000000",
		State:     config.StateBackendMemory,
		LogLevel:  "error",

		ShutdownTimeout: 0,
		SigV4Validate:   false,
		Debug:           false, // the point of this test: reset must work anyway.
	}
	store := state.NewMemoryStore()
	handler, preShutdown, cleanup, _ := New(cfg, store, zap.NewNop(), clock.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		preShutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cleanup(ctx)
		srv.Close()
	})
	return srv
}

func TestRouter_resetWorksWithDebugFalse(t *testing.T) {
	srv := newResetRouterTestServer(t)

	resp, err := http.Post(srv.URL+"/_overcast/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /_overcast/reset with cfg.Debug=false: status = %d, want 200", resp.StatusCode)
	}

	resp2, err := http.Post(srv.URL+"/_overcast/reset/s3", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("POST /_overcast/reset/s3 with cfg.Debug=false: status = %d, want 200", resp2.StatusCode)
	}
}

func TestRouter_debugResetPathIsGone(t *testing.T) {
	srv := newResetRouterTestServer(t)

	resp, err := http.Post(srv.URL+"/_overcast/debug/reset", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// No route claims "/_overcast/debug/reset" any more (the debug subrouter
	// isn't even mounted with cfg.Debug=false), so the request falls all the
	// way through to S3's private bucket/object router (bucket="_overcast",
	// key="debug/reset") — the same generic fallback any unclaimed
	// /_overcast/* path hits. Depending on host-addressing config that
	// fallback answers 404 (NoSuchBucket/NoSuchKey) or 405
	// (MethodNotAllowed) — never 200 "reset". That is what "gone, not
	// aliased" looks like at the HTTP layer; see
	// TestRouter_resetWorksWithDebugFalse for proof the new path works.
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("POST /_overcast/debug/reset: status = 200, want anything but — the debug-gated path must be gone, not still answering as reset")
	}
}
