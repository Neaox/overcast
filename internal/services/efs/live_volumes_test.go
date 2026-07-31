package efs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/state"
)

// fakeVolumeDaemon is an httptest server speaking just enough of the Docker
// Engine volume API, recording every create/remove call.
type fakeVolumeDaemon struct {
	mu      sync.Mutex
	created []string
	removed []string
	// listed is the canned response for GET /volumes.
	listed []docker.VolumeSummary
	srv    *httptest.Server
}

func newFakeVolumeDaemon(t *testing.T) *fakeVolumeDaemon {
	t.Helper()
	fd := &fakeVolumeDaemon{}
	fd.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/volumes/create"):
			var req struct {
				Name string `json:"Name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			fd.mu.Lock()
			fd.created = append(fd.created, req.Name)
			fd.mu.Unlock()
			w.Write([]byte(`{"Name":"` + req.Name + `"}`)) //nolint:errcheck
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/volumes/"):
			name := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
			fd.mu.Lock()
			fd.removed = append(fd.removed, name)
			fd.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/volumes"):
			fd.mu.Lock()
			resp := struct {
				Volumes []docker.VolumeSummary `json:"Volumes"`
			}{Volumes: fd.listed}
			fd.mu.Unlock()
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(fd.srv.Close)
	return fd
}

func (fd *fakeVolumeDaemon) createdVolumes() []string {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return append([]string(nil), fd.created...)
}

func (fd *fakeVolumeDaemon) removedVolumes() []string {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return append([]string(nil), fd.removed...)
}

// newLiveTestService builds a live-mode Service wired to the fake daemon.
// A real clock keeps 0-delay lifecycle transitions inline, so volume calls
// are synchronous with the API call that triggers them.
func newLiveTestService(t *testing.T, fd *fakeVolumeDaemon, mode config.EFSMode) *Service {
	t.Helper()
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EFSMode: mode},
		state.NewMemoryStore(), zap.NewNop(), clock.New(),
	)
	if fd != nil {
		svc.docker = docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
		svc.dockerReady.Store(true)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	return svc
}

func TestLiveMode_createAndDeleteManageVolume(t *testing.T) {
	fd := newFakeVolumeDaemon(t)
	svc := newLiveTestService(t, fd, config.EFSModeLive)
	ctx := context.Background()

	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "live-1"})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	want := volumeName(fs.FileSystemId)
	if created := fd.createdVolumes(); len(created) != 1 || created[0] != want {
		t.Fatalf("expected volume %q created, got %v", want, created)
	}

	if _, aerr := svc.deleteFileSystemTyped(ctx, &deleteFileSystemRequest{FileSystemId: fs.FileSystemId}); aerr != nil {
		t.Fatalf("DeleteFileSystem: %v", aerr)
	}
	if removed := fd.removedVolumes(); len(removed) != 1 || removed[0] != want {
		t.Fatalf("expected volume %q removed, got %v", want, removed)
	}
}

func TestMockMode_neverTouchesDocker(t *testing.T) {
	fd := newFakeVolumeDaemon(t)
	svc := newLiveTestService(t, fd, config.EFSModeMock)
	ctx := context.Background()

	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "mock-1"})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	if _, aerr := svc.deleteFileSystemTyped(ctx, &deleteFileSystemRequest{FileSystemId: fs.FileSystemId}); aerr != nil {
		t.Fatalf("DeleteFileSystem: %v", aerr)
	}
	if len(fd.createdVolumes()) != 0 || len(fd.removedVolumes()) != 0 {
		t.Fatalf("mock mode must not call Docker; created=%v removed=%v", fd.createdVolumes(), fd.removedVolumes())
	}
}

func TestLiveMode_dockerUnavailableStaysControlPlaneOnly(t *testing.T) {
	// No Docker wired at all: create/delete must still succeed.
	svc := newLiveTestService(t, nil, config.EFSModeLive)
	ctx := context.Background()

	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "no-docker"})
	if aerr != nil {
		t.Fatalf("CreateFileSystem without Docker: %v", aerr)
	}
	if _, aerr := svc.deleteFileSystemTyped(ctx, &deleteFileSystemRequest{FileSystemId: fs.FileSystemId}); aerr != nil {
		t.Fatalf("DeleteFileSystem without Docker: %v", aerr)
	}
}

func TestReconcileVolumes_healsAndSweeps(t *testing.T) {
	fd := newFakeVolumeDaemon(t)
	svc := newLiveTestService(t, fd, config.EFSModeLive)
	ctx := context.Background()

	// Two persisted file systems in different regions (as after a restart)…
	for _, rec := range []struct{ region, id string }{
		{"us-east-1", "fs-aaaaaaaaaaaaaaaaa"},
		{"eu-west-1", "fs-bbbbbbbbbbbbbbbbb"},
	} {
		if err := svc.putFileSystem(ctx, rec.region, &fileSystemRecord{
			FileSystemId: rec.id, CreationToken: rec.id, LifeCycleState: stateAvailable,
		}); err != nil {
			t.Fatalf("seed file system: %v", err)
		}
	}
	// …and one orphaned managed volume with no record behind it.
	fd.listed = []docker.VolumeSummary{
		{Name: volumeName("fs-orphan0000000000"), Labels: docker.ManagedLabels("efs", "fs-orphan0000000000")},
		{Name: volumeName("fs-aaaaaaaaaaaaaaaaa"), Labels: docker.ManagedLabels("efs", "fs-aaaaaaaaaaaaaaaaa")},
	}

	svc.reconcileVolumes(ctx)

	created := fd.createdVolumes()
	if len(created) != 2 {
		t.Fatalf("expected both persisted file systems to get volumes, got %v", created)
	}
	removed := fd.removedVolumes()
	if len(removed) != 1 || removed[0] != volumeName("fs-orphan0000000000") {
		t.Fatalf("expected only the orphan removed, got %v", removed)
	}
}
