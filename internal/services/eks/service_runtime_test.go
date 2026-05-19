package eks

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/state"
)

func TestStopCleansUpOwnedLiveRuntimeContainers(t *testing.T) {
	requests := make([]string, 0, 2)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1.45/containers/") && strings.HasSuffix(r.URL.Path, "/stop"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1.45/containers/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected docker request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	service := New(&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive}, state.NewMemoryStore(), zap.NewNop(), clock.New())
	service.SetDocker(docker.NewClient(endpoint, zap.NewNop()))
	service.setLiveClusterRuntime("us-east-1", "cleanup-cluster", &liveClusterRuntime{containerID: "ctr-123"})

	service.Stop(t.Context())

	if len(requests) != 2 {
		t.Fatalf("expected 2 docker cleanup requests, got %d (%v)", len(requests), requests)
	}
	stopPath := requestPathWithoutQuery(t, requests[0])
	if stopPath != "/v1.45/containers/ctr-123/stop" {
		t.Fatalf("expected stop request for owned container, got %q", requests[0])
	}
	removePath := requestPathWithoutQuery(t, requests[1])
	if removePath != "/v1.45/containers/ctr-123" {
		t.Fatalf("expected remove request for owned container, got %q", requests[1])
	}
	if _, found := service.getLiveClusterRuntime("us-east-1", "cleanup-cluster"); found {
		t.Fatal("expected live runtime registry to be cleared after Stop")
	}
}

