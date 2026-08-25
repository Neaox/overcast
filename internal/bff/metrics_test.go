package bff

// metrics_test.go — GET /api/lambda/functions/{name}/metrics and
// GET /api/sqs/queues/{name}/metrics, the Monitor tab's BFF proxy into the
// emulator's own allowlist endpoints (docs/plans/service-metrics-platform.md
// phase 3). This proxy layer is deliberately thin — it forwards "range" and
// the region header and passes the upstream body/status through verbatim,
// exactly like every other BFF proxy in this package (see
// lambda_layer_metadata_test.go's precedent).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLambdaMetrics_proxiesPathRangeAndRegion(t *testing.T) {
	var gotPath, gotQuery, gotRegion string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotRegion = r.Header.Get("X-Overcast-Region")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"enabled":true,"range":"6h","series":[]}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/lambda/functions/fn-a/metrics?range=6h", nil)
	req.Header.Set("X-Overcast-Region", "ap-southeast-2")
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_overcast/lambda/functions/fn-a/metrics" {
		t.Errorf("proxied to %q, want the emulator's function metrics path", gotPath)
	}
	if gotQuery != "range=6h" {
		t.Errorf("query = %q, want range=6h forwarded", gotQuery)
	}
	if gotRegion != "ap-southeast-2" {
		t.Errorf("forwarded region = %q, want ap-southeast-2", gotRegion)
	}
	if !strings.Contains(rec.Body.String(), `"range":"6h"`) {
		t.Errorf("body = %s, want the upstream payload passed through", rec.Body.String())
	}
}

func TestLambdaMetrics_noRangeOmitsQueryString(t *testing.T) {
	var gotQuery string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"enabled":true,"range":"1h","series":[]}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/lambda/functions/fn-a/metrics", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty (no range param forwarded)", gotQuery)
	}
}

func TestSQSMetrics_proxiesPathRangeAndRegion(t *testing.T) {
	var gotPath, gotQuery, gotRegion string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotRegion = r.Header.Get("X-Overcast-Region")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"enabled":true,"range":"24h","series":[]}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/sqs/queues/q-a/metrics?range=24h", nil)
	req.Header.Set("X-Overcast-Region", "eu-west-1")
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_overcast/sqs/queues/q-a/metrics" {
		t.Errorf("proxied to %q, want the emulator's queue metrics path", gotPath)
	}
	if gotQuery != "range=24h" {
		t.Errorf("query = %q, want range=24h forwarded", gotQuery)
	}
	if gotRegion != "eu-west-1" {
		t.Errorf("forwarded region = %q, want eu-west-1", gotRegion)
	}
	if !strings.Contains(rec.Body.String(), `"range":"24h"`) {
		t.Errorf("body = %s, want the upstream payload passed through", rec.Body.String())
	}
}

func TestAPIGatewayRestApiMetrics_proxiesPathRangeStageAndRegion(t *testing.T) {
	var gotPath, gotQuery, gotRegion string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotRegion = r.Header.Get("X-Overcast-Region")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"enabled":true,"range":"6h","series":[]}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/apigateway/restapis/api1/metrics?range=6h&stage=dev", nil)
	req.Header.Set("X-Overcast-Region", "ap-southeast-2")
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_overcast/apigateway/restapis/api1/metrics" {
		t.Errorf("proxied to %q, want the emulator's REST API metrics path", gotPath)
	}
	if gotQuery != "range=6h&stage=dev" {
		t.Errorf("query = %q, want range=6h&stage=dev forwarded", gotQuery)
	}
	if gotRegion != "ap-southeast-2" {
		t.Errorf("forwarded region = %q, want ap-southeast-2", gotRegion)
	}
	if !strings.Contains(rec.Body.String(), `"range":"6h"`) {
		t.Errorf("body = %s, want the upstream payload passed through", rec.Body.String())
	}
}

func TestAPIGatewayRestApiMetrics_noParamsOmitsQueryString(t *testing.T) {
	var gotQuery string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"enabled":true,"range":"1h","series":[]}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/apigateway/restapis/api1/metrics", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want empty (no range/stage forwarded)", gotQuery)
	}
}

func TestAPIGatewayRestApiMetrics_notFoundIsPassedThrough(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"__type":"NotFoundException","message":"Invalid REST API identifier specified: does-not-exist"}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/apigateway/restapis/does-not-exist/metrics", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want the emulator's 404 passed through", rec.Code)
	}
}

func TestAPIGatewayApiMetrics_proxiesPathRangeStageAndRegion(t *testing.T) {
	var gotPath, gotQuery, gotRegion string
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotRegion = r.Header.Get("X-Overcast-Region")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"enabled":true,"range":"24h","series":[]}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/apigateway/apis/api2/metrics?range=24h&stage=%24default", nil)
	req.Header.Set("X-Overcast-Region", "eu-west-1")
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/_overcast/apigateway/apis/api2/metrics" {
		t.Errorf("proxied to %q, want the emulator's HTTP API metrics path", gotPath)
	}
	if gotQuery != "range=24h&stage=%24default" {
		t.Errorf("query = %q, want range=24h&stage=%%24default forwarded", gotQuery)
	}
	if gotRegion != "eu-west-1" {
		t.Errorf("forwarded region = %q, want eu-west-1", gotRegion)
	}
	if !strings.Contains(rec.Body.String(), `"range":"24h"`) {
		t.Errorf("body = %s, want the upstream payload passed through", rec.Body.String())
	}
}

func TestSQSMetrics_invalidRangeIs400(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"enabled":true,"range":"3d","series":[],"error":"invalid range"}`))
	}))
	defer restore()

	req := httptest.NewRequest(http.MethodGet, "/api/sqs/queues/q-a/metrics?range=3d", nil)
	rec := httptest.NewRecorder()
	NewHandler(nil, nil, UIConfig{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want the emulator's 400 passed through", rec.Code)
	}
}
