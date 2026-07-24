package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/state"
)

type fakeDynamoDebugProvider struct{}

func (fakeDynamoDebugProvider) DebugNamespace() string { return "dynamodb:items" }

func (fakeDynamoDebugProvider) DebugStateKeys(context.Context) ([]string, error) {
	return []string{"Music/artist-1/song-1"}, nil
}

func (fakeDynamoDebugProvider) DebugStateValues(context.Context) (map[string]string, error) {
	return map[string]string{"Music/artist-1/song-1": `{"pk":{"S":"artist-1"}}`}, nil
}

func (fakeDynamoDebugProvider) DebugResetState(context.Context) error { return nil }

type resetCountingDynamoDebugProvider struct {
	fakeDynamoDebugProvider
	resets int
}

func (p *resetCountingDynamoDebugProvider) DebugResetState(context.Context) error {
	p.resets++
	return nil
}

// fakeLogsDebugProvider mirrors fakeDynamoDebugProvider for the CloudWatch
// Logs events virtual namespace ("logs:events" — storage-plan.md 2.3).
type fakeLogsDebugProvider struct{}

func (fakeLogsDebugProvider) DebugNamespace() string { return "logs:events" }

func (fakeLogsDebugProvider) DebugStateKeys(context.Context) ([]string, error) {
	return []string{"us-east-1/my-group/my-stream/1700000000000/0"}, nil
}

func (fakeLogsDebugProvider) DebugStateValues(context.Context) (map[string]string, error) {
	return map[string]string{
		"us-east-1/my-group/my-stream/1700000000000/0": `{"timestamp":1700000000000,"message":"hello"}`,
	}, nil
}

func (fakeLogsDebugProvider) DebugResetState(context.Context) error { return nil }

type resetCountingLogsDebugProvider struct {
	fakeLogsDebugProvider
	resets int
}

func (p *resetCountingLogsDebugProvider) DebugResetState(context.Context) error {
	p.resets++
	return nil
}

func TestDebugState_includesAppSyncAndAPIGatewayNamespaces(t *testing.T) {
	// Given: raw state exists for services beyond the original debug allowlist.
	store := state.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "appsync", "us-east-1:ds:api-id:NamespaceDS", `{"name":"NamespaceDS"}`); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, "apigw:restapis", "us-east-1:api-id", `{"id":"api-id"}`); err != nil {
		t.Fatal(err)
	}

	// When: the raw state summary is requested.
	req := httptest.NewRequest(http.MethodGet, "/_debug/state", nil)
	rec := httptest.NewRecorder()
	debugState(store, nil).ServeHTTP(rec, req)

	// Then: AppSync and API Gateway namespaces are visible in the summary.
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, body)
	}
	if !containsDebugBody(body, `"appsync"`) {
		t.Fatalf("expected appsync namespace in debug state summary, got %s", body)
	}
	if !containsDebugBody(body, `"apigw:restapis"`) {
		t.Fatalf("expected apigw namespace in debug state summary, got %s", body)
	}
}

func TestDebugState_includesDynamoDBItemsVirtualNamespace(t *testing.T) {
	// Given: DynamoDB has item data in its dedicated item backend.
	store := state.NewMemoryStore()
	dynamo := fakeDynamoDebugProvider{}
	providers := []DebugStateProvider{dynamo}

	// When: the raw state summary is requested.
	req := httptest.NewRequest(http.MethodGet, "/_debug/state", nil)
	rec := httptest.NewRecorder()
	debugState(store, providers).ServeHTTP(rec, req)

	// Then: DynamoDB items are exposed as a virtual debug namespace.
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, body)
	}
	if !containsDebugBody(body, `"dynamodb:items"`) {
		t.Fatalf("expected dynamodb item namespace in debug state summary, got %s", body)
	}

	// And: fetching that namespace returns raw item JSON values.
	req = httptest.NewRequest(http.MethodGet, "/_debug/state/dynamodb:items", nil)
	rec = httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "dynamodb:items")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugStateNamespace(store, providers).ServeHTTP(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, body)
	}
	if !containsDebugBody(body, `Music/artist-1/song-1`) {
		t.Fatalf("expected dynamodb item key in namespace response, got %s", body)
	}
}

