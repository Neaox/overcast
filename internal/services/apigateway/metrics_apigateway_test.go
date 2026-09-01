package apigateway

// metrics_apigateway_test.go proves the phase-2 AWS/ApiGateway metric
// catalogue (metrics_apigateway.go, docs/plans/service-metrics-platform.md)
// is recorded once per dispatched request for both REST (v1) and HTTP (v2)
// APIs, using a real internal/metrics.Recorder (not a stub) and reading it
// back the same way CloudWatch's read-through does — metrics.Service.QueryRange.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/metrics"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// statusControlledInvoker returns a fixed Lambda-proxy statusCode response,
// letting a test drive ExecuteRestAPI/ExecuteV2API through a 2xx, 4xx, or
// 5xx outcome deterministically (unlike capturingLambdaInvoker, which is
// pinned to 204).
type statusControlledInvoker struct {
	status int
}

func (i *statusControlledInvoker) Invoke(_ context.Context, _ string, _ []byte) (*events.InvokeOutcome, error) {
	return &events.InvokeOutcome{Payload: []byte(`{"statusCode":` + itoa(i.status) + `}`)}, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func newMetricsAPIGatewayHandler(t *testing.T) (*Handler, *metrics.Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	mock.Set(time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC))
	st := state.NewMemoryStore()
	h := newHandler(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		st,
		serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		mock,
	)
	rec := metrics.NewRecorder(st, mock, zap.NewNop())
	h.metrics = rec
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rec.Stop(ctx)
	})
	return h, rec, mock
}

func agwSum(t *testing.T, rec *metrics.Service, name string, dims []metrics.Dimension, now time.Time) float64 {
	t.Helper()
	buckets, err := rec.QueryRange(context.Background(), "AWS/ApiGateway", name, dims, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("QueryRange %s: %v", name, err)
	}
	var sum float64
	for _, b := range buckets {
		sum += b.Sum
	}
	return sum
}

func TestExecuteRestAPI_RecordsCountAndLatency(t *testing.T) {
	h, rec, mock := newMetricsAPIGatewayHandler(t)
	h.invoker = &statusControlledInvoker{status: 200}
	seedRestRoute(t, h, "api1", "/pets", "handler-fn")

	rr := executeRest(t, h, "api1", "/pets")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	now := mock.Now().UTC()
	dims := []metrics.Dimension{
		{Name: "ApiName", Value: "api1"}, {Name: "Stage", Value: "dev"},
		{Name: "Method", Value: "GET"}, {Name: "Resource", Value: "/pets"},
	}
	if got, want := agwSum(t, rec, "Count", dims, now), 1.0; got != want {
		t.Fatalf("Count Sum = %v, want %v", got, want)
	}
	if got := agwSum(t, rec, "Latency", dims, now); got < 0 {
		t.Fatalf("Latency Sum = %v, want >= 0", got)
	}
	if got := agwSum(t, rec, "IntegrationLatency", dims, now); got < 0 {
		t.Fatalf("IntegrationLatency Sum = %v, want >= 0", got)
	}
	if got := agwSum(t, rec, "4XXError", dims, now); got != 0 {
		t.Fatalf("4XXError Sum on a 200 = %v, want 0", got)
	}
	if got := agwSum(t, rec, "5XXError", dims, now); got != 0 {
		t.Fatalf("5XXError Sum on a 200 = %v, want 0", got)
	}
}

func TestExecuteRestAPI_5xxIntegrationResponse_Records5XXError(t *testing.T) {
	h, rec, mock := newMetricsAPIGatewayHandler(t)
	h.invoker = &statusControlledInvoker{status: 500}
	seedRestRoute(t, h, "api1", "/pets", "handler-fn")

	rr := executeRest(t, h, "api1", "/pets")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}

	now := mock.Now().UTC()
	dims := []metrics.Dimension{
		{Name: "ApiName", Value: "api1"}, {Name: "Stage", Value: "dev"},
		{Name: "Method", Value: "GET"}, {Name: "Resource", Value: "/pets"},
	}
	if got, want := agwSum(t, rec, "5XXError", dims, now), 1.0; got != want {
		t.Fatalf("5XXError Sum = %v, want %v", got, want)
	}
	if got := agwSum(t, rec, "4XXError", dims, now); got != 0 {
		t.Fatalf("4XXError Sum on a 500 = %v, want 0", got)
	}
}

func TestExecuteRestAPI_UnknownAPI_RecordsNoMetric(t *testing.T) {
	h, rec, _ := newMetricsAPIGatewayHandler(t)

	rr := executeRest(t, h, "does-not-exist", "/pets")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}

	// An unknown restApiId never resolves an ApiName to dimension a series
	// with — AWS never publishes a datapoint it cannot dimension, so nothing
	// should be recorded (see recordRestAPIOutcome's doc comment).
	buckets, err := rec.ListMetrics(context.Background(), "AWS/ApiGateway")
	if err != nil {
		t.Fatalf("ListMetrics: %v", err)
	}
	if len(buckets) != 0 {
		t.Fatalf("expected no AWS/ApiGateway series for an unresolvable API, got %+v", buckets)
	}
}

