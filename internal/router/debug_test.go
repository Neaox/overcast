package router

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	debugResetService(store, dynamo).ServeHTTP(rec, req)

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
