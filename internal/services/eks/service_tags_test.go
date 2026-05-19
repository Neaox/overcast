package eks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func TestEKSClusterFromResourceARN(t *testing.T) {
	tests := []struct {
		name        string
		arn         string
		wantRegion  string
		wantCluster string
		wantOK      bool
	}{
		{
			name:        "cluster arn",
			arn:         "arn:aws:eks:us-east-1:000000000000:cluster/demo-cluster",
			wantRegion:  "us-east-1",
			wantCluster: "demo-cluster",
			wantOK:      true,
		},
		{
			name:        "nodegroup arn",
			arn:         "arn:aws:eks:us-east-1:000000000000:nodegroup/demo-cluster/workers-a/mock-ng",
			wantRegion:  "us-east-1",
			wantCluster: "demo-cluster",
			wantOK:      true,
		},
		{
			name:        "fargateprofile arn",
			arn:         "arn:aws:eks:us-east-1:000000000000:fargateprofile/demo-cluster/fp-1/mock-fargate",
			wantRegion:  "us-east-1",
			wantCluster: "demo-cluster",
			wantOK:      true,
		},
		{
			name:        "addon arn",
			arn:         "arn:aws:eks:us-east-1:000000000000:addon/demo-cluster/coredns/mock-addon",
			wantRegion:  "us-east-1",
			wantCluster: "demo-cluster",
			wantOK:      true,
		},
		{
			name:        "identity provider config arn",
			arn:         "arn:aws:eks:us-east-1:000000000000:identityproviderconfig/demo-cluster/oidc/okta-main",
			wantRegion:  "us-east-1",
			wantCluster: "demo-cluster",
			wantOK:      true,
		},
		{
			name:        "access entry arn",
			arn:         "arn:aws:eks:us-east-1:000000000000:access-entry/demo-cluster/arn%3Aaws%3Aiam%3A%3A000000000000%3Arole%2Fdev",
			wantRegion:  "us-east-1",
			wantCluster: "demo-cluster",
			wantOK:      true,
		},
		{
			name:        "pod identity arn",
			arn:         "arn:aws:eks:us-east-1:000000000000:podidentityassociation/demo-cluster/pia-123",
			wantRegion:  "us-east-1",
			wantCluster: "demo-cluster",
			wantOK:      true,
		},
		{
			name:   "non eks arn",
			arn:    "arn:aws:s3:::example-bucket",
			wantOK: false,
		},
		{
			name:   "malformed eks arn",
			arn:    "arn:aws:eks:us-east-1:000000000000:addon/",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotRegion, gotCluster, gotOK := eksClusterFromResourceARN(tc.arn)
			if gotOK != tc.wantOK {
				t.Fatalf("expected ok=%v, got %v", tc.wantOK, gotOK)
			}
			if gotRegion != tc.wantRegion {
				t.Fatalf("expected region %q, got %q", tc.wantRegion, gotRegion)
			}
			if gotCluster != tc.wantCluster {
				t.Fatalf("expected cluster %q, got %q", tc.wantCluster, gotCluster)
			}
		})
	}
}

func TestLiveModeListTagsBlocksLegacyMockClusterARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock")
	req := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListTagsBlocksLegacyMockNodegroupARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:nodegroup/legacy-mock/workers-a/mock-ng")
	req := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagBlocksLegacyMockNodegroupARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{"tags": map[string]string{"env": "live"}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:nodegroup/legacy-mock/workers-a/mock-ng")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagBlocksLegacyMockNodegroupARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:nodegroup/legacy-mock/workers-a/mock-ng")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn+"?tagKeys=env", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagLegacyMockNodegroupMalformedRequestStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:nodegroup/legacy-mock/workers-a/mock-ng")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagLegacyMockNodegroupMissingTagKeysStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:nodegroup/legacy-mock/workers-a/mock-ng")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListTagsBlocksLegacyMockFargateProfileARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:fargateprofile/legacy-mock/fp-1/mock-fargate")
	req := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagBlocksLegacyMockFargateProfileARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{"tags": map[string]string{"env": "live"}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:fargateprofile/legacy-mock/fp-1/mock-fargate")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagBlocksLegacyMockFargateProfileARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:fargateprofile/legacy-mock/fp-1/mock-fargate")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn+"?tagKeys=env", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagLegacyMockFargateProfileARNMalformedRequestStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:fargateprofile/legacy-mock/fp-1/mock-fargate")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagLegacyMockFargateProfileARNMissingTagKeysStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:fargateprofile/legacy-mock/fp-1/mock-fargate")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagBlocksLegacyMockAddonARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{"tags": map[string]string{"env": "live"}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:addon/legacy-mock/coredns/mock-addon")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListTagsBlocksLegacyMockAddonARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:addon/legacy-mock/coredns/mock-addon")
	req := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagBlocksLegacyMockAddonARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:addon/legacy-mock/coredns/mock-addon")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn+"?tagKeys=env", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagLegacyMockAddonARNMalformedRequestStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:addon/legacy-mock/coredns/mock-addon")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagLegacyMockAddonARNMissingTagKeysStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:addon/legacy-mock/coredns/mock-addon")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagBlocksLegacyMockClusterARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{"tags": map[string]string{"env": "live"}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListTagsBlocksLegacyMockIdentityProviderConfigARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:identityproviderconfig/legacy-mock/oidc/okta-main")
	req := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagBlocksLegacyMockIdentityProviderConfigARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{"tags": map[string]string{"env": "live"}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:identityproviderconfig/legacy-mock/oidc/okta-main")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagLegacyMockIdentityProviderConfigARNMalformedRequestStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:identityproviderconfig/legacy-mock/oidc/okta-main")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagLegacyMockIdentityProviderConfigARNMissingTagKeysStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:identityproviderconfig/legacy-mock/oidc/okta-main")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagBlocksLegacyMockAccessEntryARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{"tags": map[string]string{"env": "live"}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:access-entry/legacy-mock/arn%3Aaws%3Aiam%3A%3A000000000000%3Arole%2Fdev")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagLegacyMockAccessEntryARNMalformedRequestStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:access-entry/legacy-mock/arn%3Aaws%3Aiam%3A%3A000000000000%3Arole%2Fdev")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagLegacyMockAccessEntryARNMissingTagKeysStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:access-entry/legacy-mock/arn%3Aaws%3Aiam%3A%3A000000000000%3Arole%2Fdev")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListTagsBlocksLegacyMockAccessEntryARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:access-entry/legacy-mock/arn%3Aaws%3Aiam%3A%3A000000000000%3Arole%2Fdev")
	req := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListTagsAllowsNonEKSARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	ctx := context.Background()
	raw, err := json.Marshal(map[string]string{"env": "live"})
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	if err := svc.store.Set(ctx, nsTags, tagKey("arn:aws:s3:::example-bucket"), string(raw)); err != nil {
		t.Fatalf("seed non-EKS tags: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:s3:::example-bucket")
	req := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-EKS tag read in live mode, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	tags, _ := body["tags"].(map[string]any)
	if tags["env"] != "live" {
		t.Fatalf("expected env=live tag for non-EKS ARN, got %#v", body)
	}
}

func TestLiveModeTagAllowsNonEKSARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{"tags": map[string]string{"env": "live"}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:s3:::example-bucket")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-EKS tag mutation in live mode, got %d body=%s", rec.Code, rec.Body.String())
	}

	verifyReq := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	verifyRec := httptest.NewRecorder()
	r.ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for verifying non-EKS tags, got %d body=%s", verifyRec.Code, verifyRec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(verifyRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode verification response: %v", err)
	}
	tags, _ := body["tags"].(map[string]any)
	if tags["env"] != "live" {
		t.Fatalf("expected env tag after non-EKS tag mutation, got %#v", body)
	}
}

func TestLiveModeUntagBlocksLegacyMockPodIdentityAssociationARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:podidentityassociation/legacy-mock/pia-123")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn+"?tagKeys=env", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeListTagsBlocksLegacyMockPodIdentityAssociationARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:podidentityassociation/legacy-mock/pia-123")
	req := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagBlocksLegacyMockPodIdentityAssociationARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{"tags": map[string]string{"env": "live"}})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:podidentityassociation/legacy-mock/pia-123")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagLegacyMockPodIdentityAssociationARNMalformedRequestStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:podidentityassociation/legacy-mock/pia-123")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagLegacyMockPodIdentityAssociationARNMissingTagKeysStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:podidentityassociation/legacy-mock/pia-123")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagBlocksLegacyMockClusterARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn+"?tagKeys=env", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeTagLegacyMockClusterMalformedRequestStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock")
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagLegacyMockClusterMissingTagKeysStillReturnsNotImplemented(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagBlocksLegacyMockIdentityProviderConfigARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:identityproviderconfig/legacy-mock/oidc/okta-main")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn+"?tagKeys=env", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagBlocksLegacyMockAccessEntryARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "legacy-mock",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/legacy-mock",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://legacy-mock.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:access-entry/legacy-mock/arn%3Aaws%3Aiam%3A%3A000000000000%3Arole%2Fdev")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn+"?tagKeys=env", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectLiveModeNotImplemented(t, rec)
}

func TestLiveModeUntagAllowsNonEKSARN(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	ctx := context.Background()
	raw, err := json.Marshal(map[string]string{"env": "live", "owner": "ci"})
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	if err := svc.store.Set(ctx, nsTags, tagKey("arn:aws:s3:::example-bucket"), string(raw)); err != nil {
		t.Fatalf("seed non-EKS tags: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:s3:::example-bucket")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn+"?tagKeys=owner", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-EKS untag in live mode, got %d body=%s", rec.Code, rec.Body.String())
	}

	verifyReq := httptest.NewRequest(http.MethodGet, "/tags/"+arn, nil)
	verifyRec := httptest.NewRecorder()
	r.ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for verifying remaining non-EKS tags, got %d body=%s", verifyRec.Code, verifyRec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(verifyRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode verification response: %v", err)
	}
	tags, _ := body["tags"].(map[string]any)
	if tags["env"] != "live" {
		t.Fatalf("expected env tag to remain after non-EKS untag, got %#v", body)
	}
	if _, exists := tags["owner"]; exists {
		t.Fatalf("expected owner tag removed after non-EKS untag, got %#v", body)
	}
}

func TestTagResourceRejectsEmptyTagsMap(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "demo-cluster",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/demo-cluster",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://demo-cluster.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:cluster/demo-cluster")
	payload, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/tags/"+arn, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty tags map, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["__type"] != "InvalidParameterException" {
		t.Fatalf("expected InvalidParameterException for empty tags map, got %#v", body)
	}
	if body["message"] != "tags map must not be empty" {
		t.Fatalf("unexpected empty tags map message: %#v", body)
	}
}

func TestUntagResourceRejectsMissingTagKeys(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "demo-cluster",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/demo-cluster",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://demo-cluster.mock.eks.local",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	arn := url.PathEscape("arn:aws:eks:us-east-1:000000000000:cluster/demo-cluster")
	req := httptest.NewRequest(http.MethodDelete, "/tags/"+arn, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing tagKeys, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["__type"] != "InvalidParameterException" {
		t.Fatalf("expected InvalidParameterException for missing tagKeys, got %#v", body)
	}
	if body["message"] != "at least one tagKeys query parameter is required" {
		t.Fatalf("unexpected missing tagKeys message: %#v", body)
	}
}