func TestStopReconcilesManagedLiveRuntimeContainersAfterRestart(t *testing.T) {
	requests := make([]string, 0, 4)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-restart-stop/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-stop-ctr",
				"Name": "/overcast-eks-restart-stop",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "restart-stop"),
				},
				"State": map[string]any{"Running": true},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/restart-stop-ctr/stop":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1.45/containers/restart-stop-ctr":
			w.WriteHeader(http.StatusNoContent)
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
		Name:      "restart-stop",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/restart-stop",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://overcast.local:16443",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	service := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	service.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	service.Stop(t.Context())

	if len(requests) != 3 {
		t.Fatalf("expected inspect + stop + remove docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-restart-stop/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/restart-stop-ctr/stop" {
		t.Fatalf("expected stop request for reconciled container, got %q", requests[1])
	}
	if got := requestPathWithoutQuery(t, requests[2]); got != "/v1.45/containers/restart-stop-ctr" {
		t.Fatalf("expected remove request for reconciled container, got %q", requests[2])
	}
}

func TestStopReconcilesManagedRuntimeWhenCachedContainerIDMissing(t *testing.T) {
	requests := make([]string, 0, 4)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-restart-stop-empty-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-stop-empty-id-ctr",
				"Name": "/overcast-eks-restart-stop-empty-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "restart-stop-empty-id"),
				},
				"State": map[string]any{"Running": true},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/restart-stop-empty-id-ctr/stop":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1.45/containers/restart-stop-empty-id-ctr":
			w.WriteHeader(http.StatusNoContent)
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
		Name:      "restart-stop-empty-id",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/restart-stop-empty-id",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://overcast.local:16443",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	service := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	service.SetDocker(docker.NewClient(endpoint, zap.NewNop()))
	service.setLiveClusterRuntime("us-east-1", "restart-stop-empty-id", &liveClusterRuntime{})

	service.Stop(t.Context())

	if len(requests) != 3 {
		t.Fatalf("expected inspect + stop + remove docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-restart-stop-empty-id/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/restart-stop-empty-id-ctr/stop" {
		t.Fatalf("expected stop request for reconciled container, got %q", requests[1])
	}
	if got := requestPathWithoutQuery(t, requests[2]); got != "/v1.45/containers/restart-stop-empty-id-ctr" {
		t.Fatalf("expected remove request for reconciled container, got %q", requests[2])
	}
}

func TestStopReconcilesManagedRuntimeWhenCachedContainerIDIsStale(t *testing.T) {
	requests := make([]string, 0, 4)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-restart-stop-stale-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-stop-current-id-ctr",
				"Name": "/overcast-eks-restart-stop-stale-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "restart-stop-stale-id"),
				},
				"State": map[string]any{"Running": true},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/restart-stop-current-id-ctr/stop":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1.45/containers/restart-stop-current-id-ctr":
			w.WriteHeader(http.StatusNoContent)
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
		Name:      "restart-stop-stale-id",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/restart-stop-stale-id",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://overcast.local:16443",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	service := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	service.SetDocker(docker.NewClient(endpoint, zap.NewNop()))
	service.setLiveClusterRuntime("us-east-1", "restart-stop-stale-id", &liveClusterRuntime{containerID: "stale-k3s-id"})

	service.Stop(t.Context())

	if len(requests) != 3 {
		t.Fatalf("expected inspect + stop + remove docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-restart-stop-stale-id/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/restart-stop-current-id-ctr/stop" {
		t.Fatalf("expected stop request for reconciled current container, got %q", requests[1])
	}
	if got := requestPathWithoutQuery(t, requests[2]); got != "/v1.45/containers/restart-stop-current-id-ctr" {
		t.Fatalf("expected remove request for reconciled current container, got %q", requests[2])
	}
}

func TestLiveModeCreateClusterStartsK3sContainer(t *testing.T) {
	var mu sync.Mutex
	creates, starts := 0, 0
	var createPayload map[string]any

	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			if err := json.NewDecoder(r.Body).Decode(&createPayload); err != nil {
				t.Fatalf("decode create request payload: %v", err)
			}
			creates++
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"Id": "k3s-container-id"}) //nolint:errcheck
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1.45/containers/") && strings.HasSuffix(r.URL.Path, "/start"):
			starts++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Logf("unexpected docker request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	svc := New(
		&config.Config{
			Region:          "us-east-1",
			AccountID:       "000000000000",
			EKSMode:         config.EKSModeLive,
			EKSDockerSocket: endpoint,
			EKSNetwork:      "overcast_eks",
		},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, _ := json.Marshal(map[string]any{
		"name":    "k3s-cluster",
		"roleArn": "arn:aws:iam::000000000000:role/eks-role",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewReader(payload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", createRec.Code, createRec.Body.String())
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rt, found := svc.getLiveClusterRuntime("us-east-1", "k3s-cluster"); found && rt.containerID != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	gotCreates, gotStarts := creates, starts
	mu.Unlock()

	if gotCreates != 1 {
		t.Fatalf("expected 1 docker create call, got %d", gotCreates)
	}
	if gotStarts != 1 {
		t.Fatalf("expected 1 docker start call, got %d", gotStarts)
	}
	hostConfig, _ := createPayload["HostConfig"].(map[string]any)
	portBindings, _ := hostConfig["PortBindings"].(map[string]any)
	bindings, _ := portBindings["6443/tcp"].([]any)
	if len(bindings) == 0 {
		t.Fatal("expected 6443/tcp port binding in docker create payload")
	}
	firstBinding, _ := bindings[0].(map[string]any)
	if gotHostIP, _ := firstBinding["HostIp"].(string); gotHostIP != "0.0.0.0" {
		t.Fatalf("expected host port binding on 0.0.0.0, got %q", gotHostIP)
	}
	runtime, found := svc.getLiveClusterRuntime("us-east-1", "k3s-cluster")
	if !found {
		t.Fatal("expected live runtime entry after container start")
	}
	if runtime.containerID != "k3s-container-id" {
		t.Fatalf("expected container ID %q, got %q", "k3s-container-id", runtime.containerID)
	}
}

func TestLiveModeCreateClusterReusesStoppedManagedContainerOnConflict(t *testing.T) {
	var mu sync.Mutex
	creates, starts := 0, 0

	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			creates++
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"Conflict. The container name is already in use."}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-k3s-conflict/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id": "existing-k3s-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "k3s-conflict"),
				},
				"State": map[string]any{"Running": false},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/existing-k3s-id/start":
			starts++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/existing-k3s-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id": "existing-k3s-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "k3s-conflict"),
				},
				"State": map[string]any{"Running": true},
				"NetworkSettings": map[string]any{
					"Ports": map[string]any{
						"6443/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": "1"}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/existing-k3s-id/archive":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Logf("unexpected docker request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer dockerSrv.Close()

	endpoint := "tcp://" + strings.TrimPrefix(dockerSrv.URL, "http://")
	svc := New(
		&config.Config{
			Region:          "us-east-1",
			AccountID:       "000000000000",
			EKSMode:         config.EKSModeLive,
			EKSDockerSocket: endpoint,
			EKSNetwork:      "overcast_eks",
		},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, _ := json.Marshal(map[string]any{
		"name":    "k3s-conflict",
		"roleArn": "arn:aws:iam::000000000000:role/eks-role",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewReader(payload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", createRec.Code, createRec.Body.String())
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rt, found := svc.getLiveClusterRuntime("us-east-1", "k3s-conflict"); found && rt.containerID == "existing-k3s-id" {
			break
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	gotCreates, gotStarts := creates, starts
	mu.Unlock()

	if gotCreates != 1 {
		t.Fatalf("expected 1 docker create call, got %d", gotCreates)
	}
	if gotStarts != 1 {
		t.Fatalf("expected 1 docker start call for reused container, got %d", gotStarts)
	}
	runtime, found := svc.getLiveClusterRuntime("us-east-1", "k3s-conflict")
	if !found {
		t.Fatal("expected live runtime entry after conflict reuse")
	}
	if runtime.containerID != "existing-k3s-id" {
		t.Fatalf("expected reused container ID %q, got %q", "existing-k3s-id", runtime.containerID)
	}
}

func TestLiveModeClusterTransitionsToACTIVE(t *testing.T) {
	const expectedCAData = "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t"
	k3sYAMLArchive := tarArchiveWithSingleFile(t, "k3s.yaml", "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: "+expectedCAData+"\n")

	readyzSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer readyzSrv.Close()

	readyzURL, _ := url.Parse(readyzSrv.URL)
	readyzPort := readyzURL.Port()

	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"Id": "k3s-ready-ctr"}) //nolint:errcheck
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1.45/containers/") && strings.HasSuffix(r.URL.Path, "/start"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/k3s-ready-ctr/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id": "k3s-ready-ctr",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "transition-cluster"),
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/k3s-ready-ctr/archive":
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
		&config.Config{
			Region:          "us-east-1",
			AccountID:       "000000000000",
			Hostname:        "overcast.local",
			EKSMode:         config.EKSModeLive,
			EKSDockerSocket: endpoint,
			EKSNetwork:      "overcast_eks",
		},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	payload, _ := json.Marshal(map[string]any{
		"name":    "transition-cluster",
		"roleArn": "arn:aws:iam::000000000000:role/eks-role",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/clusters", bytes.NewReader(payload))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	r.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", createRec.Code, createRec.Body.String())
	}

	ctx := context.Background()
	deadline := time.Now().Add(3 * time.Second)
	var finalCluster *Cluster
	for time.Now().Before(deadline) {
		c, found, err := svc.getCluster(ctx, "us-east-1", "transition-cluster")
		if err != nil {
			t.Fatalf("getCluster error: %v", err)
		}
		if found && c.Status == "ACTIVE" {
			finalCluster = c
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if finalCluster == nil {
		t.Fatal("cluster did not transition to ACTIVE within 3 seconds")
	}
	wantEndpoint := "https://overcast.local:" + readyzPort
	if finalCluster.Endpoint != wantEndpoint {
		t.Fatalf("expected endpoint %q, got %q", wantEndpoint, finalCluster.Endpoint)
	}
	caData, _ := finalCluster.CertificateAuthority["data"].(string)
	if caData != expectedCAData {
		t.Fatalf("expected certificate authority data %q, got %q", expectedCAData, caData)
	}
}

func TestLiveModeDeleteClusterReconcilesManagedContainerAfterRestart(t *testing.T) {
	requests := make([]string, 0, 3)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-restart-cluster/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-k3s-id",
				"Name": "/overcast-eks-restart-cluster",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "restart-cluster"),
				},
				"State": map[string]any{"Running": true},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/restart-k3s-id/stop":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1.45/containers/restart-k3s-id":
			w.WriteHeader(http.StatusNoContent)
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
		Name:      "restart-cluster",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/restart-cluster",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://overcast.local:16443",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate a restarted process: the persisted cluster remains, but the new
	// service instance has an empty in-memory live runtime registry.
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodDelete, "/clusters/restart-cluster", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for delete after restart, got %d body=%s", rec.Code, rec.Body.String())
	}

	if len(requests) != 3 {
		t.Fatalf("expected inspect + stop + remove docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-restart-cluster/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/restart-k3s-id/stop" {
		t.Fatalf("expected stop request for reconciled container, got %q", requests[1])
	}
	if got := requestPathWithoutQuery(t, requests[2]); got != "/v1.45/containers/restart-k3s-id" {
		t.Fatalf("expected remove request for reconciled container, got %q", requests[2])
	}
}

func TestLiveModeDeleteClusterReconcilesRuntimeWhenCachedContainerIDMissing(t *testing.T) {
	requests := make([]string, 0, 3)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-empty-runtime-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "empty-runtime-id-k3s",
				"Name": "/overcast-eks-empty-runtime-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "empty-runtime-id"),
				},
				"State": map[string]any{"Running": true},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/empty-runtime-id-k3s/stop":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1.45/containers/empty-runtime-id-k3s":
			w.WriteHeader(http.StatusNoContent)
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

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "empty-runtime-id",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/empty-runtime-id",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://overcast.local:16443",
		CreatedAt: time.Now(),
		CertificateAuthority: map[string]any{
			"data": "ZmFrZS1jYS1kYXRh",
		},
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate a cached runtime entry created before container start completed.
	// The entry exists, but container ID is still empty.
	svc.setLiveClusterRuntime("us-east-1", "empty-runtime-id", &liveClusterRuntime{})

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodDelete, "/clusters/empty-runtime-id", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for delete with empty cached runtime ID, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(requests) != 3 {
		t.Fatalf("expected inspect + stop + remove docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-empty-runtime-id/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/empty-runtime-id-k3s/stop" {
		t.Fatalf("expected stop request for reconciled container, got %q", requests[1])
	}
	if got := requestPathWithoutQuery(t, requests[2]); got != "/v1.45/containers/empty-runtime-id-k3s" {
		t.Fatalf("expected remove request for reconciled container, got %q", requests[2])
	}
}

func TestLiveModeDeleteClusterReconcilesWhenCachedContainerIDIsStale(t *testing.T) {
	requests := make([]string, 0, 3)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-stale-runtime-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "current-runtime-id-k3s",
				"Name": "/overcast-eks-stale-runtime-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "stale-runtime-id"),
				},
				"State": map[string]any{"Running": true},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/current-runtime-id-k3s/stop":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1.45/containers/current-runtime-id-k3s":
			w.WriteHeader(http.StatusNoContent)
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

	if err := svc.putCluster(context.Background(), "us-east-1", &Cluster{
		Name:      "stale-runtime-id",
		Arn:       "arn:aws:eks:us-east-1:000000000000:cluster/stale-runtime-id",
		Status:    "ACTIVE",
		Version:   "1.31",
		Endpoint:  "https://overcast.local:16443",
		CreatedAt: time.Now(),
		CertificateAuthority: map[string]any{
			"data": "ZmFrZS1jYS1kYXRh",
		},
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate runtime map drift where the cached container ID is stale.
	svc.setLiveClusterRuntime("us-east-1", "stale-runtime-id", &liveClusterRuntime{containerID: "stale-k3s-id"})

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodDelete, "/clusters/stale-runtime-id", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for delete with stale cached runtime ID, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(requests) != 3 {
		t.Fatalf("expected inspect + stop + remove docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-stale-runtime-id/json" {
		t.Fatalf("expected reconcile inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/current-runtime-id-k3s/stop" {
		t.Fatalf("expected stop request for reconciled current container, got %q", requests[1])
	}
	if got := requestPathWithoutQuery(t, requests[2]); got != "/v1.45/containers/current-runtime-id-k3s" {
		t.Fatalf("expected remove request for reconciled current container, got %q", requests[2])
	}
}

func TestLiveModeDescribeClusterReconcilesReadyRuntimeAfterRestart(t *testing.T) {
	const expectedCAData = "UkVTVEFSVC1SRUFEWS1DQQ=="
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-restart-ready/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-ready-ctr",
				"Name": "/overcast-eks-restart-ready",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "restart-ready"),
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/restart-ready-ctr/archive":
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
		Name:                 "restart-ready",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/restart-ready",
		Status:               "CREATING",
		Version:              "1.31",
		Endpoint:             "",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate a restarted process: the persisted cluster remains but the runtime
	// registry is empty, even though the managed k3s container is already ready.
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Hostname: "overcast.local", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/restart-ready", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for describe after restart readiness reconciliation, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	cluster, _ := body["cluster"].(map[string]any)
	if cluster["status"] != "ACTIVE" {
		t.Fatalf("expected reconciled cluster status ACTIVE, got %#v", cluster["status"])
	}
	wantEndpoint := "https://overcast.local:" + readyzPort
	if cluster["endpoint"] != wantEndpoint {
		t.Fatalf("expected reconciled endpoint %q, got %#v", wantEndpoint, cluster["endpoint"])
	}
	certificateAuthority, _ := cluster["certificateAuthority"].(map[string]any)
	if certificateAuthority["data"] != expectedCAData {
		t.Fatalf("expected reconciled certificate authority data %q, got %#v", expectedCAData, certificateAuthority["data"])
	}

	if len(requests) != 2 {
		t.Fatalf("expected inspect + archive docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-restart-ready/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/restart-ready-ctr/archive" {
		t.Fatalf("expected archive request for reconciled ready container, got %q", requests[1])
	}
}

func TestLiveModeDescribeClusterReconcilesWhenCachedRuntimeIDMissing(t *testing.T) {
	const expectedCAData = "UkVTVEFSVC1SRUFEWS1DQS1FTVBUWS1JRA=="
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-restart-ready-empty-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-ready-empty-id-ctr",
				"Name": "/overcast-eks-restart-ready-empty-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "restart-ready-empty-id"),
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/restart-ready-empty-id-ctr/archive":
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
		Name:                 "restart-ready-empty-id",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/restart-ready-empty-id",
		Status:               "CREATING",
		Version:              "1.31",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate restart with an incomplete cached runtime entry (container ID was
	// never populated), while managed container is already running.
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Hostname: "overcast.local", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))
	svc.setLiveClusterRuntime("us-east-1", "restart-ready-empty-id", &liveClusterRuntime{})

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/restart-ready-empty-id", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for describe after empty-runtime-ID reconciliation, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	cluster, _ := body["cluster"].(map[string]any)
	if cluster["status"] != "ACTIVE" {
		t.Fatalf("expected reconciled cluster status ACTIVE, got %#v", cluster["status"])
	}
	wantEndpoint := "https://overcast.local:" + readyzPort
	if cluster["endpoint"] != wantEndpoint {
		t.Fatalf("expected reconciled endpoint %q, got %#v", wantEndpoint, cluster["endpoint"])
	}
	certificateAuthority, _ := cluster["certificateAuthority"].(map[string]any)
	if certificateAuthority["data"] != expectedCAData {
		t.Fatalf("expected reconciled certificate authority data %q, got %#v", expectedCAData, certificateAuthority["data"])
	}

	if len(requests) != 2 {
		t.Fatalf("expected inspect + archive docker requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/overcast-eks-restart-ready-empty-id/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/restart-ready-empty-id-ctr/archive" {
		t.Fatalf("expected archive request for reconciled ready container, got %q", requests[1])
	}
}

func TestLiveModeDescribeClusterReconcilesWhenCachedRuntimeIDIsStale(t *testing.T) {
	const expectedCAData = "UkVTVEFSVC1SRUFEWS1DQS1TVEFMRS1JRA=="
	k3sYAMLArchive := tarArchiveWithSingleFile(t, "k3s.yaml", "apiVersion: v1\nclusters:\n- cluster:\n    certificate-authority-data: "+expectedCAData+"\n")

	readyzSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer readyzSrv.Close()

	readyzURL, _ := url.Parse(readyzSrv.URL)
	readyzPort := readyzURL.Port()

	requests := make([]string, 0, 4)
	dockerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/stale-k3s-id/json":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/overcast-eks-restart-ready-stale-id/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-ready-stale-id-ctr",
				"Name": "/overcast-eks-restart-ready-stale-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "restart-ready-stale-id"),
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/restart-ready-stale-id-ctr/json":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"Id":   "restart-ready-stale-id-ctr",
				"Name": "/overcast-eks-restart-ready-stale-id",
				"Config": map[string]any{
					"Labels": docker.ManagedLabels("eks", "restart-ready-stale-id"),
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1.45/containers/restart-ready-stale-id-ctr/archive":
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
		Name:                 "restart-ready-stale-id",
		Arn:                  "arn:aws:eks:us-east-1:000000000000:cluster/restart-ready-stale-id",
		Status:               "CREATING",
		Version:              "1.31",
		CreatedAt:            time.Now(),
		CertificateAuthority: map[string]any{},
	}); err != nil {
		t.Fatalf("putCluster: %v", err)
	}

	// Simulate restart with a stale non-empty cached runtime ID while managed
	// runtime is available under deterministic container name.
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", Hostname: "overcast.local", EKSMode: config.EKSModeLive},
		store, zap.NewNop(), clock.New(),
	)
	svc.SetDocker(docker.NewClient(endpoint, zap.NewNop()))
	svc.setLiveClusterRuntime("us-east-1", "restart-ready-stale-id", &liveClusterRuntime{containerID: "stale-k3s-id"})

	r := chi.NewRouter()
	svc.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/clusters/restart-ready-stale-id", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for describe after stale-runtime-ID reconciliation, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	cluster, _ := body["cluster"].(map[string]any)
	if cluster["status"] != "ACTIVE" {
		t.Fatalf("expected reconciled cluster status ACTIVE, got %#v", cluster["status"])
	}
	wantEndpoint := "https://overcast.local:" + readyzPort
	if cluster["endpoint"] != wantEndpoint {
		t.Fatalf("expected reconciled endpoint %q, got %#v", wantEndpoint, cluster["endpoint"])
	}
	certificateAuthority, _ := cluster["certificateAuthority"].(map[string]any)
	if certificateAuthority["data"] != expectedCAData {
		t.Fatalf("expected reconciled certificate authority data %q, got %#v", expectedCAData, certificateAuthority["data"])
	}

	if len(requests) != 4 {
		t.Fatalf("expected stale-inspect + name-inspect + runtime-inspect + archive requests, got %d (%v)", len(requests), requests)
	}
	if got := requestPathWithoutQuery(t, requests[0]); got != "/v1.45/containers/stale-k3s-id/json" {
		t.Fatalf("expected initial stale runtime inspect attempt, got %q", requests[0])
	}
	if got := requestPathWithoutQuery(t, requests[1]); got != "/v1.45/containers/overcast-eks-restart-ready-stale-id/json" {
		t.Fatalf("expected fallback inspect by managed container name, got %q", requests[1])
	}
	if got := requestPathWithoutQuery(t, requests[2]); got != "/v1.45/containers/restart-ready-stale-id-ctr/json" {
		t.Fatalf("expected inspect request for refreshed runtime ID, got %q", requests[2])
	}
	if got := requestPathWithoutQuery(t, requests[3]); got != "/v1.45/containers/restart-ready-stale-id-ctr/archive" {
		t.Fatalf("expected archive request for reconciled ready container, got %q", requests[3])
	}
}

func requestPathWithoutQuery(t *testing.T, raw string) string {
	t.Helper()
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected request record %q", raw)
	}
	u, err := url.ParseRequestURI(parts[1])
	if err != nil {
		t.Fatalf("parse request URI %q: %v", raw, err)
	}
	return u.Path
}

func tarArchiveWithSingleFile(t *testing.T, name, body string) []byte {
	t.Helper()

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)

	payload := []byte(body)
	hdr := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(payload))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatalf("write tar payload: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}

	out, err := io.ReadAll(buf)
	if err != nil {
		t.Fatalf("read tar buffer: %v", err)
	}
	return out
}
