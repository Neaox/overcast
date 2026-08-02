package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewClient_UnixSocket(t *testing.T) {
	c := NewClient("/var/run/docker.sock", zap.NewNop())
	if c.host != "http://docker" {
		t.Errorf("expected host http://docker, got %s", c.host)
	}
}

func TestNewClient_TCP(t *testing.T) {
	c := NewClient("tcp://dind:2375", zap.NewNop())
	if c.host != "http://dind:2375" {
		t.Errorf("expected host http://dind:2375, got %s", c.host)
	}
}

func TestNewClient_TCPLocalhost(t *testing.T) {
	c := NewClient("tcp://127.0.0.1:2375", zap.NewNop())
	if c.host != "http://127.0.0.1:2375" {
		t.Errorf("expected host http://127.0.0.1:2375, got %s", c.host)
	}
}

func TestNewClient_BarePathIsUnix(t *testing.T) {
	c := NewClient("/tmp/custom.sock", zap.NewNop())
	if c.host != "http://docker" {
		t.Errorf("expected host http://docker for bare path, got %s", c.host)
	}
}

func TestEndpointAliases_filtersNonDNSAddresses(t *testing.T) {
	// Given: endpoint addresses containing DNS names, duplicate names, and direct IP/localhost addresses.
	addresses := []string{
		"cache.ap-southeast-2.serverless.localhost",
		"127.0.0.1",
		"localhost",
		"10.0.0.5",
		"cache.ap-southeast-2.serverless.localhost",
		"reader.ap-southeast-2.serverless.localhost",
	}

	// When: Docker aliases are built.
	got := EndpointAliases(addresses...)

	// Then: only unique DNS hostnames remain, preserving first-seen order.
	want := []string{"cache.ap-southeast-2.serverless.localhost", "reader.ap-southeast-2.serverless.localhost"}
	if !slices.Equal(got, want) {
		t.Fatalf("EndpointAliases() = %#v, want %#v", got, want)
	}
}

func TestInfo_returnsHostResources(t *testing.T) {
	// Given: a fake Docker daemon reporting its CPU and memory resources.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1.45/info" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		// A real /info response carries dozens of fields; only the resource
		// ones matter here, plus one extra to prove unknown fields are ignored.
		_, _ = w.Write([]byte(`{"NCPU":8,"MemTotal":16777216000,"ServerVersion":"27.0.1"}`))
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client(), host: server.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}

	// When: daemon system info is fetched.
	info, err := client.Info(context.Background())

	// Then: the daemon's host resources are reported — the machine containers
	// actually run on, not necessarily the machine Overcast runs on.
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.NCPU != 8 {
		t.Fatalf("NCPU = %d, want 8", info.NCPU)
	}
	if info.MemTotal != 16777216000 {
		t.Fatalf("MemTotal = %d, want 16777216000", info.MemTotal)
	}
}

func TestInfo_daemonErrorIsReturned(t *testing.T) {
	// Given: a daemon that answers /info with a server error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client(), host: server.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}

	// When/Then: the error surfaces instead of a zero-value SystemInfo.
	if _, err := client.Info(context.Background()); err == nil {
		t.Fatal("Info: expected an error for a 500 response")
	}
}

