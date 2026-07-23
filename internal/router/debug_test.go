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

	// When: the raw state summary is requested.
	req := httptest.NewRequest(http.MethodGet, "/_debug/state", nil)
	rec := httptest.NewRecorder()
	debugState(store, dynamo).ServeHTTP(rec, req)

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
	debugStateNamespace(store, dynamo).ServeHTTP(rec, req)
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
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := body["us-east-1/deps:0000000001"]
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
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := body["record"]
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
	debugResetService(store, dynamo, map[string]bool{"dynamodb": true}).ServeHTTP(rec, req)

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

func containsDebugBody(body, want string) bool {
	for i := 0; i+len(want) <= len(body); i++ {
		if body[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
