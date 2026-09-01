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

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
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

// suspendLiveBootstrap replaces the background bootstrap CreateCluster hands
// off to with a stand-in that does nothing, so a test can assert on the record
// CreateCluster just persisted instead of racing the goroutine.
//
// A bootstrap that cannot start a control plane moves the cluster to FAILED
// (#895), so an assertion of CREATING made after CreateCluster returns is
// otherwise decided by which of the two goroutines the scheduler runs first.
// What the stand-in displaces is covered elsewhere, deterministically:
// the hand-off itself by TestLiveModeCreateClusterStartsK3sContainer, and the
// terminal states by the TestLiveCluster*ReachesTerminalState tests, which call
// startLiveCluster synchronously.
func suspendLiveBootstrap(t *testing.T, svc *Service) {
	t.Helper()

	svc.startLiveClusterHook = func(context.Context, string, *Cluster) {}
}

// createLiveCluster drives CreateCluster over its route, the way a client
// reaches it — so the bootstrap hand-off happens too, unlike putCreatingCluster,
// which only lays down the record CreateCluster would have written.
func createLiveCluster(t *testing.T, svc *Service, name string) {
	t.Helper()

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{
		"name":    name,
		"roleArn": "arn:aws:iam::000000000000:role/eks-role",
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for live-mode create of %q, got %d body=%s", name, rec.Code, rec.Body.String())
	}
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
