package lambda

// handler_metrics_test.go tests GetFunctionMetrics (handler_metrics.go)
// against a real *metrics.Service — never a stub — the same convention
// #1268's SQS/SNS/DynamoDB/API Gateway metrics tests established: drive the
// real recorder, read back through the same path the handler itself uses.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/state"
)

func newMonitorTestHandler(t *testing.T) (*Handler, *metrics.Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	svc := metrics.NewRecorder(state.NewMemoryStore(), mock, zap.NewNop())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	h := &Handler{clk: mock, metrics: svc}
	return h, svc, mock
}

func doGetFunctionMetrics(t *testing.T, h *Handler, name, rangeToken string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/_overcast/lambda/functions/" + name + "/metrics"
	if rangeToken != "" {
		url += "?range=" + rangeToken
	}
	req := withFunctionNameParam(httptest.NewRequest(http.MethodGet, url, nil), name)
	rec := httptest.NewRecorder()
	h.GetFunctionMetrics(rec, req)
	return rec
}

// TestGetFunctionMetrics_NoDataIsEmptyNotError pins the Monitor tab's "No
// metric data in this range" state: a function that has never been invoked
// gets a 200 with every catalogue series present but empty, never an error.
func TestGetFunctionMetrics_NoDataIsEmptyNotError(t *testing.T) {
	h, _, _ := newMonitorTestHandler(t)
	rec := doGetFunctionMetrics(t, h, "never-invoked", "")

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
	if resp.Range != "1h" {
		t.Errorf("expected default range=1h, got %q", resp.Range)
	}
	// The full catalogue is always present (Invocations, Errors, Duration x2,
	// Throttles, ConcurrentExecutions = 6 series) — it's each series' Points
	// that are empty, not the series itself missing.
	if len(resp.Series) != 6 {
		t.Fatalf("expected 6 catalogue series, got %d: %+v", len(resp.Series), resp.Series)
	}
	for _, s := range resp.Series {
		if len(s.Points) != 0 {
			t.Errorf("expected no points for a never-invoked function's %s/%s, got %+v", s.Metric, s.Statistic, s.Points)
		}
	}
}

// TestGetFunctionMetrics_ReturnsRecordedObservations proves the read side
// answers real recorded data — Invocations as Sum, Duration as both Average
// and Maximum — through the exact same *metrics.Service a real invocation
// would have recorded through.
func TestGetFunctionMetrics_ReturnsRecordedObservations(t *testing.T) {
	h, svc, mock := newMonitorTestHandler(t)
	ctx := context.Background()
	now := mock.Now()
	dims := []metrics.Dimension{{Name: "FunctionName", Value: "fn-a"}}

	for _, ms := range []float64{100, 300} {
		if err := svc.Observe(ctx, metrics.Observation{Namespace: "AWS/Lambda", Name: "Invocations", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
			t.Fatalf("Observe Invocations: %v", err)
		}
		if err := svc.Observe(ctx, metrics.Observation{Namespace: "AWS/Lambda", Name: "Duration", Dimensions: dims, Unit: "Milliseconds", Value: ms, Timestamp: now}); err != nil {
			t.Fatalf("Observe Duration: %v", err)
		}
	}

	rec := doGetFunctionMetrics(t, h, "fn-a", "1h")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp metrics.MonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	byKey := make(map[string]metrics.MonitorSeries)
	for _, s := range resp.Series {
		byKey[s.Metric+"/"+s.Statistic] = s
	}

	inv, ok := byKey["Invocations/Sum"]
	if !ok || len(inv.Points) != 1 || inv.Points[0].Value != 2 {
		t.Fatalf("expected Invocations/Sum=2, got %+v (present=%v)", inv, ok)
	}
	avg, ok := byKey["Duration/Average"]
	if !ok || len(avg.Points) != 1 || avg.Points[0].Value != 200 {
		t.Fatalf("expected Duration/Average=200, got %+v (present=%v)", avg, ok)
	}
	max, ok := byKey["Duration/Maximum"]
	if !ok || len(max.Points) != 1 || max.Points[0].Value != 300 {
		t.Fatalf("expected Duration/Maximum=300, got %+v (present=%v)", max, ok)
	}
}

// TestGetFunctionMetrics_InvalidRangeIs400 pins that an unrecognized range
// token is rejected, not silently substituted.
func TestGetFunctionMetrics_InvalidRangeIs400(t *testing.T) {
	h, _, _ := newMonitorTestHandler(t)
	rec := doGetFunctionMetrics(t, h, "fn-a", "3d")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestGetFunctionMetrics_CollectionDisabledReportsEnabledFalse pins the
// disabled-collection contract: a Handler whose metrics field is nil (no
// InitMetrics call — OVERCAST_SERVICE_METRICS=disabled, or a router built
// without CloudWatch) answers {"enabled": false}, never a 500 or a fake empty
// chart indistinguishable from "collection is on but nothing happened yet".
func TestGetFunctionMetrics_CollectionDisabledReportsEnabledFalse(t *testing.T) {
	h := &Handler{clk: clock.NewMock()}
	rec := doGetFunctionMetrics(t, h, "fn-a", "")
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
