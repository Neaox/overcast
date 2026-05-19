package eks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/state"
)

func TestLiveModeUpdateKubeconfigReturnsKubeconfigWhenClusterReady(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	cluster := &Cluster{
		Name:      "live-ready",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/live-ready",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://overcast.local:16443",
		CreatedAt: time.Now(),
		CertificateAuthority: map[string]any{
			"data": "ZmFrZS1jYS1kYXRh",
		},
	}
	if err := svc.putCluster(context.Background(), "us-east-1", cluster); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/live-ready/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	kubeconfig, _ := body["kubeconfig"].(string)
	if !strings.Contains(kubeconfig, "server: https://overcast.local:16443") {
		t.Fatalf("expected kubeconfig to include live endpoint, got %q", kubeconfig)
	}
	if !strings.Contains(kubeconfig, "certificate-authority-data: ZmFrZS1jYS1kYXRh") {
		t.Fatalf("expected kubeconfig to include CA data, got %q", kubeconfig)
	}
}

func TestLiveModeUpdateKubeconfigReturnsUnavailableWhenClusterNotReady(t *testing.T) {
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)

	cluster := &Cluster{
		Name:                 "live-creating",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/live-creating",
		Status:               "CREATING",
		Version:              "1.31",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}
	if err := svc.putCluster(context.Background(), "us-east-1", cluster); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/live-creating/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	expectServiceUnavailable(t, rec)
}

func TestLiveModeUpdateKubeconfigBackfillsCAFromRuntime(t *testing.T) {
	const expectedCAData = "Q0EtQkFDS0ZJTEw="
	k3sYAMLArchive := tarArchiveWithSingleFile(t, "k3s.yaml", "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: "+expectedCAData+"\n")

	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/k3s-backfill-ctr/archive":
			if gotPath := r.URL.Query().Get("path"); gotPath != "/etc/rancher/k3s/k3s.yaml" {
				t.Fatalf("expected archive path /etc/rancher/k3s/k3s.yaml, got %q", gotPath)
			}
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(k3sYAMLArchive)
		default:
			t.Logf("unexpected docker request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	cluster := &Cluster{
		Name:                 "live-backfill",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/live-backfill",
		Status:               "ACTIVE",
		Version:              "1.31",
		Endpoint:             "https://overcast.local:17443",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}
	if err := svc.putCluster(context.Background(), "us-east-1", cluster); err != nil {
		t.Fatalf("putCluster: %v", err)
	}
	svc.setLiveClusterRuntime("us-east-1", "live-backfill", &liveClusterRuntime{containerID: "k3s-backfill-ctr"})

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/live-backfill/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	kubeconfig, _ := body["kubeconfig"].(string)
	if !strings.Contains(kubeconfig, "certificate-authority-data: "+expectedCAData) {
		t.Fatalf("expected kubeconfig to include backfilled CA data, got %q", kubeconfig)
	}

	persisted, found, err := svc.getCluster(context.Background(), "us-east-1", "live-backfill")
	if err != nil {
		t.Fatalf("getCluster: %v", err)
	}
	if !found {
		t.Fatal("expected cluster to remain present")
	}
	persistedCAData, _ := persisted.CertificateAuthority["data"].(string)
	if persistedCAData != expectedCAData {
		t.Fatalf("expected persisted CA data %q, got %q", expectedCAData, persistedCAData)
	}
}

