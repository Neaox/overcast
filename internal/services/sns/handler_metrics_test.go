package sns

// handler_metrics_test.go tests GetTopicMetrics (handler_metrics.go) against
// a real *metrics.Service — the same convention #1268's own metrics tests and
// phase 3/4's Lambda/SQS Monitor endpoint tests established: drive the real
// recorder, read back through the exact path the handler itself uses.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/metrics"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

func withTopicNameParam(req *http.Request, name string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("topicName", name)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func newSNSMonitorTestHandler(t *testing.T) (*Handler, *metrics.Service, *clock.Mock) {
	t.Helper()
	mock := clock.NewMock()
	svc := metrics.NewRecorder(state.NewMemoryStore(), mock, zap.NewNop())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	h := &Handler{clk: mock, metrics: svc, log: serviceutil.NewServiceLogger(zap.NewNop(), serviceName)}
	return h, svc, mock
}

func doGetTopicMetrics(t *testing.T, h *Handler, name, rangeToken string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/_overcast/sns/topics/" + name + "/metrics"
	if rangeToken != "" {
		url += "?range=" + rangeToken
	}
	req := withTopicNameParam(httptest.NewRequest(http.MethodGet, url, nil), name)
	rec := httptest.NewRecorder()
	h.GetTopicMetrics(rec, req)
	return rec
}

// TestGetTopicMetrics_NoDataIsEmptyNotError pins the Monitor tab's "No metric
// data in this range" state: a topic that has never published gets a 200
// with every catalogue series present but empty, never an error.
func TestGetTopicMetrics_NoDataIsEmptyNotError(t *testing.T) {
	h, _, _ := newSNSMonitorTestHandler(t)
	rec := doGetTopicMetrics(t, h, "never-published", "")

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
	// Published (Sum) + PublishSize (Average, Maximum) + Delivered (Sum) +
	// Failed (Sum) = 5 series.
	if len(resp.Series) != 5 {
		t.Fatalf("expected 5 catalogue series, got %d: %+v", len(resp.Series), resp.Series)
	}
	for _, s := range resp.Series {
		if len(s.Points) != 0 {
			t.Errorf("expected no points for a never-published topic's %s/%s, got %+v", s.Metric, s.Statistic, s.Points)
		}
	}
}

// TestGetTopicMetrics_ReturnsRecordedObservations proves the read side
// answers real recorded data through the exact *metrics.Service a real
// Publish/fan-out would have recorded through.
func TestGetTopicMetrics_ReturnsRecordedObservations(t *testing.T) {
	h, svc, mock := newSNSMonitorTestHandler(t)
	ctx := context.Background()
	now := mock.Now()
	dims := []metrics.Dimension{{Name: "TopicName", Value: "topic-a"}}

	if err := svc.Observe(ctx, metrics.Observation{Namespace: "AWS/SNS", Name: "NumberOfMessagesPublished", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
		t.Fatalf("Observe NumberOfMessagesPublished: %v", err)
	}
	if err := svc.Observe(ctx, metrics.Observation{Namespace: "AWS/SNS", Name: "NumberOfNotificationsDelivered", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
		t.Fatalf("Observe NumberOfNotificationsDelivered: %v", err)
	}
	if err := svc.Observe(ctx, metrics.Observation{Namespace: "AWS/SNS", Name: "NumberOfNotificationsFailed", Dimensions: dims, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
		t.Fatalf("Observe NumberOfNotificationsFailed: %v", err)
	}

	rec := doGetTopicMetrics(t, h, "topic-a", "1h")
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

	if pub, ok := byKey["NumberOfMessagesPublished/Sum"]; !ok || len(pub.Points) != 1 || pub.Points[0].Value != 1 {
		t.Fatalf("expected NumberOfMessagesPublished/Sum=1, got %+v (present=%v)", pub, ok)
	}
	if del, ok := byKey["NumberOfNotificationsDelivered/Sum"]; !ok || len(del.Points) != 1 || del.Points[0].Value != 1 {
		t.Fatalf("expected NumberOfNotificationsDelivered/Sum=1, got %+v (present=%v)", del, ok)
	}
	if fail, ok := byKey["NumberOfNotificationsFailed/Sum"]; !ok || len(fail.Points) != 1 || fail.Points[0].Value != 1 {
		t.Fatalf("expected NumberOfNotificationsFailed/Sum=1, got %+v (present=%v)", fail, ok)
	}
}

// TestGetTopicMetrics_InvalidRangeIs400 pins that an unrecognized range token
// is rejected, not silently substituted.
func TestGetTopicMetrics_InvalidRangeIs400(t *testing.T) {
	h, _, _ := newSNSMonitorTestHandler(t)
	rec := doGetTopicMetrics(t, h, "topic-a", "3d")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestGetTopicMetrics_CollectionDisabledReportsEnabledFalse pins the
// disabled-collection contract: a Handler whose metrics field is nil answers
// {"enabled": false}, never a 500 or a fake empty chart.
func TestGetTopicMetrics_CollectionDisabledReportsEnabledFalse(t *testing.T) {
	h := &Handler{clk: clock.NewMock(), log: serviceutil.NewServiceLogger(zap.NewNop(), serviceName)}
	rec := doGetTopicMetrics(t, h, "topic-a", "")
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
