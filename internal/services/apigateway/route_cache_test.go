package apigateway

// route_cache_test.go — pins the execution route cache: proxied requests are
// served from cached routing state, and every mutation type (create, update,
// delete, delete-all) drops the cache so the very next request observes the
// change. Staleness here would silently route production-shaped traffic to
// the wrong integration, so each mutation is asserted end-to-end through the
// execution handlers.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

func routeCacheTestHandler(t *testing.T) (*Handler, *capturingLambdaInvoker) {
	t.Helper()
	inv := &capturingLambdaInvoker{}
	h := newHandler(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(),
		serviceutil.NewServiceLogger(zap.NewNop(), "apigateway"),
		clock.NewMock(),
	)
	h.invoker = inv
	return h, inv
}

func execRequest(method, path string, params map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func lambdaIntegrationURI(functionName string) string {
	return "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:" + functionName + "/invocations"
}

func seedRestRoute(t *testing.T, h *Handler, apiID, path, functionName string) *Resource {
	t.Helper()
	ctx := context.Background()
	if aerr := h.store.putRestAPI(ctx, &RestAPI{ID: apiID, Name: apiID}); aerr != nil {
		t.Fatalf("putRestAPI: %v", aerr)
	}
	res := &Resource{
		ID:   "res-" + path[1:],
		Path: path,
		ResourceMethods: map[string]*Method{
			http.MethodGet: {
				HTTPMethod:        http.MethodGet,
				AuthorizationType: "NONE",
				MethodIntegration: &Integration{
					Type: "AWS_PROXY",
					URI:  lambdaIntegrationURI(functionName),
				},
			},
		},
	}
	if aerr := h.store.putResource(ctx, apiID, res); aerr != nil {
		t.Fatalf("putResource: %v", aerr)
	}
	return res
}

func executeRest(t *testing.T, h *Handler, apiID, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := execRequest(http.MethodGet, "/restapis/"+apiID+"/dev/_user_request_"+path, map[string]string{
		"restApiId": apiID,
		"stageName": "dev",
		"*":         path[1:],
	})
	h.ExecuteRestAPI(rec, req)
	return rec
}

func TestExecuteRestAPI_routeCacheInvalidatedByEveryMutation(t *testing.T) {
	// Given: a REST API with one Lambda-proxied route, executed once so the
	// route cache is warm.
	h, inv := routeCacheTestHandler(t)
	res := seedRestRoute(t, h, "api1", "/hello", "fn-one")
	if rec := executeRest(t, h, "api1", "/hello"); rec.Code != http.StatusNoContent {
		t.Fatalf("initial execute status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}
	if inv.functionName != "fn-one" {
		t.Fatalf("initial execute invoked %q, want fn-one", inv.functionName)
	}

	// When: the integration is repointed (putResource) — Then: the next
	// request routes to the new function, not the cached one.
	res.ResourceMethods[http.MethodGet].MethodIntegration.URI = lambdaIntegrationURI("fn-two")
	if aerr := h.store.putResource(context.Background(), "api1", res); aerr != nil {
		t.Fatalf("putResource update: %v", aerr)
	}
	if rec := executeRest(t, h, "api1", "/hello"); rec.Code != http.StatusNoContent {
		t.Fatalf("post-update execute status = %d, want 204", rec.Code)
	}
	if inv.functionName != "fn-two" {
		t.Fatalf("post-update execute invoked %q, want fn-two — stale route cache", inv.functionName)
	}

	// When: a new resource is added — Then: it is immediately routable.
	res2 := &Resource{
		ID:   "res-added",
		Path: "/added",
		ResourceMethods: map[string]*Method{
			http.MethodGet: {
				HTTPMethod:        http.MethodGet,
				AuthorizationType: "NONE",
				MethodIntegration: &Integration{Type: "AWS_PROXY", URI: lambdaIntegrationURI("fn-added")},
			},
		},
	}
	if aerr := h.store.putResource(context.Background(), "api1", res2); aerr != nil {
		t.Fatalf("putResource add: %v", aerr)
	}
	if rec := executeRest(t, h, "api1", "/added"); rec.Code != http.StatusNoContent {
		t.Fatalf("new-resource execute status = %d, want 204", rec.Code)
	}
	if inv.functionName != "fn-added" {
		t.Fatalf("new-resource execute invoked %q, want fn-added", inv.functionName)
	}

	// When: the resource is deleted — Then: the route is gone on the next
	// request (AWS answers 403 Missing Authentication Token).
	if aerr := h.store.deleteResource(context.Background(), "api1", res2.ID); aerr != nil {
		t.Fatalf("deleteResource: %v", aerr)
	}
	if rec := executeRest(t, h, "api1", "/added"); rec.Code != http.StatusForbidden {
		t.Fatalf("post-delete execute status = %d, want 403", rec.Code)
	}

	// When: every resource is deleted — Then: nothing routes.
	if aerr := h.store.deleteAllResources(context.Background(), "api1"); aerr != nil {
		t.Fatalf("deleteAllResources: %v", aerr)
	}
	if rec := executeRest(t, h, "api1", "/hello"); rec.Code != http.StatusForbidden {
		t.Fatalf("post-delete-all execute status = %d, want 403", rec.Code)
	}
}

func seedV2Route(t *testing.T, h *Handler, apiID, routeID, routeKey, integID, functionName string) {
	t.Helper()
	ctx := context.Background()
	if aerr := h.store.putV2Route(ctx, apiID, &RouteV2{RouteID: routeID, RouteKey: routeKey, Target: "integrations/" + integID}); aerr != nil {
		t.Fatalf("putV2Route: %v", aerr)
	}
	if aerr := h.store.putV2Integration(ctx, apiID, &IntegrationV2{
		IntegrationID:        integID,
		IntegrationType:      "AWS_PROXY",
		IntegrationURI:       lambdaIntegrationURI(functionName),
		PayloadFormatVersion: "2.0",
	}); aerr != nil {
		t.Fatalf("putV2Integration: %v", aerr)
	}
}

func executeV2(t *testing.T, h *Handler, apiID, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := execRequest(http.MethodGet, "/_overcast/apigateway/connections/"+apiID+"/dev"+path, map[string]string{
		"apiId":     apiID,
		"stageName": "dev",
		"*":         path[1:],
	})
	h.ExecuteV2API(rec, req)
	return rec
}

func TestExecuteV2API_routeCacheInvalidatedByEveryMutation(t *testing.T) {
	// Given: an HTTP API with one Lambda-proxied route, executed once so the
	// route cache is warm.
	h, inv := routeCacheTestHandler(t)
	ctx := context.Background()
	if aerr := h.store.putV2API(ctx, &APIV2{ApiID: "api2", Name: "api2", ProtocolType: "HTTP"}); aerr != nil {
		t.Fatalf("putV2API: %v", aerr)
	}
	seedV2Route(t, h, "api2", "r1", "GET /hello", "int1", "fn-one")
	if rec := executeV2(t, h, "api2", "/hello"); rec.Code != http.StatusNoContent {
		t.Fatalf("initial execute status = %d, want 204; body: %s", rec.Code, rec.Body.String())
	}
	if inv.functionName != "fn-one" {
		t.Fatalf("initial execute invoked %q, want fn-one", inv.functionName)
	}

	// When: the route is retargeted (putV2Route) — Then: the next request
	// follows the new target.
	seedV2Route(t, h, "api2", "r1", "GET /hello", "int2", "fn-two")
	if rec := executeV2(t, h, "api2", "/hello"); rec.Code != http.StatusNoContent {
		t.Fatalf("post-update execute status = %d, want 204", rec.Code)
	}
	if inv.functionName != "fn-two" {
		t.Fatalf("post-update execute invoked %q, want fn-two — stale route cache", inv.functionName)
	}

	// When: a route is added — Then: it is immediately routable.
	seedV2Route(t, h, "api2", "r2", "GET /added", "int3", "fn-added")
	if rec := executeV2(t, h, "api2", "/added"); rec.Code != http.StatusNoContent {
		t.Fatalf("new-route execute status = %d, want 204", rec.Code)
	}
	if inv.functionName != "fn-added" {
		t.Fatalf("new-route execute invoked %q, want fn-added", inv.functionName)
	}

	// When: a route is deleted — Then: it stops routing on the next request.
	if aerr := h.store.deleteV2Route(ctx, "api2", "r2"); aerr != nil {
		t.Fatalf("deleteV2Route: %v", aerr)
	}
	if rec := executeV2(t, h, "api2", "/added"); rec.Code != http.StatusNotFound {
		t.Fatalf("post-delete execute status = %d, want 404", rec.Code)
	}

	// When: every route is deleted — Then: nothing routes.
	if aerr := h.store.deleteAllV2Routes(ctx, "api2"); aerr != nil {
		t.Fatalf("deleteAllV2Routes: %v", aerr)
	}
	if rec := executeV2(t, h, "api2", "/hello"); rec.Code != http.StatusNotFound {
		t.Fatalf("post-delete-all execute status = %d, want 404", rec.Code)
	}
}
