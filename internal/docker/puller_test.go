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
	"time"

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
		case strings.HasSuffix(r.URL.Path, "/json"):
			// A failed pull asks whether the image is already here; it is not.
			w.WriteHeader(http.StatusNotFound)
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

func TestImagePullerEnsure_rateLimitRetriesWithinSingleCall(t *testing.T) {
	// Given: a registry rate-limiting the first two attempts and then serving
	// the image — the "status 500: {\"message\":\"toomanyrequests: Rate
	// exceeded\"}" shape compat's flake logs captured from RunTask pulling
	// public.ecr.aws/nginx/nginx:latest under CI load (issues #991, #995).
	var pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/images/create"):
			if pulls.Add(1) <= 2 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"toomanyrequests: Rate exceeded"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case strings.Contains(r.URL.Path, "/images/prune"):
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	p := NewImagePuller(&Client{httpClient: srv.Client(), host: srv.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)})
	p.sleep = func(time.Duration) {} // no real backoff in tests

	// When: a single Ensure call rides out the rate limit.
	if err := p.Ensure(context.Background(), "img:1"); err != nil {
		t.Fatalf("Ensure should retry past a transient rate limit within one call, got %v", err)
	}

	// Then: it retried in-place — three attempts on the registry, not a
	// caller-driven retry across separate Ensure calls — and the result is
	// cached like any other success.
	if got := pulls.Load(); got != 3 {
		t.Fatalf("pulls = %d, want 3 (two rate-limited, one success)", got)
	}
	if err := p.Ensure(context.Background(), "img:1"); err != nil || pulls.Load() != 3 {
		t.Fatalf("second Ensure should be a cache hit (err=%v pulls=%d)", err, pulls.Load())
	}
}

func TestImagePullerEnsure_rateLimitMidStreamRetries(t *testing.T) {
	// Given: the other shape the same logs captured — a pull that starts
	// (status 200) and then reports the rate limit as the last line of its
	// progress stream, which is what a failure discovered mid-pull looks like.
	var pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/images/create"):
			if pulls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"status":"Pulling"}` + "\n" + `{"error":"toomanyrequests: Rate exceeded"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case strings.Contains(r.URL.Path, "/images/prune"):
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	p := NewImagePuller(&Client{httpClient: srv.Client(), host: srv.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)})
	p.sleep = func(time.Duration) {}

	// When / Then: Ensure rides out the one rate-limited attempt.
	if err := p.Ensure(context.Background(), "img:1"); err != nil {
		t.Fatalf("Ensure should retry past a mid-stream rate limit, got %v", err)
	}
	if got := pulls.Load(); got != 2 {
		t.Fatalf("pulls = %d, want 2", got)
	}
}

func TestImagePullerEnsure_nonTransientErrorDoesNotRetry(t *testing.T) {
	// Given: a pull that fails for a reason retrying cannot fix. Unrelated to
	// the rate-limit path, and it must not be swept up by it.
	var pulls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/images/create"):
			pulls.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"pull access denied for img, repository does not exist"}`))
		case strings.HasSuffix(r.URL.Path, "/json"):
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	p := NewImagePuller(&Client{httpClient: srv.Client(), host: srv.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)})
	p.sleep = func(time.Duration) { t.Fatal("should not back off for a non-transient error") }

	if err := p.Ensure(context.Background(), "img:1"); err == nil {
		t.Fatal("Ensure should fail on a non-transient error")
	}
	if got := pulls.Load(); got != 1 {
		t.Fatalf("pulls = %d, want 1 (no retry)", got)
	}
}

func TestIsTransientPullError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"status-wrapped rate limit", errors.New(`pull image x: status 500: {"message":"toomanyrequests: Rate exceeded"}`), true},
		{"mid-stream rate limit", errors.New("pull image x: toomanyrequests: Rate exceeded"), true},
		{"case insensitive", errors.New("pull image x: TOOMANYREQUESTS: rate EXCEEDED"), true},
		{"unrelated 404", errors.New(`pull image x: status 404: {"message":"No such image"}`), false},
		{"connection refused", errors.New("pull image x: connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientPullError(tc.err); got != tc.want {
				t.Fatalf("isTransientPullError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestImagePullerEnsure_failedPullSucceedsWhenImageAlreadyLocal(t *testing.T) {
	// Given: a daemon that cannot pull — offline, rate-limited, or (as Docker
	// Desktop's containerd image store does) 404ing a stale lease — but which
	// already holds the image.
	var pulls, inspects atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/images/create"):
			pulls.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"unable to lease content: lease does not exist: not found"}`))
		case strings.Contains(r.URL.Path, "/images/prune"):
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/json"):
			inspects.Add(1)
			_, _ = w.Write([]byte(`{"Id":"sha256:abc"}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	p := NewImagePuller(&Client{httpClient: srv.Client(), host: srv.URL, logger: zap.NewNop(), sem: make(chan struct{}, maxConcurrentOps)})

	// When: a launch asks for the image.
	if err := p.Ensure(context.Background(), "img:1"); err != nil {
		t.Fatalf("Ensure should succeed on a locally present image, got %v", err)
	}

	// Then: the pull was attempted, the local copy was found, and the result is
	// cached like any other success — a container can start.
	if pulls.Load() != 1 || inspects.Load() != 1 {
		t.Fatalf("pulls = %d, inspects = %d, want 1 and 1", pulls.Load(), inspects.Load())
	}
	if err := p.Ensure(context.Background(), "img:1"); err != nil || pulls.Load() != 1 {
		t.Fatalf("second Ensure should be a cache hit (err=%v pulls=%d)", err, pulls.Load())
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
