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
	// helperScripts records the Cmd of each materialization helper container.
	helperScripts []string
	srv           *httptest.Server
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
		// Root-directory materialization helper container lifecycle.
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images/create"):
			w.WriteHeader(http.StatusOK) // pull succeeds with empty progress stream
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create"):
			var req struct {
				Cmd []string `json:"Cmd"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			fd.mu.Lock()
			fd.helperScripts = append(fd.helperScripts, strings.Join(req.Cmd, " "))
			fd.mu.Unlock()
			w.Write([]byte(`{"Id":"helper1"}`)) //nolint:errcheck
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/helper1/json"):
			w.Write([]byte(`{"Id":"helper1","State":{"Status":"exited","Running":false,"ExitCode":0}}`)) //nolint:errcheck
		default:
			// image pull, container start/remove, etc.
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(fd.srv.Close)
	return fd
}

func (fd *fakeVolumeDaemon) helperRuns() []string {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return append([]string(nil), fd.helperScripts...)
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
		// Wired directly (not via SetDocker) so the startup reconciliation
		// goroutine cannot race the per-test assertions on recorded calls.
		dc := docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
		svc.docker = dc
		svc.puller = docker.NewImagePuller(dc)
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

func TestEFSVolumeForAccessPoint(t *testing.T) {
	fd := newFakeVolumeDaemon(t)
	svc := newLiveTestService(t, fd, config.EFSModeLive)
	ctx := context.Background()

	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "resolver"})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	ap, aerr := svc.createAccessPointTyped(ctx, &createAccessPointRequest{ClientToken: "r-1", FileSystemId: fs.FileSystemId})
	if aerr != nil {
		t.Fatalf("CreateAccessPoint: %v", aerr)
	}

	// Live mode resolves the access-point ARN to the backing volume; the
	// default root directory "/" yields no subpath.
	volume, subpath, ok := svc.EFSVolumeForAccessPoint(ctx, ap.AccessPointArn)
	if !ok || volume != volumeName(fs.FileSystemId) || subpath != "" {
		t.Fatalf("expected volume %q with no subpath, got %q/%q ok=%v", volumeName(fs.FileSystemId), volume, subpath, ok)
	}

	// Unknown access points and non-AP ARNs do not resolve.
	if _, _, ok := svc.EFSVolumeForAccessPoint(ctx, "arn:aws:elasticfilesystem:us-east-1:000000000000:access-point/fsap-00000000000000000"); ok {
		t.Fatal("unknown access point must not resolve")
	}
	if _, _, ok := svc.EFSVolumeForAccessPoint(ctx, fs.FileSystemArn); ok {
		t.Fatal("file-system ARN must not resolve")
	}

	// Mock mode never resolves.
	mockSvc := newLiveTestService(t, fd, config.EFSModeMock)
	if _, _, ok := mockSvc.EFSVolumeForAccessPoint(ctx, ap.AccessPointArn); ok {
		t.Fatal("mock mode must not resolve volumes")
	}

	// The file-system-ID resolver (ECS path) follows the same rules.
	volume, ok = svc.EFSVolumeForFileSystem(ctx, fs.FileSystemId)
	if !ok || volume != volumeName(fs.FileSystemId) {
		t.Fatalf("expected file-system resolver to return %q, got %q ok=%v", volumeName(fs.FileSystemId), volume, ok)
	}
	if _, ok := svc.EFSVolumeForFileSystem(ctx, "fs-00000000000000000"); ok {
		t.Fatal("unknown file system must not resolve")
	}
	if _, ok := mockSvc.EFSVolumeForFileSystem(ctx, fs.FileSystemId); ok {
		t.Fatal("mock mode must not resolve file systems")
	}
}

func TestAccessPointRootDirectory_subpathAndMaterialization(t *testing.T) {
	fd := newFakeVolumeDaemon(t)
	svc := newLiveTestService(t, fd, config.EFSModeLive)
	ctx := context.Background()

	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "subdir"})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	uid, gid := int64(1000), int64(1000)
	ap, aerr := svc.createAccessPointTyped(ctx, &createAccessPointRequest{
		ClientToken:  "sub-1",
		FileSystemId: fs.FileSystemId,
		RootDirectory: &RootDirectory{
			Path:         "/app/data",
			CreationInfo: &CreationInfo{OwnerUid: &uid, OwnerGid: &gid, Permissions: "0755"},
		},
	})
	if aerr != nil {
		t.Fatalf("CreateAccessPoint: %v", aerr)
	}

	// Resolution returns the subpath and materializes the directory once.
	volume, subpath, ok := svc.EFSVolumeForAccessPoint(ctx, ap.AccessPointArn)
	if !ok || subpath != "app/data" || volume != volumeName(fs.FileSystemId) {
		t.Fatalf("expected subpath app/data on %q, got %q/%q ok=%v", volumeName(fs.FileSystemId), volume, subpath, ok)
	}
	runs := fd.helperRuns()
	if len(runs) != 1 {
		t.Fatalf("expected 1 materialization helper run, got %v", runs)
	}
	for _, want := range []string{"mkdir -p '/v/app/data'", "chown 1000:1000", "chmod '0755'"} {
		if !strings.Contains(runs[0], want) {
			t.Fatalf("helper script missing %q: %s", want, runs[0])
		}
	}

	// Repeat resolution is deduplicated — no second helper run.
	if _, _, ok := svc.EFSVolumeForAccessPointID(ctx, ap.AccessPointId); !ok {
		t.Fatal("expected access-point ID resolution to succeed")
	}
	if runs := fd.helperRuns(); len(runs) != 1 {
		t.Fatalf("expected materialization to be deduplicated, got %d runs", len(runs))
	}
}

// failingRemoveDaemon wraps the fake daemon so volume removal fails with 409
// (volume in use) a configurable number of times before succeeding.
func TestRemoveVolume_retriesWhileInUse(t *testing.T) {
	var mu sync.Mutex
	removeCalls := 0
	failures := 2
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/volumes/") {
			mu.Lock()
			removeCalls++
			busy := removeCalls <= failures
			mu.Unlock()
			if busy {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	mock := clock.NewMock()
	svc := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000", EFSMode: config.EFSModeLive},
		state.NewMemoryStore(), zap.NewNop(), mock,
	)
	svc.docker = docker.NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
	svc.dockerReady.Store(true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	ctx := context.Background()

	svc.removeVolume(ctx, "fs-busy0000000000000")
	mu.Lock()
	first := removeCalls
	mu.Unlock()
	if first != 1 {
		t.Fatalf("expected 1 immediate attempt, got %d", first)
	}

	// Advance the mock clock until every retry has fired. Repeated Adds are
	// harmless — they just fire pending timers as they appear, absorbing the
	// scheduling races between timer goroutines and this loop.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		calls := removeCalls
		mu.Unlock()
		if calls == failures+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d attempts, got %d", failures+1, calls)
		}
		mock.Add(volumeRemoveRetryDelay + time.Millisecond)
		time.Sleep(5 * time.Millisecond)
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
	// …and one orphaned volume of this instance's own, with no record behind
	// it. It carries this instance's identity, which is what makes it
	// provably litter rather than merely unrecognised — see the scoping tests
	// below.
	fd.listed = []docker.VolumeSummary{
		{Name: volumeName("fs-orphan0000000000"), Labels: svc.managedLabels(ctx, "fs-orphan0000000000")},
		{Name: volumeName("fs-aaaaaaaaaaaaaaaaa"), Labels: svc.managedLabels(ctx, "fs-aaaaaaaaaaaaaaaaa")},
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

// ─── Sweep scoping ────────────────────────────────────────────────────────────
//
// Two Overcast instances share one Docker daemon and keep separate stores, so
// "no record of this file system" means nothing about whether the volume is
// wanted. In live mode these volumes hold the user's actual file data, which
// makes getting it wrong data loss rather than lost scratch space.

// The regression. A volume belonging to a file system this instance has never
// heard of — another Overcast's, still serving it — must survive the sweep.
func TestReconcileVolumes_leavesVolumesOwnedByAnotherInstance(t *testing.T) {
	fd := newFakeVolumeDaemon(t)
	svc := newLiveTestService(t, fd, config.EFSModeLive)
	ctx := context.Background()

	foreign := docker.ManagedLabels(serviceName, "fs-otherinstance00")
	foreign[docker.LabelInstance] = "11111111-2222-3333-4444-555555555555"
	fd.listed = []docker.VolumeSummary{
		{Name: volumeName("fs-otherinstance00"), Labels: foreign},
	}

	svc.reconcileVolumes(ctx)

	if removed := fd.removedVolumes(); len(removed) != 0 {
		t.Fatalf("another instance's live EFS volume was removed: %v", removed)
	}
}

// A volume from a version of Overcast that predates the identity label has no
// owner to establish, and "cannot establish" resolves to "leave it alone".
func TestReconcileVolumes_leavesUnlabelledVolumes(t *testing.T) {
	fd := newFakeVolumeDaemon(t)
	svc := newLiveTestService(t, fd, config.EFSModeLive)
	ctx := context.Background()

	fd.listed = []docker.VolumeSummary{
		{Name: volumeName("fs-nolabel00000000"), Labels: docker.ManagedLabels(serviceName, "fs-nolabel00000000")},
	}

	svc.reconcileVolumes(ctx)

	if removed := fd.removedVolumes(); len(removed) != 0 {
		t.Fatalf("expected an unlabelled volume to be left alone, removed %v", removed)
	}
}

// The sweep still does the job it exists for: a volume this instance stamped
// as its own, whose file system is gone from a store that outlived the
// process, is provably litter.
func TestReconcileVolumes_removesItsOwnOrphan(t *testing.T) {
	fd := newFakeVolumeDaemon(t)
	svc := newLiveTestService(t, fd, config.EFSModeLive)
	ctx := context.Background()

	fd.listed = []docker.VolumeSummary{
		{Name: volumeName("fs-myorphan0000000"), Labels: svc.managedLabels(ctx, "fs-myorphan0000000")},
	}

	svc.reconcileVolumes(ctx)

	want := volumeName("fs-myorphan0000000")
	if removed := fd.removedVolumes(); len(removed) != 1 || removed[0] != want {
		t.Fatalf("expected %q swept, got %v", want, removed)
	}
}

// The identity is read from the store, so its lifetime is the store's. Two
// services over one store — two processes pointed at the same data directory —
// share a sweep domain, because they share the records that say what is still
// wanted. Over separate stores they share nothing.
func TestSweepDomain_followsTheStoreNotTheProcess(t *testing.T) {
	ctx := context.Background()
	newSvc := func(st state.Store) *Service {
		return New(
			&config.Config{Region: "us-east-1", AccountID: "000000000000", EFSMode: config.EFSModeLive},
			st, zap.NewNop(), clock.New(),
		)
	}

	shared := state.NewMemoryStore()
	a, b := newSvc(shared), newSvc(shared)
	if a.sweepDomain(ctx) == "" {
		t.Fatal("expected an identity to be minted")
	}
	if a.sweepDomain(ctx) != b.sweepDomain(ctx) {
		t.Fatalf("instances sharing a store must share a sweep domain: %q vs %q",
			a.sweepDomain(ctx), b.sweepDomain(ctx))
	}

	if c := newSvc(state.NewMemoryStore()); c.sweepDomain(ctx) == a.sweepDomain(ctx) {
		t.Fatal("instances with separate stores must not share a sweep domain")
	}
}

// The memory backend's answer, pinned because it is the first thing a reader
// asks. The store is empty at startup, so every file system is unknown — but
// the identity is gone too, so the previous run's volumes are outside this
// instance's domain and nothing is swept. Before this change the empty store
// meant every EFS volume on the daemon was deleted on every restart.
func TestReconcileVolumes_memoryBackedRestartKeepsPreviousRunsVolumes(t *testing.T) {
	fd := newFakeVolumeDaemon(t)
	ctx := context.Background()

	before := newLiveTestService(t, fd, config.EFSModeLive)
	stamped := before.managedLabels(ctx, "fs-previousrun0000")

	// The restart: a new process over a new in-memory store, holding no record
	// of the file system and no memory of who created its volume.
	after := newLiveTestService(t, fd, config.EFSModeLive)
	fd.listed = []docker.VolumeSummary{
		{Name: volumeName("fs-previousrun0000"), Labels: stamped},
	}

	after.reconcileVolumes(ctx)

	if removed := fd.removedVolumes(); len(removed) != 0 {
		t.Fatalf("a memory-backed restart deleted the previous run's file data: %v", removed)
	}
}
