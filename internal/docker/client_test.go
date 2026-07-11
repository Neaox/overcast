package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
