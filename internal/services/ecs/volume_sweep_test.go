package ecs

// volume_sweep_test.go — the orphan sweep's scoping.
//
// The bug this pins was found by running two Overcast instances against one
// Docker daemon: the second instance's startup sweep deleted a volume belonging
// to a task the *first* instance was still running. The sweep decided a volume
// was abandoned by asking its own task records, and two instances do not share
// records. It now asks the daemon which volumes are referenced, so these tests
// are about the request it sends as much as the volumes it removes.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// fakeVolumeDaemon answers the two calls the sweep makes and records both the
// filters it was asked for and the volumes it was asked to remove.
type fakeVolumeDaemon struct {
	srv *httptest.Server

	mu          sync.Mutex
	listFilters []map[string][]string
	removed     []string
	// volumes is what GET /volumes returns, keyed by whether the request asked
	// for dangling ones only. A daemon would never return a referenced volume
	// under dangling=true, and neither does this.
	all      []docker.VolumeSummary
	dangling []docker.VolumeSummary
}

func newFakeVolumeDaemon(t *testing.T, all, dangling []docker.VolumeSummary) *fakeVolumeDaemon {
	t.Helper()
	fd := &fakeVolumeDaemon{all: all, dangling: dangling}

	fd.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/volumes"):
			filters := map[string][]string{}
			if raw := r.URL.Query().Get("filters"); raw != "" {
				decoded, err := url.QueryUnescape(raw)
				if err == nil {
					_ = json.Unmarshal([]byte(decoded), &filters)
				}
			}
			fd.mu.Lock()
			fd.listFilters = append(fd.listFilters, filters)
			fd.mu.Unlock()

			out := fd.all
			if len(filters["dangling"]) > 0 && filters["dangling"][0] == "true" {
				out = fd.dangling
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Volumes": out})

		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/volumes/"):
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/volumes/")+len("/volumes/"):]
			unescaped, err := url.PathUnescape(name)
			if err == nil {
				name = unescaped
			}
			fd.mu.Lock()
			fd.removed = append(fd.removed, name)
			fd.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(fd.srv.Close)
	return fd
}

func (fd *fakeVolumeDaemon) removedVolumes() []string {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return append([]string(nil), fd.removed...)
}

func (fd *fakeVolumeDaemon) lastFilters() map[string][]string {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	if len(fd.listFilters) == 0 {
		return nil
	}
	return fd.listFilters[len(fd.listFilters)-1]
}

func taskVolume(name, resourceID, scope string) docker.VolumeSummary {
	return docker.VolumeSummary{
		Name: name,
		Labels: map[string]string{
			docker.LabelManaged:    "true",
			docker.LabelService:    serviceName,
			docker.LabelResourceID: resourceID,
			volumeScopeLabel:       scope,
		},
	}
}

func sweepHandler(t *testing.T, fd *fakeVolumeDaemon) *Handler {
	t.Helper()
	return &Handler{
		log:    serviceutil.NewServiceLogger(zap.NewNop(), "ecs"),
		cfg:    &config.Config{Region: "us-east-1"},
		docker: docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop()),
	}
}

// The regression. A volume belonging to a task this instance has never heard
// of — another Overcast's running task — is referenced by a container, so the
// daemon does not report it dangling and the sweep must not touch it.
func TestSweepOrphanedTaskVolumes_leavesVolumesInUseByAnotherInstance(t *testing.T) {
	inUse := taskVolume("overcast-ecs-task-aaaaaaaa-app-src", "default/aaaaaaaa-1111-2222-3333-444444444444", "task")
	orphan := taskVolume("overcast-ecs-task-bbbbbbbb-app-src", "default/bbbbbbbb-1111-2222-3333-444444444444", "task")

	// The other instance's volume is in `all` but not in `dangling`: a
	// container still references it.
	fd := newFakeVolumeDaemon(t, []docker.VolumeSummary{inUse, orphan}, []docker.VolumeSummary{orphan})
	h := sweepHandler(t, fd)

	h.sweepOrphanedTaskVolumes(context.Background())

	got := fd.removedVolumes()
	if len(got) != 1 || got[0] != orphan.Name {
		t.Fatalf("expected only the unreferenced volume removed, got %v", got)
	}
}

// The sweep must ask for dangling volumes, not merely filter what it gets
// back: asking for all of them and deciding locally is the shape of the bug.
func TestSweepOrphanedTaskVolumes_asksDaemonForUnreferencedOnly(t *testing.T) {
	fd := newFakeVolumeDaemon(t, nil, nil)
	h := sweepHandler(t, fd)

	h.sweepOrphanedTaskVolumes(context.Background())

	filters := fd.lastFilters()
	if len(filters["dangling"]) == 0 || filters["dangling"][0] != "true" {
		t.Fatalf("expected a dangling=true filter, got %v", filters)
	}
	// Still scoped to this service's managed volumes.
	labels := strings.Join(filters["label"], ",")
	if !strings.Contains(labels, docker.LabelManaged+"=true") || !strings.Contains(labels, docker.LabelService+"="+serviceName) {
		t.Errorf("expected managed+service label filters, got %v", filters["label"])
	}
}

// A shared-scope volume is unreferenced between tasks by design. It outlives
// every task that mounts it, exactly as one on a container instance does, so
// being dangling is not evidence it is finished with.
func TestSweepOrphanedTaskVolumes_neverRemovesSharedScope(t *testing.T) {
	shared := taskVolume("app-data", "app-data", "shared")
	scoped := taskVolume("overcast-ecs-task-cccccccc-app-src", "default/cccccccc-1111-2222-3333-444444444444", "task")

	fd := newFakeVolumeDaemon(t, []docker.VolumeSummary{shared, scoped}, []docker.VolumeSummary{shared, scoped})
	h := sweepHandler(t, fd)

	h.sweepOrphanedTaskVolumes(context.Background())

	for _, name := range fd.removedVolumes() {
		if name == shared.Name {
			t.Fatal("shared-scope volume was removed; it must outlive the tasks that mount it")
		}
	}
	if got := fd.removedVolumes(); len(got) != 1 || got[0] != scoped.Name {
		t.Fatalf("expected only the task-scoped volume removed, got %v", got)
	}
}

// A volume a user created themselves carries no scope label, so it is not
// Overcast's to remove even when it is dangling.
func TestSweepOrphanedTaskVolumes_ignoresUnlabelledVolumes(t *testing.T) {
	foreign := docker.VolumeSummary{Name: "somebody-elses-volume", Labels: map[string]string{
		docker.LabelManaged: "true",
		docker.LabelService: serviceName,
	}}

	fd := newFakeVolumeDaemon(t, []docker.VolumeSummary{foreign}, []docker.VolumeSummary{foreign})
	h := sweepHandler(t, fd)

	h.sweepOrphanedTaskVolumes(context.Background())

	if got := fd.removedVolumes(); len(got) != 0 {
		t.Fatalf("expected nothing removed, got %v", got)
	}
}
