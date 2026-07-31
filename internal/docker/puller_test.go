package docker

// puller_test.go — pins the ImagePuller's resilience contract: a failed pull
// never bricks an image for the process lifetime, and a create that finds
// its image removed behind our back re-pulls and retries once.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
)

func TestIsImageMissingErr(t *testing.T) {
	// The daemon's create error for a removed image must be recognized so
	// launch paths re-pull instead of failing until restart; unrelated
	// failures must not trigger a re-pull.
	if !IsImageMissingErr(errors.New(`create container: status 404: {"message":"No such image: public.ecr.aws/lambda/nodejs:22"}`)) {
		t.Fatal("daemon no-such-image error not recognized")
	}
	if IsImageMissingErr(errors.New("create container: connection refused")) {
		t.Fatal("unrelated error misclassified as missing image")
	}
	if IsImageMissingErr(nil) {
		t.Fatal("nil error misclassified")
	}
}

func TestImagePullerEnsure_failedPullRetriesOnNextCall(t *testing.T) {
	// Given: a daemon whose first pull fails and second succeeds.
	var pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/images/create"):
			if pulls.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case strings.Contains(r.URL.Path, "/images/prune"):
			_, _ = w.Write([]byte(`{}`)) // post-pull dangling-image prune
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	p := NewImagePuller(&Client{httpClient: srv.Client(), host: srv.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)})

	// When: Ensure fails once and is called again — the next user-driven
	// launch attempt.
	if err := p.Ensure(context.Background(), "img:1"); err == nil {
		t.Fatal("first pull should have failed")
	}
	if err := p.Ensure(context.Background(), "img:1"); err != nil {
		t.Fatalf("second pull should retry and succeed, got %v", err)
	}

	// Then: exactly two pulls happened, and further calls are cached.
	if got := pulls.Load(); got != 2 {
		t.Fatalf("pulls = %d, want 2", got)
	}
	if err := p.Ensure(context.Background(), "img:1"); err != nil || pulls.Load() != 2 {
		t.Fatalf("third Ensure should be a cache hit (err=%v pulls=%d)", err, pulls.Load())
	}
}

func TestCreateContainerWithRetry_missingImageRepullsOnce(t *testing.T) {
	// Given: a daemon where the image was removed behind our back — the first
	// create 404s with "No such image", the re-pull succeeds, and the second
	// create works.
	var creates, pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/containers/create"):
			if creates.Add(1) == 1 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such image: img:1"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(CreateContainerResponse{ID: "c1"})
		case strings.Contains(r.URL.Path, "/images/create"):
			pulls.Add(1)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case strings.Contains(r.URL.Path, "/images/prune"):
			_, _ = w.Write([]byte(`{}`)) // post-pull dangling-image prune
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	p := NewImagePuller(&Client{httpClient: srv.Client(), host: srv.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)})
	// Simulate the image having been pulled earlier in the process.
	if err := p.Ensure(context.Background(), "img:1"); err != nil {
		t.Fatalf("seed pull: %v", err)
	}

	// When: a container is created after the image vanished.
	id, err := p.CreateContainerWithRetry(context.Background(), "c", &CreateContainerRequest{
		ContainerConfig: &ContainerConfig{Image: "img:1"},
	})

	// Then: the stale pull record was dropped, the image re-pulled, and the
	// create retried successfully.
	if err != nil || id != "c1" {
		t.Fatalf("create = (%q, %v), want (c1, nil)", id, err)
	}
	if creates.Load() != 2 || pulls.Load() != 2 {
		t.Fatalf("creates=%d pulls=%d, want 2 and 2", creates.Load(), pulls.Load())
	}
}