func TestDebugState_includesSQSMessagesNamespace(t *testing.T) {
	// Given: SQS message state exists in the shared store.
	store := state.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "sqs:messages", "us-east-1/orders/msg-1", `{"body":"hello"}`); err != nil {
		t.Fatal(err)
	}

	// When: the raw state summary is requested.
	req := httptest.NewRequest(http.MethodGet, "/_debug/state", nil)
	rec := httptest.NewRecorder()
	debugState(store, nil).ServeHTTP(rec, req)

	// Then: SQS messages are visible through dynamic namespace discovery.
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, body)
	}
	if !containsDebugBody(body, `"sqs:messages"`) {
		t.Fatalf("expected sqs messages namespace in debug state summary, got %s", body)
	}
}

func TestDebugStateNamespace_truncatesLargeValues(t *testing.T) {
	// Given: a namespace has a very large raw string value such as a Lambda layer zip payload.
	store := state.NewMemoryStore()
	ctx := context.Background()
	largeValue := `{"layer_name":"deps","content":"` + strings.Repeat("A", debugStateValuePreviewBytes+1024) + `"}`
	if err := store.Set(ctx, "lambda:layers", "us-east-1/deps:0000000001", largeValue); err != nil {
		t.Fatal(err)
	}

	// When: the raw state namespace is requested.
	req := httptest.NewRequest(http.MethodGet, "/_debug/state/lambda:layers", nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "lambda:layers")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugStateNamespace(store, nil).ServeHTTP(rec, req)

	// Then: the value is capped for UI rendering and marked as truncated.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body debugStateNamespacePage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := body.Values["us-east-1/deps:0000000001"]
	var layer map[string]string
	if err := json.Unmarshal([]byte(got), &layer); err != nil {
		t.Fatalf("expected valid JSON after truncation: %v; body=%q", err, got)
	}
	if layer["layer_name"] != "deps" {
		t.Fatalf("expected metadata to remain readable, got %q", layer["layer_name"])
	}
	if !strings.HasSuffix(layer["content"], debugStateTruncatedSuffix) {
		t.Fatalf("expected truncation marker, got suffix %q", layer["content"][len(layer["content"])-32:])
	}
	if len(layer["content"]) != debugStateValuePreviewBytes+len(debugStateTruncatedSuffix) {
		t.Fatalf("expected capped content length, got %d", len(layer["content"]))
	}
}

func TestDebugStateNamespace_truncatesLargePlainTextValues(t *testing.T) {
	// Given: a namespace has a large non-JSON raw string value.
	store := state.NewMemoryStore()
	ctx := context.Background()
	largeValue := strings.Repeat("A", debugStateValuePreviewBytes+1024)
	if err := store.Set(ctx, "debug:plain", "record", largeValue); err != nil {
		t.Fatal(err)
	}

	// When: the raw state namespace is requested.
	req := httptest.NewRequest(http.MethodGet, "/_debug/state/debug:plain", nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "debug:plain")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugStateNamespace(store, nil).ServeHTTP(rec, req)

	// Then: the plain value is capped for UI rendering and marked as truncated.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body debugStateNamespacePage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := body.Values["record"]
	if !strings.HasSuffix(got, debugStateTruncatedSuffix) {
		t.Fatalf("expected truncation marker, got suffix %q", got[len(got)-32:])
	}
	if len(got) != debugStateValuePreviewBytes+len(debugStateTruncatedSuffix) {
		t.Fatalf("expected capped value length, got %d", len(got))
	}
}

func TestDebugStateNamespaceKey_returnsRawJSONValue(t *testing.T) {
	// Given: a selected raw state value is itself JSON and larger than the UI preview cap.
	store := state.NewMemoryStore()
	ctx := context.Background()
	key := "us-east-1/deps:0000000001"
	largeValue := `{"layer_name":"deps","content":"` + strings.Repeat("A", debugStateValuePreviewBytes+1024) + `"}`
	if err := store.Set(ctx, "lambda:layers", key, largeValue); err != nil {
		t.Fatal(err)
	}

	// When: the selected value is requested directly.
	req := httptest.NewRequest(http.MethodGet, "/_debug/state/lambda:layers?key="+key, nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "lambda:layers")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugStateNamespace(store, nil).ServeHTTP(rec, req)

	// Then: the full raw JSON value is returned without UI truncation.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content-type, got %q", got)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != largeValue {
		t.Fatalf("expected full raw value, got length %d want %d", len(got), len(largeValue))
	}
}

