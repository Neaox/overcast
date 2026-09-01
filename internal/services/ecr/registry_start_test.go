package ecr

// registry_start_test.go — bringing the shared registry up when a predecessor
// still holds its name.
//
// Docker keeps a container name reserved until removal finishes, and removal
// is asynchronous: the container is already gone from inspect while the name
// is not yet free. A restarted Overcast therefore meets a 409 on create for a
// moment. Treating that as fatal left the registry down for the whole process
// — `docker push` to the emulated ECR refused, and every ECS task launched
// from an image in it failed to pull.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// registryDaemon is a fake Docker daemon that answers the first conflicts
// worth of container creates with 409, as one still reaping a predecessor does.
func registryDaemon(t *testing.T, conflicts int32) (*Service, *atomic.Int32) {
	t.Helper()
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/containers/create"):
			if creates.Add(1) <= conflicts {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"Conflict. The container name \"/overcast-ecr-registry\" is already in use"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(docker.CreateContainerResponse{ID: "registry-1"})
		case strings.HasSuffix(r.URL.Path, "/json"):
			// Inspect during the conflict window: already gone.
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	s := &Service{
		cfg:    &config.Config{Hostname: "localhost", AccountID: "000000000000", Region: "us-east-1"},
		log:    serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		docker: docker.NewClient(strings.Replace(srv.URL, "http://", "tcp://", 1), zap.NewNop()),
	}
	return s, &creates
}

func TestCreateRegistryContainer_waitsOutAPredecessorsName(t *testing.T) {
	// Given: a daemon that conflicts twice before the name comes free.
	s, creates := registryDaemon(t, 2)

	// When: the registry container is created.
	id, err := s.createRegistryContainer(context.Background(), ecrRegistryNamePrefix+"-4510", &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{Image: ecrRegistryImage},
	})

	// Then: it is created rather than abandoned, after retrying.
	if err != nil {
		t.Fatalf("createRegistryContainer: %v", err)
	}
	if id != "registry-1" {
		t.Errorf("container id = %q, want registry-1", id)
	}
	if got := creates.Load(); got != 3 {
		t.Errorf("create attempts = %d, want 3", got)
	}
}

func TestReapLegacyRegistry_neverTouchesSiblingsInAnyState(t *testing.T) {
	// Given: a daemon holding a legacy singleton-named registry plus sibling
	// registries under per-claim names in every lifecycle state — including
	// "created", which is a registry mid-birth between its create and start.
	var removed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/overcast-ecr-registry/json"):
			_, _ = w.Write([]byte(`{"Id":"legacy-1","Config":{"Labels":{
				"overcast.managed":"true","overcast.service":"ecr","overcast.resource-id":"registry"}}}`))
		case r.Method == http.MethodDelete:
			removed = append(removed, strings.TrimPrefix(r.URL.Path, "/v1.45/containers/"))
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/stop"):
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(r.URL.Path, "/containers/json"):
			// The sweep must not even ask — a list is the first step of the
			// race that force-removed a sibling's newborn registry — but if it
			// does, hand it every tempting target.
			_, _ = w.Write([]byte(`[
				{"Id":"sib-created","State":"created","Labels":{"overcast.managed":"true","overcast.service":"ecr","overcast.resource-id":"registry"}},
				{"Id":"sib-exited","State":"exited","Labels":{"overcast.managed":"true","overcast.service":"ecr","overcast.resource-id":"registry"}},
				{"Id":"sib-running","State":"running","Labels":{"overcast.managed":"true","overcast.service":"ecr","overcast.resource-id":"registry"}}
			]`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	s := &Service{
		cfg:    &config.Config{},
		log:    serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		docker: docker.NewClient(strings.Replace(srv.URL, "http://", "tcp://", 1), zap.NewNop()),
	}

	// When: startup's sweep runs.
	s.reapLegacyRegistry(context.Background())

	// Then: exactly the legacy container went; every sibling survived.
	if len(removed) != 1 || removed[0] != "legacy-1" {
		t.Errorf("removed = %v, want exactly the legacy singleton", removed)
	}
}

func TestCreateRegistryContainer_nonConflictFailsImmediately(t *testing.T) {
	// Given: a daemon that refuses the create for a reason waiting cannot fix.
	var creates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		creates.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"no such image: registry:2"}`))
	}))
	defer srv.Close()
	s := &Service{
		cfg:    &config.Config{},
		log:    serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		docker: docker.NewClient(strings.Replace(srv.URL, "http://", "tcp://", 1), zap.NewNop()),
	}

	// When: the registry container is created.
	_, err := s.createRegistryContainer(context.Background(), ecrRegistryNamePrefix+"-4510", &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{Image: ecrRegistryImage},
	})

	// Then: it gives up at once — retrying a real failure only delays the log
	// line that explains it.
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := creates.Load(); got != 1 {
		t.Errorf("create attempts = %d, want 1", got)
	}
}
