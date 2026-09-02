package ecr

// registry_shutdown_test.go — the registry container carries
// HostConfig.AutoRemove, so a shutdown that stops it can be racing Docker's
// own exit-triggered auto-remove goroutine for who actually performs the
// removal. This test freezes that race — the daemon answers our own explicit
// force-remove with the 409 "already in progress" that means AutoRemove won
// it — and proves removeRegistryContainer waits for the daemon's own
// removal-complete signal rather than assuming a discarded 409 means nothing
// is left to wait for.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

func TestRemoveRegistryContainer_waitsForDaemonToConfirmRemoval(t *testing.T) {
	// Given: a daemon where the explicit force-remove loses the race against
	// Docker's own AutoRemove (409 "already in progress"), and the container
	// stays in "removing" — reachable, mid-cleanup — until a signal says the
	// daemon's own background removal actually finished.
	release := make(chan struct{})
	var waitRequested sync.WaitGroup
	waitRequested.Add(1)
	var once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stop"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"removal of container registry-1 is already in progress"}`))
		case strings.HasSuffix(r.URL.Path, "/wait") && r.URL.RawQuery == "condition=removed":
			once.Do(waitRequested.Done)
			<-release
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"StatusCode":0}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	s := &Service{
		log:    serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		docker: docker.NewClient(strings.Replace(srv.URL, "http://", "tcp://", 1), zap.NewNop()),
	}

	done := make(chan struct{})
	go func() {
		s.removeRegistryContainer("registry-1", "overcast-ecr-registry-4510")
		close(done)
	}()

	// Then: it does not return while the daemon's own removal is still
	// working, even though the explicit remove call already came back.
	waitRequested.Wait()
	select {
	case <-done:
		t.Fatal("removeRegistryContainer returned before the daemon confirmed removal")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("removeRegistryContainer did not return after the daemon confirmed removal")
	}
}

func TestRemoveRegistryContainer_alreadyRemovedReturnsPromptly(t *testing.T) {
	// Given: a daemon that has no record of the container by the time
	// shutdown asks — a fast, uncontended removal.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stop"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/wait"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such container: registry-1"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	s := &Service{
		log:    serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		docker: docker.NewClient(strings.Replace(srv.URL, "http://", "tcp://", 1), zap.NewNop()),
	}

	done := make(chan struct{})
	go func() {
		s.removeRegistryContainer("registry-1", "overcast-ecr-registry-4510")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("removeRegistryContainer did not return for an already-removed container")
	}
}
