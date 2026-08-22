package dynamodb

// handler_metrics_test.go tests GetTableMetrics (handler_metrics.go) against
// a real *metrics.Service, following the convention every other service's
// Monitor endpoint test uses: drive the real recorder, read back through the
// exact path the handler itself uses. Exercises MonitorCatalogEntry's
// ExtraDimensions field (internal/metrics/monitor.go) end to end —
// ConsumedWriteCapacityUnits' series is only discoverable because
// BuildMonitorResponse appends the catalogue's "Source=Customer" dimension
// on top of the base TableName dimension.
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

func withTableNameParam(req *http.Request, name string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", name)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func newDDBMonitorTestHandler(t *testing.T) (*Handler, *metrics.Service, *clock.Mock) {
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

func doGetTableMetrics(t *testing.T, h *Handler, name, rangeToken string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/_overcast/dynamodb/tables/" + name + "/metrics"
	if rangeToken != "" {
		url += "?range=" + rangeToken
	}
	req := withTableNameParam(httptest.NewRequest(http.MethodGet, url, nil), name)
	rec := httptest.NewRecorder()
	h.GetTableMetrics(rec, req)
	return rec
}

// TestGetTableMetrics_NoDataIsEmptyNotError pins the Monitor tab's "No metric
// data in this range" state.
func TestGetTableMetrics_NoDataIsEmptyNotError(t *testing.T) {
	h, _, _ := newDDBMonitorTestHandler(t)
	rec := doGetTableMetrics(t, h, "never-used", "")

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
	if len(resp.Series) != 2 {
		t.Fatalf("expected 2 catalogue series (read + write capacity), got %d: %+v", len(resp.Series), resp.Series)
	}
	for _, s := range resp.Series {
		if len(s.Points) != 0 {
			t.Errorf("expected no points for a never-used table's %s/%s, got %+v", s.Metric, s.Statistic, s.Points)
		}
	}
}

// TestGetTableMetrics_ReturnsRecordedObservations proves the read side finds
// both the TableName-only ConsumedReadCapacityUnits series and the
// TableName+Source ConsumedWriteCapacityUnits series — the exact dimension
// sets recordConsumedReadCapacity/recordConsumedWriteCapacity record through
// in real operation traffic (metrics_dynamodb.go), not a simplification.
func TestGetTableMetrics_ReturnsRecordedObservations(t *testing.T) {
	h, svc, mock := newDDBMonitorTestHandler(t)
	ctx := context.Background()
	now := mock.Now()

	readDims := []metrics.Dimension{{Name: "TableName", Value: "orders"}}
	writeDims := []metrics.Dimension{{Name: "TableName", Value: "orders"}, {Name: "Source", Value: "Customer"}}

	if err := svc.Observe(ctx, metrics.Observation{Namespace: "AWS/DynamoDB", Name: "ConsumedReadCapacityUnits", Dimensions: readDims, Unit: "Count", Value: 1, Timestamp: now}); err != nil {
		t.Fatalf("Observe ConsumedReadCapacityUnits: %v", err)
	}
	if err := svc.Observe(ctx, metrics.Observation{Namespace: "AWS/DynamoDB", Name: "ConsumedWriteCapacityUnits", Dimensions: writeDims, Unit: "Count", Value: 2, Timestamp: now}); err != nil {
		t.Fatalf("Observe ConsumedWriteCapacityUnits: %v", err)
	}

	rec := doGetTableMetrics(t, h, "orders", "1h")
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

	if read, ok := byKey["ConsumedReadCapacityUnits/Sum"]; !ok || len(read.Points) != 1 || read.Points[0].Value != 1 {
		t.Fatalf("expected ConsumedReadCapacityUnits/Sum=1, got %+v (present=%v)", read, ok)
	}
	if write, ok := byKey["ConsumedWriteCapacityUnits/Sum"]; !ok || len(write.Points) != 1 || write.Points[0].Value != 2 {
		t.Fatalf("expected ConsumedWriteCapacityUnits/Sum=2 (found via the Source=Customer ExtraDimensions match), got %+v (present=%v)", write, ok)
	}
}

// TestGetTableMetrics_InvalidRangeIs400 pins that an unrecognized range token
// is rejected, not silently substituted.
func TestGetTableMetrics_InvalidRangeIs400(t *testing.T) {
	h, _, _ := newDDBMonitorTestHandler(t)
	rec := doGetTableMetrics(t, h, "orders", "3d")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestGetTableMetrics_CollectionDisabledReportsEnabledFalse pins the
// disabled-collection contract.
func TestGetTableMetrics_CollectionDisabledReportsEnabledFalse(t *testing.T) {
	h := &Handler{clk: clock.NewMock(), log: serviceutil.NewServiceLogger(zap.NewNop(), serviceName)}
	rec := doGetTableMetrics(t, h, "orders", "")
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
