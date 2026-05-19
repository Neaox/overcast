package eks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

const (
	liveModeTestRegion    = "us-east-1"
	legacyMockClusterName = "legacy-mock"
)

func newLiveModeTestService() *Service {
	return New(
		&config.Config{Region: liveModeTestRegion, AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)
}

func putLegacyMockCluster(t *testing.T, svc *Service) {
	t.Helper()

	if err := svc.putCluster(context.Background(), liveModeTestRegion, &Cluster{
		Name:      legacyMockClusterName,
		Arn:       svc.clusterARN(liveModeTestRegion, legacyMockClusterName),
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("put legacy mock cluster: %v", err)
	}
}

func putLiveClusterRecord(t *testing.T, svc *Service, name string) {
	t.Helper()

	if err := svc.putCluster(context.Background(), liveModeTestRegion, &Cluster{
		Name:      name,
		Arn:       svc.clusterARN(liveModeTestRegion, name),
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://127.0.0.1:6443",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("put live cluster record: %v", err)
	}
}

func expectLiveModeNotImplemented(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 for live-mode mixed record, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode not implemented response: %v", err)
	}
	if body["__type"] != "NotImplemented" {
		t.Fatalf("expected NotImplemented body for live-mode mixed record, got %#v", body)
	}
}

func expectServiceUnavailable(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 service unavailable, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode service unavailable response: %v", err)
	}
	if body["__type"] != "ServiceUnavailableException" {
		t.Fatalf("expected ServiceUnavailableException body, got %#v", body)
	}
}
