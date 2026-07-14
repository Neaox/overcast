package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1.45/images/create":
			gotImage = r.URL.Query().Get("fromImage")
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
	if gotImage != "public.ecr.aws/lambda/nodejs:22" {
		t.Fatalf("fromImage query = %q", gotImage)
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
