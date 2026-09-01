package ecr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/events"
	"github.com/overcast-sh/overcast/internal/state"
)

func TestRegistryContainerExitInvalidatesAddressAndStartsReplacement(t *testing.T) {
	pinged := make(chan struct{}, 1)
	dockerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			pinged <- struct{}{}
			http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	}))
	t.Cleanup(dockerServer.Close)

	s := New(&config.Config{
		Region: "us-east-1", AccountID: "000000000000",
		LambdaDockerSocket: "tcp://" + strings.TrimPrefix(dockerServer.URL, "http://"),
	}, state.NewMemoryStore(), zap.NewNop(), clock.New())
	s.registryMu.Lock()
	s.registryContainer = "registry-1"
	s.registryName = "overcast-ecr-registry-4510"
	s.registryHost = "localhost"
	s.registryHostPort = 4510
	s.registryPassword = "existing-token-password"
	s.registryInitOnce.Do(func() {})
	s.registryMu.Unlock()

	s.handleRegistryContainerDied(context.Background(), events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: "registry-1", Service: serviceName, ResourceID: ecrRegistryResource, Action: "die",
		},
	})

	select {
	case <-pinged:
	case <-time.After(time.Second):
		t.Fatal("registry replacement was not started")
	}
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if s.registryContainer != "" || s.registryHost != "" || s.registryHostPort != 0 {
		t.Fatalf("stale registry address retained: container=%q host=%q port=%d",
			s.registryContainer, s.registryHost, s.registryHostPort)
	}
	if s.registryPassword != "existing-token-password" {
		t.Fatal("registry replacement changed the password and invalidated issued authorization tokens")
	}
}

func TestRegistryReconnectReplacesMissingCachedContainer(t *testing.T) {
	pinged := make(chan struct{}, 1)
	dockerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			pinged <- struct{}{}
			http.Error(w, "daemon unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "unexpected request", http.StatusNotFound)
	}))
	t.Cleanup(dockerServer.Close)

	s := New(&config.Config{
		Region: "us-east-1", AccountID: "000000000000",
		LambdaDockerSocket: "tcp://" + strings.TrimPrefix(dockerServer.URL, "http://"),
	}, state.NewMemoryStore(), zap.NewNop(), clock.New())
	s.registryMu.Lock()
	s.registryContainer = "registry-missed-exit"
	s.registryHost = "localhost"
	s.registryHostPort = 4510
	s.registryPassword = "existing-token-password"
	s.registryInitOnce.Do(func() {})
	s.registryMu.Unlock()

	s.ReconcileContainers(context.Background(), nil)

	select {
	case <-pinged:
	case <-time.After(time.Second):
		t.Fatal("missing cached registry was not replaced after reconciliation")
	}
}
