package eks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func TestLiveModeDeleteClusterAllowsLegacyMockCleanup(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodDelete, "/clusters/"+legacyMockClusterName, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 delete in live mode for legacy mock cleanup, got %d body=%s", rec.Code, rec.Body.String())
	}

	if _, found, err := svc.store.Get(context.Background(), nsClusters, clusterKey(liveModeTestRegion, legacyMockClusterName)); err != nil {
		t.Fatalf("get deleted cluster: %v", err)
	} else if found {
		t.Fatal("expected legacy mock cluster to be deleted in live mode cleanup path")
	}
}

func TestLiveModeListClustersFiltersMockRecords(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "mock-cluster",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/mock-cluster",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://mock-cluster.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster mock: %v", err)
	}

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "live-cluster",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/live-cluster",
		Status:    "CREATING",
		Version:   "1.31",
		Endpoint:  "",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster live: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	rawClusters, _ := body["clusters"].([]any)
	if len(rawClusters) != 1 {
		t.Fatalf("expected 1 live-visible cluster, got %d (%v)", len(rawClusters), rawClusters)
	}
	if got, _ := rawClusters[0].(string); got != "live-cluster" {
		t.Fatalf("expected only live-cluster in list, got %q", got)
	}
}

func TestMockModeListClustersIncludesMockRecords(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeMock},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "mock-cluster",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/mock-cluster",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://mock-cluster.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster mock: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	rawClusters, _ := body["clusters"].([]any)
	if len(rawClusters) != 1 {
		t.Fatalf("expected 1 cluster in mock mode, got %d (%v)", len(rawClusters), rawClusters)
	}
	if got, _ := rawClusters[0].(string); got != "mock-cluster" {
		t.Fatalf("expected mock-cluster in list, got %q", got)
	}
}

func TestLiveModeListUpdatesBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+legacyMockClusterName+"/updates", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeDescribeClusterBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+legacyMockClusterName, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListInsightsBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+legacyMockClusterName+"/insights", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeDescribeInsightBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+legacyMockClusterName+"/insights/platform-version-check", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeDescribeUpdateBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/"+legacyMockClusterName+"/updates/upd-123", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUpdateClusterVersionBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, _ := json.Marshal(map[string]any{"version": "1.32"})
	req := httptest.NewRequest(http.MethodPost, "/clusters/"+legacyMockClusterName+"/updates", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUpdateClusterConfigBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, _ := json.Marshal(map[string]any{
		"logging": map[string]any{"clusterLogging": []any{}},
	})
	req := httptest.NewRequest(http.MethodPost, "/clusters/"+legacyMockClusterName+"/update-config", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUpdateKubeconfigBlocksMockRecord(t *testing.T) {
	svc := newLiveModeTestService()
	putLegacyMockCluster(t, svc)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/"+legacyMockClusterName+"/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}