func TestExecuteV2API_RecordsCountWithApiIdAndRouteKey(t *testing.T) {
	h, rec, mock := newMetricsAPIGatewayHandler(t)
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

	rec2 := httptest.NewRecorder()
	req := execRequest(http.MethodGet, "/v2/apis/api2/stages/$default/pets", map[string]string{
		"apiId":     "api2",
		"stageName": "$default",
		"*":         "pets",
	})
	h.ExecuteV2API(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}

	now := mock.Now().UTC()
	dims := []metrics.Dimension{
		{Name: "ApiId", Value: "api2"}, {Name: "Stage", Value: "$default"},
		{Name: "HttpMethod", Value: "GET"}, {Name: "RouteKey", Value: "GET /pets"},
	}
	if got, want := agwSum(t, rec, "Count", dims, now), 1.0; got != want {
		t.Fatalf("Count Sum = %v, want %v", got, want)
	}
}

// TestExecuteRestAPI_RecordsAllThreeCoarseDimensionCombinations proves
// recordRestAPIOutcome's #1307 change: one dispatched request now leaves a
// Count=1 series under all three AWS-documented REST combinations, not only
// the most granular one TestExecuteRestAPI_RecordsCountAndLatency already
// pins.
func TestExecuteRestAPI_RecordsAllThreeCoarseDimensionCombinations(t *testing.T) {
	h, rec, mock := newMetricsAPIGatewayHandler(t)
	h.invoker = &statusControlledInvoker{status: 200}
	seedRestRoute(t, h, "api1", "/pets", "handler-fn")

	rr := executeRest(t, h, "api1", "/pets")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	now := mock.Now().UTC()
	combos := map[string][]metrics.Dimension{
		"ApiName only":  {{Name: "ApiName", Value: "api1"}},
		"ApiName+Stage": {{Name: "ApiName", Value: "api1"}, {Name: "Stage", Value: "dev"}},
		"ApiName+Stage+Method+Resource": {
			{Name: "ApiName", Value: "api1"}, {Name: "Stage", Value: "dev"},
			{Name: "Method", Value: "GET"}, {Name: "Resource", Value: "/pets"},
		},
	}
	for label, dims := range combos {
		if got, want := agwSum(t, rec, "Count", dims, now), 1.0; got != want {
			t.Errorf("%s: Count Sum = %v, want %v", label, got, want)
		}
	}
}

// TestExecuteV2API_RecordsAllThreeCoarseDimensionCombinations is
// TestExecuteRestAPI_RecordsAllThreeCoarseDimensionCombinations' HTTP (v2)
// equivalent.
func TestExecuteV2API_RecordsAllThreeCoarseDimensionCombinations(t *testing.T) {
	h, rec, mock := newMetricsAPIGatewayHandler(t)
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

	rr := httptest.NewRecorder()
	req := execRequest(http.MethodGet, "/v2/apis/api2/stages/$default/pets", map[string]string{
		"apiId":     "api2",
		"stageName": "$default",
		"*":         "pets",
	})
	h.ExecuteV2API(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	now := mock.Now().UTC()
	combos := map[string][]metrics.Dimension{
		"ApiId only":  {{Name: "ApiId", Value: "api2"}},
		"ApiId+Stage": {{Name: "ApiId", Value: "api2"}, {Name: "Stage", Value: "$default"}},
		"ApiId+Stage+HttpMethod+RouteKey": {
			{Name: "ApiId", Value: "api2"}, {Name: "Stage", Value: "$default"},
			{Name: "HttpMethod", Value: "GET"}, {Name: "RouteKey", Value: "GET /pets"},
		},
	}
	for label, dims := range combos {
		if got, want := agwSum(t, rec, "Count", dims, now), 1.0; got != want {
			t.Errorf("%s: Count Sum = %v, want %v", label, got, want)
		}
	}
}

// TestExecuteV2API_5xxIntegrationResponse_RecordsLowercase5xx pins #1307's
// HTTP API error-metric-name fix: a v2 5xx outcome is recorded as "5xx"
// (AWS's real HTTP API metric name), never REST's "5XXError".
func TestExecuteV2API_5xxIntegrationResponse_RecordsLowercase5xx(t *testing.T) {
	h, rec, mock := newMetricsAPIGatewayHandler(t)
	h.invoker = &statusControlledInvoker{status: 500}
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

	rr := httptest.NewRecorder()
	req := execRequest(http.MethodGet, "/v2/apis/api2/stages/$default/pets", map[string]string{
		"apiId":     "api2",
		"stageName": "$default",
		"*":         "pets",
	})
	h.ExecuteV2API(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}

	now := mock.Now().UTC()
	dims := []metrics.Dimension{
		{Name: "ApiId", Value: "api2"}, {Name: "Stage", Value: "$default"},
		{Name: "HttpMethod", Value: "GET"}, {Name: "RouteKey", Value: "GET /pets"},
	}
	if got, want := agwSum(t, rec, "5xx", dims, now), 1.0; got != want {
		t.Fatalf("5xx Sum = %v, want %v", got, want)
	}
	if got := agwSum(t, rec, "5XXError", dims, now); got != 0 {
		t.Fatalf("5XXError Sum on a v2 500 = %v, want 0 (HTTP APIs must not use the REST error name)", got)
	}
	if got := agwSum(t, rec, "4xx", dims, now); got != 0 {
		t.Fatalf("4xx Sum on a 500 = %v, want 0", got)
	}
}

func TestAPIGatewayMetrics_NilRecorderIsNoOp(t *testing.T) {
	inv := &capturingLambdaInvoker{}
	h := newHandler(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(),
		serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		clock.NewMock(),
	)
	h.invoker = inv
	seedRestRoute(t, h, "api1", "/pets", "handler-fn")
	rr := executeRest(t, h, "api1", "/pets")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("ExecuteRestAPI with nil metrics recorder must still succeed: status=%d", rr.Code)
	}
}
