package apigateway

// handler_metrics_test.go tests GetRestApiMetrics and GetApiMetrics
// (handler_metrics.go) against a real *metrics.Service, following the
// convention every other service's Monitor endpoint test uses: drive the
// real recorder, read back through the exact path the handler itself uses.
// Exercises the #1307 coarse-series recording end to end — the {ApiName}/
// {ApiId}-only series and the +Stage series are only discoverable here
// because recordRestAPIOutcome/recordV2APIOutcome (metrics_apigateway.go)
// record them, not just the fully-dimensioned detailed combination.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/metrics"
)

func withAPIIDParam(req *http.Request, apiID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("apiId", apiID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func doGetRestApiMetrics(t *testing.T, h *Handler, apiID, stage, rangeToken string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/_overcast/apigateway/restapis/" + apiID + "/metrics"
	qs := ""
	if rangeToken != "" {
		qs += "range=" + rangeToken
	}
	if stage != "" {
		if qs != "" {
			qs += "&"
		}
		qs += "stage=" + stage
	}
	if qs != "" {
		url += "?" + qs
	}
	req := withAPIIDParam(httptest.NewRequest(http.MethodGet, url, nil), apiID)
	rec := httptest.NewRecorder()
	h.GetRestApiMetrics(rec, req)
	return rec
}

func doGetApiMetrics(t *testing.T, h *Handler, apiID, stage, rangeToken string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/_overcast/apigateway/apis/" + apiID + "/metrics"
	qs := ""
	if rangeToken != "" {
		qs += "range=" + rangeToken
	}
	if stage != "" {
		if qs != "" {
			qs += "&"
		}
		qs += "stage=" + stage
	}
	if qs != "" {
		url += "?" + qs
	}
	req := withAPIIDParam(httptest.NewRequest(http.MethodGet, url, nil), apiID)
	rec := httptest.NewRecorder()
	h.GetApiMetrics(rec, req)
	return rec
}

// ---- REST (v1) --------------------------------------------------------

func TestGetRestApiMetrics_UnknownApiIs404(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	rec := doGetRestApiMetrics(t, h, "does-not-exist", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestGetRestApiMetrics_NoDataIsEmptyNotError(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	ctx := context.Background()
	if aerr := h.store.putRestAPI(ctx, &RestAPI{ID: "api1", Name: "api1"}); aerr != nil {
		t.Fatalf("putRestAPI: %v", aerr)
	}

	rec := doGetRestApiMetrics(t, h, "api1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp metrics.MonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Enabled {
		t.Fatal("expected Enabled=true (a *metrics.Service is wired)")
	}
	// Count, 4XXError, 5XXError (1 stat each) + Latency, IntegrationLatency
	// (2 stats each) = 7 catalogue series.
	if len(resp.Series) != 7 {
		t.Fatalf("expected 7 catalogue series, got %d: %+v", len(resp.Series), resp.Series)
	}
	for _, s := range resp.Series {
		if len(s.Points) != 0 {
			t.Errorf("expected no points for a never-used API's %s/%s, got %+v", s.Metric, s.Statistic, s.Points)
		}
	}
}

func TestGetRestApiMetrics_ReturnsApiNameOnlySeriesByDefault(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	h.invoker = &statusControlledInvoker{status: 200}
	seedRestRoute(t, h, "api1", "/pets", "handler-fn")
	if rr := executeRest(t, h, "api1", "/pets"); rr.Code != http.StatusOK {
		t.Fatalf("executeRest status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	got := doGetRestApiMetrics(t, h, "api1", "", "1h")
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", got.Code, got.Body.String())
	}
	var resp metrics.MonitorResponse
	if err := json.Unmarshal(got.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	found := false
	for _, s := range resp.Series {
		if s.Metric == "Count" && s.Statistic == metrics.StatSum {
			found = true
			if len(s.Points) == 0 || s.Points[len(s.Points)-1].Value != 1 {
				t.Errorf("Count/Sum points = %+v, want a point with value 1", s.Points)
			}
		}
	}
	if !found {
		t.Fatal("expected a Count/Sum series in the response")
	}
}

func TestGetRestApiMetrics_StageParamFiltersToStageSeries(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	h.invoker = &statusControlledInvoker{status: 200}
	seedRestRoute(t, h, "api1", "/pets", "handler-fn")
	if rr := executeRest(t, h, "api1", "/pets"); rr.Code != http.StatusOK {
		t.Fatalf("executeRest status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	// The ApiName+Stage series should also carry the recorded observation —
	// it is recorded alongside the ApiName-only series, not derived from it.
	got := doGetRestApiMetrics(t, h, "api1", "dev", "1h")
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", got.Code, got.Body.String())
	}
	var resp metrics.MonitorResponse
	if err := json.Unmarshal(got.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, s := range resp.Series {
		if s.Metric == "Count" && s.Statistic == metrics.StatSum {
			if len(s.Points) == 0 || s.Points[len(s.Points)-1].Value != 1 {
				t.Fatalf("?stage=dev Count/Sum points = %+v, want a point with value 1", s.Points)
			}
			return
		}
	}
	t.Fatal("expected a Count/Sum series in the response")
}

func TestGetRestApiMetrics_InvalidRangeIs400(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	ctx := context.Background()
	if aerr := h.store.putRestAPI(ctx, &RestAPI{ID: "api1", Name: "api1"}); aerr != nil {
		t.Fatalf("putRestAPI: %v", aerr)
	}
	rec := doGetRestApiMetrics(t, h, "api1", "", "3d")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestGetRestApiMetrics_CollectionDisabledReportsEnabledFalse(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	ctx := context.Background()
	if aerr := h.store.putRestAPI(ctx, &RestAPI{ID: "api1", Name: "api1"}); aerr != nil {
		t.Fatalf("putRestAPI: %v", aerr)
	}
	h.metrics = nil // simulate OVERCAST_SERVICE_METRICS=disabled

	rec := doGetRestApiMetrics(t, h, "api1", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp metrics.MonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Enabled {
		t.Error("expected Enabled=false with no metrics.Service wired")
	}
}

// ---- HTTP (v2) ----------------------------------------------------------

func TestGetApiMetrics_UnknownApiIs404(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	rec := doGetApiMetrics(t, h, "does-not-exist", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestGetApiMetrics_ReturnsLowercase4xx5xxCatalogue(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	ctx := context.Background()
	if aerr := h.store.putV2API(ctx, &APIV2{ApiID: "api2", Name: "http-api", ProtocolType: "HTTP"}); aerr != nil {
		t.Fatalf("putV2API: %v", aerr)
	}

	rec := doGetApiMetrics(t, h, "api2", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp metrics.MonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	metricNames := map[string]bool{}
	for _, s := range resp.Series {
		metricNames[s.Metric] = true
	}
	for _, want := range []string{"Count", "4xx", "5xx", "Latency", "IntegrationLatency"} {
		if !metricNames[want] {
			t.Errorf("catalogue missing %q; got %+v", want, metricNames)
		}
	}
	if metricNames["4XXError"] || metricNames["5XXError"] {
		t.Errorf("HTTP API catalogue must not use REST's 4XXError/5XXError names; got %+v", metricNames)
	}
}

func TestGetApiMetrics_StageParamFiltersToStageSeries(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	h.invoker = &statusControlledInvoker{status: 200}
	ctx := context.Background()
	if aerr := h.store.putV2API(ctx, &APIV2{ApiID: "api2", Name: "http-api", ProtocolType: "HTTP"}); aerr != nil {
		t.Fatalf("putV2API: %v", aerr)
	}
	if aerr := h.store.putV2Integration(ctx, "api2", &IntegrationV2{IntegrationID: "integ1", IntegrationType: "AWS_PROXY", IntegrationURI: lambdaIntegrationURI("handler-fn")}); aerr != nil {
		t.Fatalf("putV2Integration: %v", aerr)
	}
	if aerr := h.store.putV2Route(ctx, "api2", &RouteV2{RouteID: "route1", RouteKey: "GET /pets", Target: "integrations/integ1"}); aerr != nil {
		t.Fatalf("putV2Route: %v", aerr)
	}
	req := execRequest(http.MethodGet, "/v2/apis/api2/stages/$default/pets", map[string]string{
		"apiId":     "api2",
		"stageName": "$default",
		"*":         "pets",
	})
	rr := httptest.NewRecorder()
	h.ExecuteV2API(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("ExecuteV2API status = %d, want 200: %s", rr.Code, rr.Body.String())
	}

	got := doGetApiMetrics(t, h, "api2", "$default", "1h")
	if got.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", got.Code, got.Body.String())
	}
	var resp metrics.MonitorResponse
	if err := json.Unmarshal(got.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, s := range resp.Series {
		if s.Metric == "Count" && s.Statistic == metrics.StatSum {
			if len(s.Points) == 0 || s.Points[len(s.Points)-1].Value != 1 {
				t.Fatalf("?stage=$default Count/Sum points = %+v, want a point with value 1", s.Points)
			}
			return
		}
	}
	t.Fatal("expected a Count/Sum series in the response")
}

func TestGetApiMetrics_CollectionDisabledReportsEnabledFalse(t *testing.T) {
	h, _, _ := newMetricsAPIGatewayHandler(t)
	ctx := context.Background()
	if aerr := h.store.putV2API(ctx, &APIV2{ApiID: "api2", Name: "http-api", ProtocolType: "HTTP"}); aerr != nil {
		t.Fatalf("putV2API: %v", aerr)
	}
	h.metrics = nil

	rec := doGetApiMetrics(t, h, "api2", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp metrics.MonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Enabled {
		t.Error("expected Enabled=false with no metrics.Service wired")
	}
}
