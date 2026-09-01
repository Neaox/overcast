package ecr

// registry_volume_test.go — the storage the registry container is created with.
//
// registry:2 keeps its blobs under /var/lib/registry, and the image declares
// that path a VOLUME. Left to itself Docker satisfies that with an anonymous
// volume, which AutoRemove reaps along with the container — so every image
// pushed to the emulated ECR died with the process. Mounting a *named* volume
// there is what makes a push survive a restart, and named volumes are exactly
// the ones neither AutoRemove nor a container delete takes with them.
//
// These tests are about the create request's shape, against a fake daemon.
// That a restart really finds the images is
// tests/integration/ecr/registry_persistence_test.go, which needs a real one.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// volumeDaemon is a fake Docker daemon that records the volume creates and the
// container-create payload, and answers everything startRegistry needs to run
// to completion.
type volumeDaemon struct {
	mu sync.Mutex
	// volumesCreated holds one entry per POST /volumes/create.
	volumesCreated []struct {
		Name   string            `json:"Name"`
		Labels map[string]string `json:"Labels"`
	}
	// created is the container-create request body.
	created docker.CreateContainerRequest
	// deleted holds the paths of every DELETE the daemon received, so a test
	// can tell a container removal from a volume removal.
	deleted []string
}

func newVolumeDaemon(t *testing.T, cfg *config.Config) (*Service, *volumeDaemon) {
	t.Helper()
	d := &volumeDaemon{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/volumes/create"):
			var req struct {
				Name   string            `json:"Name"`
				Labels map[string]string `json:"Labels"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			d.volumesCreated = append(d.volumesCreated, req)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Name":"` + req.Name + `"}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/containers/create"):
			_ = json.NewDecoder(r.Body).Decode(&d.created)
			_ = json.NewEncoder(w).Encode(docker.CreateContainerResponse{ID: "registry-1"})
		case r.Method == http.MethodDelete:
			d.deleted = append(d.deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/archive"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/start"), strings.HasSuffix(r.URL.Path, "/stop"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/json"):
			// Inspect: a started registry with its port published. Also serves
			// the by-name lookup replaceRegistryHolder makes.
			_, _ = w.Write([]byte(`{"Id":"registry-1","Name":"/overcast-ecr-registry-4510",
				"Config":{"Labels":{"overcast.managed":"true","overcast.service":"ecr","overcast.resource-id":"registry"}},
				"NetworkSettings":{"Ports":{"5000/tcp":[{"HostPort":"4510"}]}}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	s := &Service{
		cfg:    cfg,
		log:    serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
		docker: docker.NewClient(strings.Replace(srv.URL, "http://", "tcp://", 1), zap.NewNop()),
	}
	return s, d
}

// registryMount returns the create request's mount at the registry's storage
// path, if there is one.
func (d *volumeDaemon) registryMount() (docker.Mount, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.created.HostConfig == nil {
		return docker.Mount{}, false
	}
	for _, m := range d.created.HostConfig.Mounts {
		if m.Target == ecrRegistryStoragePath {
			return m, true
		}
	}
	return docker.Mount{}, false
}

func TestStartRegistry_fixedPortClaimMountsANamedVolume(t *testing.T) {
	// Given: the fixed-port claim, with persistence at its default.
	s, d := newVolumeDaemon(t, &config.Config{ECRRegistryPort: 4510, ECRRegistryPersist: true})

	// When: the registry container is started on that port.
	if _, _, err := s.startRegistry(context.Background(), ecrRegistryNamePrefix+"-4510", nil, "4510"); err != nil {
		t.Fatalf("startRegistry: %v", err)
	}

	// Then: a named volume was created for it, carrying the same managed labels
	// the container does so `docker volume ls --filter label=overcast.service=ecr`
	// finds it — which is the whole reclaim path.
	d.mu.Lock()
	volumes := d.volumesCreated
	d.mu.Unlock()
	if len(volumes) != 1 {
		t.Fatalf("volumes created = %d, want 1", len(volumes))
	}
	if want := ecrRegistryNamePrefix + "-data-4510"; volumes[0].Name != want {
		t.Errorf("volume name = %q, want %q", volumes[0].Name, want)
	}
	for key, want := range docker.ManagedLabels(serviceName, ecrRegistryResource) {
		if got := volumes[0].Labels[key]; got != want {
			t.Errorf("volume label %s = %q, want %q", key, got, want)
		}
	}

	// And: the container mounts it at the registry's storage path.
	mount, ok := d.registryMount()
	if !ok {
		t.Fatalf("no mount at %s in %#v", ecrRegistryStoragePath, d.created.HostConfig)
	}
	if mount.Type != "volume" {
		t.Errorf("mount type = %q, want volume", mount.Type)
	}
	if want := ecrRegistryNamePrefix + "-data-4510"; mount.Source != want {
		t.Errorf("mount source = %q, want %q", mount.Source, want)
	}
	if mount.ReadOnly {
		t.Error("mount is read-only; the registry has to write to it")
	}
}

func TestStartRegistry_ephemeralClaimGetsNoVolume(t *testing.T) {
	// Given: an ephemeral claim — a random container name and a daemon-assigned
	// port. Persistence is on; it is the claim that disqualifies it.
	s, d := newVolumeDaemon(t, &config.Config{ECRRegistryPort: 4510, ECRRegistryPersist: true})

	// When: the registry is started under an ephemeral name and no host port.
	if _, _, err := s.startRegistry(context.Background(), ephemeralRegistryName(), nil, ""); err != nil {
		t.Fatalf("startRegistry: %v", err)
	}

	// Then: no volume was created. There is nothing stable to name one after —
	// the container name is random by design and the port changes every run —
	// so a volume here would be a new orphan on every start, with no reclaim
	// key and no restart that could ever find it again.
	d.mu.Lock()
	volumes := len(d.volumesCreated)
	d.mu.Unlock()
	if volumes != 0 {
		t.Errorf("volumes created = %d, want 0", volumes)
	}
	if mount, ok := d.registryMount(); ok {
		t.Errorf("unexpected mount %#v", mount)
	}
}

func TestStartRegistry_persistenceOffGetsNoVolume(t *testing.T) {
	// Given: the fixed-port claim with OVERCAST_ECR_REGISTRY_PERSIST=false.
	s, d := newVolumeDaemon(t, &config.Config{ECRRegistryPort: 4510, ECRRegistryPersist: false})

	// When: the registry container is started.
	if _, _, err := s.startRegistry(context.Background(), ecrRegistryNamePrefix+"-4510", nil, "4510"); err != nil {
		t.Fatalf("startRegistry: %v", err)
	}

	// Then: the opt-out really opts out — back to a registry whose contents die
	// with its container.
	d.mu.Lock()
	volumes := len(d.volumesCreated)
	d.mu.Unlock()
	if volumes != 0 {
		t.Errorf("volumes created = %d, want 0", volumes)
	}
	if mount, ok := d.registryMount(); ok {
		t.Errorf("unexpected mount %#v", mount)
	}
}

func TestReplaceRegistryHolder_removesThePredecessorButNotItsData(t *testing.T) {
	// Given: a fixed-port name already held by an Overcast-managed registry —
	// a predecessor left running by an unclean shutdown.
	s, d := newVolumeDaemon(t, &config.Config{ECRRegistryPort: 4510, ECRRegistryPersist: true})

	// When: startup replaces it.
	s.replaceRegistryHolder(context.Background(), ecrRegistryNamePrefix+"-4510")

	// Then: the container went and the volume did not. Removing the volume here
	// would delete the images this change exists to keep — the successor is
	// meant to inherit them, not to start from nothing behind a persistent
	// facade.
	d.mu.Lock()
	deleted := append([]string(nil), d.deleted...)
	d.mu.Unlock()
	var containers, volumes int
	for _, path := range deleted {
		switch {
		case strings.Contains(path, "/volumes/"):
			volumes++
		case strings.Contains(path, "/containers/"):
			containers++
		default:
			t.Errorf("unexpected delete of %q", path)
		}
	}
	if containers != 1 {
		t.Errorf("containers removed = %d, want 1", containers)
	}
	if volumes != 0 {
		t.Errorf("volumes removed = %d, want 0", volumes)
	}
}