func TestConnectNetworkWithAliases_sendsEndpointConfig(t *testing.T) {
	// Given: a fake Docker daemon that captures network connect payloads.
	var got struct {
		Container      string            `json:"Container"`
		EndpointConfig *EndpointSettings `json:"EndpointConfig"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1.45/networks/lambda-net/connect" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode connect payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client(), host: server.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}

	// When: a container is connected with DNS aliases.
	err := client.ConnectNetworkWithAliases(context.Background(), "lambda-net", "container-1", []string{"cache.localhost"})

	// Then: Docker receives EndpointConfig aliases.
	if err != nil {
		t.Fatalf("ConnectNetworkWithAliases: %v", err)
	}
	if got.Container != "container-1" {
		t.Fatalf("Container = %q, want container-1", got.Container)
	}
	if got.EndpointConfig == nil || !slices.Equal(got.EndpointConfig.Aliases, []string{"cache.localhost"}) {
		t.Fatalf("EndpointConfig = %#v, want aliases", got.EndpointConfig)
	}
}

func TestContainerStatsOneShot_usesOneShotAndParsesSample(t *testing.T) {
	// Given: a fake Docker daemon that records the stats query and returns a
	// single stats sample.
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1.45/containers/abc123/stats" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{
			"memory_stats": {"usage": 134217728, "stats": {"inactive_file": 33554432}},
			"cpu_stats": {"cpu_usage": {"total_usage": 5000000}, "system_cpu_usage": 100000000, "online_cpus": 4}
		}`))
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client(), host: server.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}

	// When: a stats sample is fetched.
	stats, err := client.ContainerStatsOneShot(context.Background(), "abc123")

	// Then: one-shot=true is requested — stream=false alone makes the daemon
	// wait an extra collection cycle (~1–2 s), which is what used to overrun
	// caller timeouts on slow hosts and surface as "memory used: 0".
	if err != nil {
		t.Fatalf("ContainerStatsOneShot: %v", err)
	}
	if gotQuery.Get("stream") != "false" || gotQuery.Get("one-shot") != "true" {
		t.Fatalf("stats query = %v, want stream=false and one-shot=true", gotQuery)
	}
	// Memory follows docker-CLI semantics: usage minus inactive file cache.
	if stats.MemoryUsageBytes != 134217728-33554432 {
		t.Fatalf("MemoryUsageBytes = %d, want %d", stats.MemoryUsageBytes, 134217728-33554432)
	}
	if stats.CPUTotalUsage != 5000000 || stats.SystemCPUUsage != 100000000 || stats.OnlineCPUs != 4 {
		t.Fatalf("cpu sample = %+v, want total=5000000 system=100000000 cpus=4", stats)
	}
}

