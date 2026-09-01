package sqs

// handler_metrics_test.go tests GetQueueMetrics (handler_metrics.go) against
// a real *metrics.Service — the same convention #1268's own metrics tests
// established: drive the real recorder, read back through the exact path the
// handler itself uses.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/metrics"
	"github.com/overcast-sh/overcast/internal/state"
)

func withQueueNameParam(req *http.Request, name string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", name)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

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

func doGetQueueMetrics(t *testing.T, h *Handler, name, rangeToken string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/_overcast/sqs/queues/" + name + "/metrics"
	if rangeToken != "" {
		url += "?range=" + rangeToken
	}
	req := withQueueNameParam(httptest.NewRequest(http.MethodGet, url, nil), name)
	rec := httptest.NewRecorder()
	h.GetQueueMetrics(rec, req)
	return rec
}

// TestGetQueueMetrics_NoDataIsEmptyNotError mirrors Lambda's
// TestGetFunctionMetrics_NoDataIsEmptyNotError: an idle queue's Monitor
// request gets a 200 with the full catalogue present but empty.
func TestGetQueueMetrics_NoDataIsEmptyNotError(t *testing.T) {
	h, _, _ := newMonitorTestHandler(t)
	rec := doGetQueueMetrics(t, h, "idle-queue", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp metrics.MonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Enabled {
		t.Fatal("expected Enabled=true")
	}
	if len(resp.Series) != len(sqsMonitorCatalog) {
		t.Fatalf("expected %d catalogue series, got %d: %+v", len(sqsMonitorCatalog), len(resp.Series), resp.Series)
	}
	for _, s := range resp.Series {
		if len(s.Points) != 0 {
			t.Errorf("expected no points for an idle queue's %s, got %+v", s.Metric, s.Points)
		}
	}
}

// TestGetQueueMetrics_ReturnsRecordedObservations proves the read side
// answers real recorded AWS/SQS data — a count series as Sum and a gauge
// series as its catalogue statistic.
func TestGetQueueMetrics_ReturnsRecordedObservations(t *testing.T) {
	h, svc, mock := newMonitorTestHandler(t)
	ctx := context.Background()
	now := mock.Now()
	dims := []metrics.Dimension{{Name: "QueueName", Value: "q-a"}}

	for i := 0; i < 3; i++ {
		if err := svc.Observe(ctx, metrics.Observation{Namespace: "AWS/SQS", Name: "NumberOfMessagesSent", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	if err := svc.Observe(ctx, metrics.Observation{Namespace: "AWS/SQS", Name: "ApproximateNumberOfMessagesVisible", Dimensions: dims, Unit: "Count", Value: 5, Timestamp: now}); err != nil {
		t.Fatalf("Observe gauge: %v", err)
	}

	rec := doGetQueueMetrics(t, h, "q-a", "1h")
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

	sent, ok := byKey["NumberOfMessagesSent/Sum"]
	if !ok || len(sent.Points) != 1 || sent.Points[0].Value != 3 {
		t.Fatalf("expected NumberOfMessagesSent/Sum=3, got %+v (present=%v)", sent, ok)
	}
	visible, ok := byKey["ApproximateNumberOfMessagesVisible/Average"]
	if !ok || len(visible.Points) != 1 || visible.Points[0].Value != 5 {
		t.Fatalf("expected ApproximateNumberOfMessagesVisible/Average=5, got %+v (present=%v)", visible, ok)
	}
}

// TestGetQueueMetrics_InvalidRangeIs400 pins that an unrecognized range token
// is rejected, not silently substituted.
func TestGetQueueMetrics_InvalidRangeIs400(t *testing.T) {
	h, _, _ := newMonitorTestHandler(t)
	rec := doGetQueueMetrics(t, h, "q-a", "3d")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestGetQueueMetrics_CollectionDisabledReportsEnabledFalse mirrors Lambda's
// equivalent test: a Handler with no metrics recorder wired answers
// {"enabled": false}, never a 500.
func TestGetQueueMetrics_CollectionDisabledReportsEnabledFalse(t *testing.T) {
	h := &Handler{clk: clock.NewMock()}
	rec := doGetQueueMetrics(t, h, "q-a", "")
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