func TestDebugStateNamespaceKey_returnsRawTextValue(t *testing.T) {
	// Given: a selected raw state value is plain text.
	store := state.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "debug:plain", "record", "not json"); err != nil {
		t.Fatal(err)
	}

	// When: the selected value is requested directly.
	req := httptest.NewRequest(http.MethodGet, "/_debug/state/debug:plain?key=record", nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "debug:plain")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugStateNamespace(store, nil).ServeHTTP(rec, req)

	// Then: the value is served as plain text for the browser to display or download.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("expected text content-type, got %q", got)
	}
	if got := rec.Body.String(); got != "not json" {
		t.Fatalf("unexpected body %q", got)
	}
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

func TestDebugResetService_dynamodbClearsVirtualItems(t *testing.T) {
	// Given: DynamoDB has table metadata in state.Store and item data in its virtual backend.
	store := state.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "dynamodb:tables", "us-east-1/Music", `{"TableName":"Music"}`); err != nil {
		t.Fatal(err)
	}
	dynamo := &resetCountingDynamoDebugProvider{}

	// When: the DynamoDB debug reset endpoint runs.
	req := httptest.NewRequest(http.MethodPost, "/_debug/reset/dynamodb", nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", "dynamodb")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugResetService(store, []DebugStateProvider{dynamo}, map[string]bool{"dynamodb": true}).ServeHTTP(rec, req)

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

func TestDebugReset_clearsStateAcrossNamespacedStoreOverrides(t *testing.T) {
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

	// When the global debug reset endpoint is invoked with the wrapped store...
	req := httptest.NewRequest(http.MethodPost, "/_debug/reset", nil)
	rec := httptest.NewRecorder()
	debugReset(ns, nil).ServeHTTP(rec, req)

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
	// Direct unit test of the resetStore helper used by debugReset — proves
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

// TestDebugState_includesLogsEventsVirtualNamespace is the CloudWatch Logs
// equivalent of TestDebugState_includesDynamoDBItemsVirtualNamespace —
// storage-plan.md 2.3 requires log events (now stored in a dedicated
// logs_events SQL table, not the generic kv store) to stay visible to
// /_debug/state via the same DebugStateProvider mechanism DynamoDB uses.
func TestDebugState_includesLogsEventsVirtualNamespace(t *testing.T) {
	// Given: CloudWatch Logs has event data in its dedicated event backend.
	store := state.NewMemoryStore()
	logsProvider := fakeLogsDebugProvider{}
	providers := []DebugStateProvider{logsProvider}

	// When: the raw state summary is requested.
	req := httptest.NewRequest(http.MethodGet, "/_debug/state", nil)
	rec := httptest.NewRecorder()
	debugState(store, providers).ServeHTTP(rec, req)

	// Then: log events are exposed as a virtual debug namespace.
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, body)
	}
	if !containsDebugBody(body, `"logs:events"`) {
		t.Fatalf("expected logs:events namespace in debug state summary, got %s", body)
	}

	// And: fetching that namespace returns raw event JSON values.
	req = httptest.NewRequest(http.MethodGet, "/_debug/state/logs:events", nil)
	rec = httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", "logs:events")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugStateNamespace(store, providers).ServeHTTP(rec, req)
	body = rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, body)
	}
	if !containsDebugBody(body, `us-east-1/my-group/my-stream/1700000000000/0`) {
		t.Fatalf("expected log event key in namespace response, got %s", body)
	}
	if !containsDebugBody(body, `hello`) {
		t.Fatalf("expected log event message in namespace response, got %s", body)
	}
}