func TestContainerStatsOneShot_cgroupV1Fallbacks(t *testing.T) {
	// Given: a daemon shaped like cgroup v1 — total_inactive_file instead of
	// inactive_file, per-CPU usage array instead of online_cpus.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"memory_stats": {"usage": 67108864, "stats": {"total_inactive_file": 16777216}},
			"cpu_stats": {"cpu_usage": {"total_usage": 1, "percpu_usage": [1, 2]}, "system_cpu_usage": 2}
		}`))
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client(), host: server.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}

	// When: a stats sample is fetched.
	stats, err := client.ContainerStatsOneShot(context.Background(), "abc123")

	// Then: both v1 field spellings are honoured.
	if err != nil {
		t.Fatalf("ContainerStatsOneShot: %v", err)
	}
	if stats.MemoryUsageBytes != 67108864-16777216 {
		t.Fatalf("MemoryUsageBytes = %d, want %d", stats.MemoryUsageBytes, 67108864-16777216)
	}
	if stats.OnlineCPUs != 2 {
		t.Fatalf("OnlineCPUs = %d, want 2 (from percpu_usage length)", stats.OnlineCPUs)
	}
}

func TestCreateContainer_boundsConcurrentDockerMutations(t *testing.T) {
	// Given: a fake Docker daemon that records concurrent create requests.
	var inFlight int32
	var highWater int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1.45/containers/create" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		current := atomic.AddInt32(&inFlight, 1)
		for {
			observed := atomic.LoadInt32(&highWater)
			if current <= observed || atomic.CompareAndSwapInt32(&highWater, observed, current) {
				break
			}
		}
		<-release
		atomic.AddInt32(&inFlight, -1)
		_ = json.NewEncoder(w).Encode(CreateContainerResponse{ID: "container-id"})
	}))
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		host:       server.URL,
		logger:     zap.NewNop(),
		sem:        make(chan struct{}, maxConcurrentOps),
	}

	// When: many create requests are issued concurrently.
	const requests = maxConcurrentOps * 4
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.CreateContainer(context.Background(), "", &CreateContainerRequest{ContainerConfig: &ContainerConfig{Image: "test"}})
			if err != nil {
				t.Errorf("CreateContainer: %v", err)
			}
		}()
	}

	for atomic.LoadInt32(&highWater) < maxConcurrentOps {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()

	// Then: Docker mutations are bounded by the explicit operation semaphore.
	if got := atomic.LoadInt32(&highWater); got > maxConcurrentOps {
		t.Fatalf("concurrent create requests = %d, want <= %d", got, maxConcurrentOps)
	}
}

func TestCreateContainer_sendsPlatformQuery(t *testing.T) {
	// Given: a fake Docker daemon that records the create-container query string.
	var gotName string
	var gotPlatform string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1.45/containers/create" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		gotName = r.URL.Query().Get("name")
		gotPlatform = r.URL.Query().Get("platform")
		_ = json.NewEncoder(w).Encode(CreateContainerResponse{ID: "container-id"})
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client(), host: server.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}

	// When: a platform-specific container create request is sent.
	_, err := client.CreateContainer(context.Background(), "lambda-demo", &CreateContainerRequest{
		ContainerConfig: &ContainerConfig{Image: "public.ecr.aws/lambda/nodejs:22"},
		Platform:        "linux/amd64",
	})

	// Then: Docker receives platform in the URL query where the Engine API expects it.
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if gotName != "lambda-demo" {
		t.Fatalf("name query = %q, want lambda-demo", gotName)
	}
	if gotPlatform != "linux/amd64" {
		t.Fatalf("platform query = %q, want linux/amd64", gotPlatform)
	}
}

func TestPullImageForPlatform_sendsPlatformQuery(t *testing.T) {
	// Given: a fake Docker daemon that records image pull query parameters.
	var gotImage string
	var gotPlatform string
	var gotTag string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/images/create":
			gotImage = r.URL.Query().Get("fromImage")
			gotTag = r.URL.Query().Get("tag")
			gotPlatform = r.URL.Query().Get("platform")
			_, _ = w.Write([]byte(`{"status":"done"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/images/prune":
			_, _ = w.Write([]byte(`{"ImagesDeleted":null,"SpaceReclaimed":0}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client(), host: server.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}

	// When: an image is pulled for a specific platform.
	err := client.PullImageForPlatform(context.Background(), "public.ecr.aws/lambda/nodejs:22", "linux/amd64")

	// Then: Docker receives platform in the images/create URL query.
	if err != nil {
		t.Fatalf("PullImageForPlatform: %v", err)
	}
	// Docker takes the version in `tag`, never folded into `fromImage` — see
	// splitImageRef and TestPullImage_sendsTagSeparately.
	if gotImage != "public.ecr.aws/lambda/nodejs" {
		t.Fatalf("fromImage query = %q, want public.ecr.aws/lambda/nodejs", gotImage)
	}
	if gotTag != "22" {
		t.Fatalf("tag query = %q, want 22", gotTag)
	}
	if gotPlatform != "linux/amd64" {
		t.Fatalf("platform query = %q, want linux/amd64", gotPlatform)
	}
}

func TestImageMatchesPlatform(t *testing.T) {
	// Given: a local image inspect response for linux/arm64.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1.45/images/") || !strings.HasSuffix(r.URL.Path, "/json") {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(ImageInspect{OS: "linux", Architecture: "arm64"})
	}))
	defer server.Close()
	client := &Client{httpClient: server.Client(), host: server.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)}

	// When/Then: only the matching platform is reported as present.
	match, err := client.ImageMatchesPlatform(context.Background(), "public.ecr.aws/lambda/nodejs:22", "linux/arm64")
	if err != nil || !match {
		t.Fatalf("ImageMatchesPlatform arm64 = %v, %v; want true, nil", match, err)
	}
	match, err = client.ImageMatchesPlatform(context.Background(), "public.ecr.aws/lambda/nodejs:22", "linux/amd64")
	if err != nil || match {
		t.Fatalf("ImageMatchesPlatform amd64 = %v, %v; want false, nil", match, err)
	}
}

func TestContainerLogsStream_doesNotStarveCreateContainer(t *testing.T) {
	// Given: enough held log-follow streams to consume the old Docker transport limit.
	logStreams := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Get("follow") == "true":
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-logStreams
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/containers/create":
			_ = json.NewEncoder(w).Encode(CreateContainerResponse{ID: "container-id"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := NewClient("tcp://"+server.Listener.Addr().String(), zap.NewNop())

	var streams []interface{ Close() error }
	for i := 0; i < maxConcurrentOps; i++ {
		stream, err := client.ContainerLogsStream(context.Background(), "id", time.Time{})
		if err != nil {
			t.Fatalf("ContainerLogsStream %d: %v", i, err)
		}
		streams = append(streams, stream)
	}
	defer func() {
		close(logStreams)
		for _, stream := range streams {
			_ = stream.Close()
		}
	}()

	// When: a create request is sent while streams are still open.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := client.CreateContainer(ctx, "", &CreateContainerRequest{ContainerConfig: &ContainerConfig{Image: "test"}})

	// Then: the request succeeds instead of waiting behind long-lived log streams.
	if err != nil {
		t.Fatalf("CreateContainer while log streams are open: %v", err)
	}
}

// TestPullImage_sendsTagSeparately guards the wire shape of the pull request.
// Docker takes the version in `tag`, not folded into `fromImage`: sending a
// digest-pinned reference whole makes the daemon report a successful pull and
// store nothing under that digest, so the next container create fails with
// "No such image".
func TestPullImage_sendsTagSeparately(t *testing.T) {
	tests := []struct {
		name         string
		image        string
		wantFrom     string
		wantTag      string
		wantPlatform string
	}{
		{
			name:     "digest pin",
			image:    "registry.k8s.io/sig-storage/nfs-provisioner@sha256:c825f3d5",
			wantFrom: "registry.k8s.io/sig-storage/nfs-provisioner",
			wantTag:  "sha256:c825f3d5",
		},
		{
			name:     "plain tag",
			image:    "busybox:1.36",
			wantFrom: "busybox",
			wantTag:  "1.36",
		},
		{
			name:     "registry port is not a tag",
			image:    "localhost:5000/team/app",
			wantFrom: "localhost:5000/team/app",
			wantTag:  "",
		},
		{
			name:     "registry port with tag",
			image:    "localhost:5000/team/app:v2",
			wantFrom: "localhost:5000/team/app",
			wantTag:  "v2",
		},
		{
			name:     "no version",
			image:    "alpine",
			wantFrom: "alpine",
			wantTag:  "",
		},
		{
			name:         "platform is preserved",
			image:        "alpine:3.20",
			wantFrom:     "alpine",
			wantTag:      "3.20",
			wantPlatform: "linux/amd64",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotQuery url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/images/create") {
					gotQuery = r.URL.Query()
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(http.StatusOK) // prune after pull
			}))
			defer srv.Close()

			c := NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
			if err := c.PullImageForPlatform(context.Background(), tc.image, tc.wantPlatform); err != nil {
				t.Fatalf("PullImageForPlatform: %v", err)
			}
			if got := gotQuery.Get("fromImage"); got != tc.wantFrom {
				t.Errorf("fromImage: want %q, got %q", tc.wantFrom, got)
			}
			if got := gotQuery.Get("tag"); got != tc.wantTag {
				t.Errorf("tag: want %q, got %q", tc.wantTag, got)
			}
			if got := gotQuery.Get("platform"); got != tc.wantPlatform {
				t.Errorf("platform: want %q, got %q", tc.wantPlatform, got)
			}
		})
	}
}