func TestLiveModeUpdateKubeconfigBackfillsCAWhenCachedRuntimeIDIsStale(t *testing.T) {
	const expectedCAData = "Q0EtQkFDS0ZJTEwtU1RBTEUtSUQ="
	k3sYAMLArchive := tarArchiveWithSingleFile(t, "k3s.yaml", "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: "+expectedCAData+"\n")

	requests := make([]string, 0, 3)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/stale-k3s-id/json":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-live-backfill-stale-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "k3s-backfill-stale-id-ctr",
				"Name": "/overcast-eks-live-backfill-stale-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "live-backfill-stale-id"),
				},
				"State": map[string]any{"Running": true},
				"NetworkSettings": map[string]any{
					"Ports": map[string]any{
						"6443/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "1"}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/k3s-backfill-stale-id-ctr/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "k3s-backfill-stale-id-ctr",
				"Name": "/overcast-eks-live-backfill-stale-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "live-backfill-stale-id"),
				},
				"State": map[string]any{"Running": true},
				"NetworkSettings": map[string]any{
					"Ports": map[string]any{
						"6443/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "1"}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/k3s-backfill-stale-id-ctr/archive":
			if gotPath := r.URL.Query().Get("path"); gotPath != "/etc/rancher/k3s/k3s.yaml" {
				t.Fatalf("expected archive path /etc/rancher/k3s/k3s.yaml, got %q", gotPath)
			}
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(k3sYAMLArchive)
		default:
			t.Fatalf("unexpected docker request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	cluster := &Cluster{
		Name:                 "live-backfill-stale-id",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/live-backfill-stale-id",
		Status:               "ACTIVE",
		Version:              "1.31",
		Endpoint:             "https://overcast.local:17443",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}
	if err := svc.putCluster(context.Background(), "us-east-1", cluster); err != nil {
		t.Fatalf("putCluster: %v", err)
	}
	svc.setLiveClusterRuntime("us-east-1", "live-backfill-stale-id", &liveClusterRuntime{containerID: "stale-k3s-id"})

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/live-backfill-stale-id/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	kubeconfig, _ := body["kubeconfig"].(string)
	if !strings.Contains(kubeconfig, "certificate-authority-data: "+expectedCAData) {
		t.Fatalf("expected kubeconfig to include backfilled CA data, got %q", kubeconfig)
	}

	if len(requests) != 4 {
		t.Fatalf("expected stale-inspect + name-inspect + runtime-inspect + archive docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/stale-k3s-id/json" {
		t.Fatalf("expected initial stale runtime inspect attempt, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/overcast-eks-live-backfill-stale-id/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[1])
	}
	if got := requestPathWithoutQuery(t, requests[2]); got != "/v1.45/containers/k3s-backfill-stale-id-ctr/json" {
		t.Fatalf("expected inspect request for refreshed runtime ID, got %q", requests[2])
	}
	if got := requestPathWithoutQuery(t, requests[3]); got != "/v1.45/containers/k3s-backfill-stale-id-ctr/archive" {
		t.Fatalf("expected archive request for reconciled runtime container, got %q", requests[3])
	}

	persisted, found, err := svc.getCluster(context.Background(), "us-east-1", "live-backfill-stale-id")
	if err != nil {
		t.Fatalf("getCluster: %v", err)
	}
	if !found {
		t.Fatal("expected cluster to remain present")
	}
	persistedCAData, _ := persisted.CertificateAuthority["data"].(string)
	if persistedCAData != expectedCAData {
		t.Fatalf("expected persisted CA data %q, got %q", expectedCAData, persistedCAData)
	}
}

func TestLiveModeUpdateKubeconfigBackfillsCAWhenCachedRuntimeIDIsBlank(t *testing.T) {
	const expectedCAData = "Q0EtQkFDS0ZJTEwtQkxBTkstSUQ="
	k3sYAMLArchive := tarArchiveWithSingleFile(t, "k3s.yaml", "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: "+expectedCAData+"\n")

	requests := make([]string, 0, 2)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-live-backfill-blank-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "k3s-backfill-blank-id-ctr",
				"Name": "/overcast-eks-live-backfill-blank-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "live-backfill-blank-id"),
				},
				"State": map[string]any{"Running": true},
				"NetworkSettings": map[string]any{
					"Ports": map[string]any{
						"6443/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "1"}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/k3s-backfill-blank-id-ctr/archive":
			if gotPath := r.URL.Query().Get("path"); gotPath != "/etc/rancher/k3s/k3s.yaml" {
				t.Fatalf("expected archive path /etc/rancher/k3s/k3s.yaml, got %q", gotPath)
			}
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(k3sYAMLArchive)
		default:
			t.Fatalf("unexpected docker request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	cluster := &Cluster{
		Name:                 "live-backfill-blank-id",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/live-backfill-blank-id",
		Status:               "ACTIVE",
		Version:              "1.31",
		Endpoint:             "https://overcast.local:17443",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}
	if err := svc.putCluster(context.Background(), "us-east-1", cluster); err != nil {
		t.Fatalf("putCluster: %v", err)
	}
	// Cached runtime entry exists but container ID is blank.
	svc.setLiveClusterRuntime("us-east-1", "live-backfill-blank-id", &liveClusterRuntime{})

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/live-backfill-blank-id/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	kubeconfig, _ := body["kubeconfig"].(string)
	if !strings.Contains(kubeconfig, "certificate-authority-data: "+expectedCAData) {
		t.Fatalf("expected kubeconfig to include backfilled CA data, got %q", kubeconfig)
	}

	if len(requests) != 2 {
		t.Fatalf("expected name-inspect + archive docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-live-backfill-blank-id/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/k3s-backfill-blank-id-ctr/archive" {
		t.Fatalf("expected archive request for reconciled runtime container, got %q", requests[1])
	}

	persisted, found, err := svc.getCluster(context.Background(), "us-east-1", "live-backfill-blank-id")
	if err != nil {
		t.Fatalf("getCluster: %v", err)
	}
	if !found {
		t.Fatal("expected cluster to remain present")
	}
	persistedCAData, _ := persisted.CertificateAuthority["data"].(string)
	if persistedCAData != expectedCAData {
		t.Fatalf("expected persisted CA data %q, got %q", expectedCAData, persistedCAData)
	}
}

func TestLiveModeUpdateKubeconfigReconcilesRuntimeAfterRestart(t *testing.T) {
	const expectedCAData = "UkVTVEFSVC1DQS1CQUNLRklMTA=="
	k3sYAMLArchive := tarArchiveWithSingleFile(t, "k3s.yaml", "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: "+expectedCAData+"\n")

	requests := make([]string, 0, 2)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-live-restart-backfill/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-backfill-ctr",
				"Name": "/overcast-eks-live-restart-backfill",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "live-restart-backfill"),
				},
				"State": map[string]any{"Running": true},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/restart-backfill-ctr/archive":
			if gotPath := r.URL.Query().Get("path"); gotPath != "/etc/rancher/k3s/k3s.yaml" {
				t.Fatalf("expected archive path /etc/rancher/k3s/k3s.yaml, got %q", gotPath)
			}
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(k3sYAMLArchive)
		default:
			t.Fatalf("unexpected docker request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	store := state.NewMemoryStore()

	seed := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	if err := seed.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:                 "live-restart-backfill",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/live-restart-backfill",
		Status:               "ACTIVE",
		Version:              "1.31",
		Endpoint:             "https://overcast.local:17443",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate a restarted process: the cluster record persists, but the new
	// service instance has no in-memory runtime bookkeeping yet.
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/live-restart-backfill/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after restart runtime reconciliation, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	kubeconfig, _ := body["kubeconfig"].(string)
	if !strings.Contains(kubeconfig, "certificate-authority-data: "+expectedCAData) {
		t.Fatalf("expected kubeconfig to include reconciled CA data, got %q", kubeconfig)
	}

	if len(requests) != 2 {
		t.Fatalf("expected inspect + archive docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-live-restart-backfill/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/restart-backfill-ctr/archive" {
		t.Fatalf("expected archive request for reconciled runtime container, got %q", requests[1])
	}

	persisted, found, err := svc.getCluster(context.Background(), "us-east-1", "live-restart-backfill")
	if err != nil {
		t.Fatalf("getCluster: %v", err)
	}
	if !found {
		t.Fatal("expected cluster to remain present")
	}
	persistedCAData, _ := persisted.CertificateAuthority["data"].(string)
	if persistedCAData != expectedCAData {
		t.Fatalf("expected persisted CA data %q, got %q", expectedCAData, persistedCAData)
	}
}

func TestLiveModeUpdateKubeconfigReconcilesReadyClusterAfterRestart(t *testing.T) {
	const expectedCAData = "UkVTVEFSVC1LRUNPTkZJRy1SRUFEWS1DQQ=="
	k3sYAMLArchive := tarArchiveWithSingleFile(t, "k3s.yaml", "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: "+expectedCAData+"\n")

	readyzSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer readyzSrv.Close()

	readyzURL, _ := url.Parse(readyzSrv.URL)
	readyzPort := readyzURL.Port()

	requests := make([]string, 0, 2)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-live-restart-ready-kubeconfig/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-ready-kubeconfig-ctr",
				"Name": "/overcast-eks-live-restart-ready-kubeconfig",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "live-restart-ready-kubeconfig"),
				},
				"State": map[string]any{"Running": true},
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"overcast_eks": map[string]any{"IPAddress": "172.17.0.2"},
					},
					"Ports": map[string]any{
						"6443/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": readyzPort}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/restart-ready-kubeconfig-ctr/archive":
			if gotPath := r.URL.Query().Get("path"); gotPath != "/etc/rancher/k3s/k3s.yaml" {
				t.Fatalf("expected archive path /etc/rancher/k3s/k3s.yaml, got %q", gotPath)
			}
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(k3sYAMLArchive)
		default:
			t.Fatalf("unexpected docker request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	store := state.NewMemoryStore()

	seed := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Hostname: "overcast.local", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	if err := seed.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:                 "live-restart-ready-kubeconfig",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/live-restart-ready-kubeconfig",
		Status:               "CREATING",
		Version:              "1.31",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate a restarted process: the persisted cluster is stale but the
	// managed k3s runtime is already ready and discoverable via Docker.
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Hostname: "overcast.local", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/live-restart-ready-kubeconfig/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after restart readiness reconciliation, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	kubeconfig, _ := body["kubeconfig"].(string)
	wantEndpoint := "https://overcast.local:" + readyzPort
	if !strings.Contains(kubeconfig, "server: "+wantEndpoint) {
		t.Fatalf("expected kubeconfig to include reconciled endpoint %q, got %q", wantEndpoint, kubeconfig)
	}
	if !strings.Contains(kubeconfig, "certificate-authority-data: "+expectedCAData) {
		t.Fatalf("expected kubeconfig to include reconciled CA data, got %q", kubeconfig)
	}

	if len(requests) != 2 {
		t.Fatalf("expected inspect + archive docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-live-restart-ready-kubeconfig/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/restart-ready-kubeconfig-ctr/archive" {
		t.Fatalf("expected archive request for reconciled ready runtime, got %q", requests[1])
	}

	persisted, found, err := svc.getCluster(context.Background(), "us-east-1", "live-restart-ready-kubeconfig")
	if err != nil {
		t.Fatalf("getCluster: %v", err)
	}
	if !found {
		t.Fatal("expected cluster to remain present")
	}
	if persisted.Status != "ACTIVE" {
		t.Fatalf("expected persisted cluster to reconcile to ACTIVE, got %q", persisted.Status)
	}
	if persisted.Endpoint != wantEndpoint {
		t.Fatalf("expected persisted endpoint %q, got %q", wantEndpoint, persisted.Endpoint)
	}
	persistedCAData, _ := persisted.CertificateAuthority["data"].(string)
	if persistedCAData != expectedCAData {
		t.Fatalf("expected persisted CA data %q, got %q", expectedCAData, persistedCAData)
	}
}

func TestLiveModeUpdateKubeconfigReconcilesWhenCachedRuntimeIDMissing(t *testing.T) {
	const expectedCAData = "UkVTVEFSVC1LRUNPTkZJRy1FTVBUWS1JRC1DQQ=="
	k3sYAMLArchive := tarArchiveWithSingleFile(t, "k3s.yaml", "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: "+expectedCAData+"\n")

	readyzSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer readyzSrv.Close()

	readyzURL, _ := url.Parse(readyzSrv.URL)
	readyzPort := readyzURL.Port()

	requests := make([]string, 0, 2)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-live-restart-ready-kubeconfig-empty-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-ready-kubeconfig-empty-id-ctr",
				"Name": "/overcast-eks-live-restart-ready-kubeconfig-empty-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "live-restart-ready-kubeconfig-empty-id"),
				},
				"State": map[string]any{"Running": true},
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"overcast_eks": map[string]any{"IPAddress": "172.17.0.2"},
					},
					"Ports": map[string]any{
						"6443/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": readyzPort}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/restart-ready-kubeconfig-empty-id-ctr/archive":
			if gotPath := r.URL.Query().Get("path"); gotPath != "/etc/rancher/k3s/k3s.yaml" {
				t.Fatalf("expected archive path /etc/rancher/k3s/k3s.yaml, got %q", gotPath)
			}
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(k3sYAMLArchive)
		default:
			t.Fatalf("unexpected docker request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	store := state.NewMemoryStore()

	seed := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Hostname: "overcast.local", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	if err := seed.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:                 "live-restart-ready-kubeconfig-empty-id",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/live-restart-ready-kubeconfig-empty-id",
		Status:               "CREATING",
		Version:              "1.31",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate restart with an incomplete cached runtime entry (container ID
	// never populated) while the managed k3s runtime is already ready.
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Hostname: "overcast.local", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))
	svc.setLiveClusterRuntime("us-east-1", "live-restart-ready-kubeconfig-empty-id", &liveClusterRuntime{})

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/live-restart-ready-kubeconfig-empty-id/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after empty-runtime-ID reconciliation, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	kubeconfig, _ := body["kubeconfig"].(string)
	wantEndpoint := "https://overcast.local:" + readyzPort
	if !strings.Contains(kubeconfig, "server: "+wantEndpoint) {
		t.Fatalf("expected kubeconfig to include reconciled endpoint %q, got %q", wantEndpoint, kubeconfig)
	}
	if !strings.Contains(kubeconfig, "certificate-authority-data: "+expectedCAData) {
		t.Fatalf("expected kubeconfig to include reconciled CA data, got %q", kubeconfig)
	}

	if len(requests) != 2 {
		t.Fatalf("expected inspect + archive docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-live-restart-ready-kubeconfig-empty-id/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/restart-ready-kubeconfig-empty-id-ctr/archive" {
		t.Fatalf("expected archive request for reconciled ready runtime, got %q", requests[1])
	}

	persisted, found, err := svc.getCluster(context.Background(), "us-east-1", "live-restart-ready-kubeconfig-empty-id")
	if err != nil {
		t.Fatalf("getCluster: %v", err)
	}
	if !found {
		t.Fatal("expected cluster to remain present")
	}
	if persisted.Status != "ACTIVE" {
		t.Fatalf("expected persisted cluster to reconcile to ACTIVE, got %q", persisted.Status)
	}
	if persisted.Endpoint != wantEndpoint {
		t.Fatalf("expected persisted endpoint %q, got %q", wantEndpoint, persisted.Endpoint)
	}
	persistedCAData, _ := persisted.CertificateAuthority["data"].(string)
	if persistedCAData != expectedCAData {
		t.Fatalf("expected persisted CA data %q, got %q", expectedCAData, persistedCAData)
	}
}

func TestLiveModeUpdateKubeconfigReconcilesWhenCachedRuntimeIDIsStale(t *testing.T) {
	const expectedCAData = "UkVTVEFSVC1LRUNPTkZJRy1TVEFMRS1JRC1DQQ=="
	k3sYAMLArchive := tarArchiveWithSingleFile(t, "k3s.yaml", "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: "+expectedCAData+"\n")

	readyzSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer readyzSrv.Close()

	readyzURL, _ := url.Parse(readyzSrv.URL)
	readyzPort := readyzURL.Port()

	requests := make([]string, 0, 2)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/stale-k3s-id/json":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/restart-ready-kubeconfig-stale-id-ctr/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-ready-kubeconfig-stale-id-ctr",
				"Name": "/overcast-eks-live-restart-ready-kubeconfig-stale-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "live-restart-ready-kubeconfig-stale-id"),
				},
				"State": map[string]any{"Running": true},
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"overcast_eks": map[string]any{"IPAddress": "172.17.0.2"},
					},
					"Ports": map[string]any{
						"6443/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": readyzPort}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-live-restart-ready-kubeconfig-stale-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-ready-kubeconfig-stale-id-ctr",
				"Name": "/overcast-eks-live-restart-ready-kubeconfig-stale-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "live-restart-ready-kubeconfig-stale-id"),
				},
				"State": map[string]any{"Running": true},
				"NetworkSettings": map[string]any{
					"Networks": map[string]any{
						"overcast_eks": map[string]any{"IPAddress": "172.17.0.2"},
					},
					"Ports": map[string]any{
						"6443/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": readyzPort}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/restart-ready-kubeconfig-stale-id-ctr/archive":
			if gotPath := r.URL.Query().Get("path"); gotPath != "/etc/rancher/k3s/k3s.yaml" {
				t.Fatalf("expected archive path /etc/rancher/k3s/k3s.yaml, got %q", gotPath)
			}
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(k3sYAMLArchive)
		default:
			t.Fatalf("unexpected docker request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	store := state.NewMemoryStore()

	seed := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Hostname: "overcast.local", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	if err := seed.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:                 "live-restart-ready-kubeconfig-stale-id",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/live-restart-ready-kubeconfig-stale-id",
		Status:               "CREATING",
		Version:              "1.31",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate restart with a stale non-empty cached runtime ID while the
	// managed k3s runtime is already ready under the deterministic name.
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Hostname: "overcast.local", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))
	svc.setLiveClusterRuntime("us-east-1", "live-restart-ready-kubeconfig-stale-id", &liveClusterRuntime{containerID: "stale-k3s-id"})

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/clusters/live-restart-ready-kubeconfig-stale-id/kubeconfig", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after stale-runtime-ID reconciliation, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	kubeconfig, _ := body["kubeconfig"].(string)
	wantEndpoint := "https://overcast.local:" + readyzPort
	if !strings.Contains(kubeconfig, "server: "+wantEndpoint) {
		t.Fatalf("expected kubeconfig to include reconciled endpoint %q, got %q", wantEndpoint, kubeconfig)
	}
	if !strings.Contains(kubeconfig, "certificate-authority-data: "+expectedCAData) {
		t.Fatalf("expected kubeconfig to include reconciled CA data, got %q", kubeconfig)
	}

	if len(requests) < 4 {
		t.Fatalf("expected stale-inspect + name-inspect + runtime-inspect + archive docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/stale-k3s-id/json" {
		t.Fatalf("expected initial inspect attempt of stale runtime ID, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/overcast-eks-live-restart-ready-kubeconfig-stale-id/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[1])
	}
	if got := requestPathWithoutQuery(t, requests[2]); got != "/v1.45/containers/restart-ready-kubeconfig-stale-id-ctr/json" {
		t.Fatalf("expected inspect request for reconciled runtime ID, got %q", requests[2])
	}
	if got := requestPathWithoutQuery(t, requests[len(requests)-1]); got != "/v1.45/containers/restart-ready-kubeconfig-stale-id-ctr/archive" {
		t.Fatalf("expected archive request for reconciled ready runtime, got %q", requests[len(requests)-1])
	}

	persisted, found, err := svc.getCluster(context.Background(), "us-east-1", "live-restart-ready-kubeconfig-stale-id")
	if err != nil {
		t.Fatalf("getCluster: %v", err)
	}
	if !found {
		t.Fatal("expected cluster to remain present")
	}
	if persisted.Status != "ACTIVE" {
		t.Fatalf("expected persisted cluster to reconcile to ACTIVE, got %q", persisted.Status)
	}
	if persisted.Endpoint != wantEndpoint {
		t.Fatalf("expected persisted endpoint %q, got %q", wantEndpoint, persisted.Endpoint)
	}
	persistedCAData, _ := persisted.CertificateAuthority["data"].(string)
	if persistedCAData != expectedCAData {
		t.Fatalf("expected persisted CA data %q, got %q", expectedCAData, persistedCAData)
	}
}