// TestDebugResetService_logsClearsVirtualEvents mirrors
// TestDebugResetService_dynamodbClearsVirtualItems for the "logs" service
// prefix, proving /_debug/reset/logs clears the dedicated event backend too.
func TestDebugResetService_logsClearsVirtualEvents(t *testing.T) {
	// Given: CloudWatch Logs has group metadata in state.Store and event data
	// in its virtual backend.
	store := state.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "logs:groups", "us-east-1/my-group", `{"name":"my-group"}`); err != nil {
		t.Fatal(err)
	}
	logsProvider := &resetCountingLogsDebugProvider{}

	// When: the logs debug reset endpoint runs.
	req := httptest.NewRequest(http.MethodPost, "/_debug/reset/logs", nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", "logs")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugResetService(store, []DebugStateProvider{logsProvider}, map[string]bool{"logs": true}).ServeHTTP(rec, req)

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

// TestDebugReset_clearsMultipleProviders proves /_debug/reset clears every
// registered DebugStateProvider, not just one hardcoded service — the
// generalization this test file's other multi-provider tests underwrite.
func TestDebugReset_clearsMultipleProviders(t *testing.T) {
	store := state.NewMemoryStore()
	dynamo := &resetCountingDynamoDebugProvider{}
	logsProvider := &resetCountingLogsDebugProvider{}

	req := httptest.NewRequest(http.MethodPost, "/_debug/reset", nil)
	rec := httptest.NewRecorder()
	debugReset(store, []DebugStateProvider{dynamo, logsProvider}).ServeHTTP(rec, req)

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

// TestDebugResetService_dynamodbUnaffectedByOtherProviders is a regression
// test for the debugDynamoDBProvider → []DebugStateProvider generalization:
// resetting "dynamodb" must reset only the DynamoDB provider, even when other
// providers (e.g. logs) are also registered.
func TestDebugResetService_dynamodbUnaffectedByOtherProviders(t *testing.T) {
	store := state.NewMemoryStore()
	dynamo := &resetCountingDynamoDebugProvider{}
	logsProvider := &resetCountingLogsDebugProvider{}

	req := httptest.NewRequest(http.MethodPost, "/_debug/reset/dynamodb", nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("service", "dynamodb")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugResetService(store, []DebugStateProvider{dynamo, logsProvider}, map[string]bool{"dynamodb": true}).ServeHTTP(rec, req)

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

// ---- 3.13: /_debug/state/{namespace} pagination -----------------------------

func TestDebugStateNamespace_paginatesStoreBackedNamespace(t *testing.T) {
	// Given: a namespace with three keys.
	store := state.NewMemoryStore()
	ctx := context.Background()
	for _, k := range []string{"a", "b", "c"} {
		if err := store.Set(ctx, "test:ns", k, `"v-`+k+`"`); err != nil {
			t.Fatal(err)
		}
	}

	// When: the first page is requested with limit=2.
	page1 := fetchDebugStateNamespacePage(t, store, nil, "test:ns", "", "2")

	// Then: it returns the first two keys in order and a nextKey cursor.
	if len(page1.Values) != 2 {
		t.Fatalf("expected 2 values on page 1, got %d: %+v", len(page1.Values), page1.Values)
	}
	if _, ok := page1.Values["a"]; !ok {
		t.Errorf("expected key %q on page 1, got %+v", "a", page1.Values)
	}
	if _, ok := page1.Values["b"]; !ok {
		t.Errorf("expected key %q on page 1, got %+v", "b", page1.Values)
	}
	if page1.NextKey != "b" {
		t.Fatalf("expected nextKey %q, got %q", "b", page1.NextKey)
	}

	// When: the second page is requested using the first page's nextKey.
	page2 := fetchDebugStateNamespacePage(t, store, nil, "test:ns", page1.NextKey, "2")

	// Then: it returns exactly the remaining key, and signals no further page.
	if len(page2.Values) != 1 {
		t.Fatalf("expected 1 value on page 2, got %d: %+v", len(page2.Values), page2.Values)
	}
	if _, ok := page2.Values["c"]; !ok {
		t.Errorf("expected key %q on page 2, got %+v", "c", page2.Values)
	}
	if page2.NextKey != "" {
		t.Fatalf("expected empty nextKey on the last page, got %q", page2.NextKey)
	}
}

func TestDebugStateNamespace_defaultLimitAppliedWhenAbsent(t *testing.T) {
	// Given: a namespace with fewer keys than the default page limit.
	store := state.NewMemoryStore()
	ctx := context.Background()
	if err := store.Set(ctx, "test:ns", "only-key", `"v"`); err != nil {
		t.Fatal(err)
	}

	// When: the namespace is requested with no ?limit= at all.
	page := fetchDebugStateNamespacePage(t, store, nil, "test:ns", "", "")

	// Then: every key is returned in a single page (well under the default cap).
	if len(page.Values) != 1 {
		t.Fatalf("expected 1 value, got %d: %+v", len(page.Values), page.Values)
	}
	if page.NextKey != "" {
		t.Fatalf("expected empty nextKey, got %q", page.NextKey)
	}
}

func TestParseDebugStateLimit(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"absent", "", debugStateDefaultPageLimit},
		{"non-numeric", "not-a-number", debugStateDefaultPageLimit},
		{"zero", "0", debugStateDefaultPageLimit},
		{"negative", "-5", debugStateDefaultPageLimit},
		{"valid", "10", 10},
		{"exceeds max", "999999", debugStateMaxPageLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseDebugStateLimit(tc.raw); got != tc.want {
				t.Errorf("parseDebugStateLimit(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// fakeMultiKeyDebugProvider is a DebugStateProvider with more than one key,
// used to exercise pagination over a provider-backed virtual namespace
// (which has no ScanPage of its own — see paginateDebugStateValues).
type fakeMultiKeyDebugProvider struct{}

func (fakeMultiKeyDebugProvider) DebugNamespace() string { return "fake:multi" }

func (fakeMultiKeyDebugProvider) DebugStateKeys(context.Context) ([]string, error) {
	return []string{"k1", "k2", "k3"}, nil
}

func (fakeMultiKeyDebugProvider) DebugStateValues(context.Context) (map[string]string, error) {
	return map[string]string{"k1": `"v1"`, "k2": `"v2"`, "k3": `"v3"`}, nil
}

func (fakeMultiKeyDebugProvider) DebugResetState(context.Context) error { return nil }

func TestDebugStateNamespace_paginatesProviderBackedNamespace(t *testing.T) {
	// Given: a virtual (provider-backed) namespace with three keys.
	store := state.NewMemoryStore()
	providers := []DebugStateProvider{fakeMultiKeyDebugProvider{}}

	// When: the first page is requested with limit=2.
	page1 := fetchDebugStateNamespacePage(t, store, providers, "fake:multi", "", "2")

	// Then: pagination applies the same way it does for a store-backed namespace.
	if len(page1.Values) != 2 {
		t.Fatalf("expected 2 values on page 1, got %d: %+v", len(page1.Values), page1.Values)
	}
	if page1.NextKey != "k2" {
		t.Fatalf("expected nextKey %q, got %q", "k2", page1.NextKey)
	}

	// When: the second page follows the cursor.
	page2 := fetchDebugStateNamespacePage(t, store, providers, "fake:multi", page1.NextKey, "2")

	// Then: the remaining key is returned and pagination terminates.
	if len(page2.Values) != 1 {
		t.Fatalf("expected 1 value on page 2, got %d: %+v", len(page2.Values), page2.Values)
	}
	if _, ok := page2.Values["k3"]; !ok {
		t.Errorf("expected key %q on page 2, got %+v", "k3", page2.Values)
	}
	if page2.NextKey != "" {
		t.Fatalf("expected empty nextKey on the last page, got %q", page2.NextKey)
	}
}

// fetchDebugStateNamespacePage issues a GET /_debug/state/{namespace} request
// with the given after/limit query parameters and decodes the paginated
// response.
func fetchDebugStateNamespacePage(t *testing.T, store state.Store, providers []DebugStateProvider, namespace, after, limit string) debugStateNamespacePage {
	t.Helper()
	url := "/_debug/state/" + namespace + "?"
	if after != "" {
		url += "after=" + after + "&"
	}
	if limit != "" {
		url += "limit=" + limit
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("namespace", namespace)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	debugStateNamespace(store, providers).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var page debugStateNamespacePage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return page
}

// ---- 3.6: /_debug/metrics ----------------------------------------------------

func TestDebugMetrics_zeroValueForMemoryStore(t *testing.T) {
	// Given: a store backend that does not implement state.DebugMetricsReporter.
	store := state.NewMemoryStore()

	// When: the metrics endpoint is requested.
	req := httptest.NewRequest(http.MethodGet, "/_debug/metrics", nil)
	rec := httptest.NewRecorder()
	debugMetrics(store).ServeHTTP(rec, req)

	// Then: it responds 200 with an empty (never null) store list, not an error.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp debugMetricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Stores == nil {
		t.Fatal("expected a non-nil (possibly empty) stores list")
	}
	if len(resp.Stores) != 0 {
		t.Fatalf("expected no reporting stores for MemoryStore, got %+v", resp.Stores)
	}
}

func containsDebugBody(body, want string) bool {
	for i := 0; i+len(want) <= len(body); i++ {
		if body[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
