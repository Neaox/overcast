package efs

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

// ─── Fake Docker daemon with container lifecycle ──────────────────────────────

// createdContainer records one POST /containers/create.
type createdContainer struct {
	Name         string
	Image        string
	Cmd          []string
	Labels       map[string]string
	Mounts       []docker.Mount
	PortBindings map[string][]docker.PortBinding
}

// fakeNFSDaemon speaks enough of the Engine API for export containers, and
// lets a test block inside StartContainer to reproduce the delete-mid-start
// interleaving deterministically.
type fakeNFSDaemon struct {
	mu         sync.Mutex
	created    []createdContainer
	started    []string
	stopped    []string
	removed    []string
	listed     []docker.ContainerSummary
	inspectRun map[string]bool // container ID → State.Running (default true)
	// exists holds every name and ID the daemon knows about, so inspecting an
	// unknown container 404s the way a real daemon does.
	exists map[string]bool

	// startGate, when non-nil, blocks every StartContainer until closed.
	startGate chan struct{}

	srv *httptest.Server
}

func newFakeNFSDaemon(t *testing.T) *fakeNFSDaemon {
	t.Helper()
	fd := &fakeNFSDaemon{inspectRun: map[string]bool{}, exists: map[string]bool{}}
	fd.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/volumes/create"):
			w.Write([]byte(`{"Name":"v"}`)) //nolint:errcheck

		case r.Method == http.MethodGet && strings.HasSuffix(path, "/volumes"):
			_ = json.NewEncoder(w).Encode(struct {
				Volumes []docker.VolumeSummary `json:"Volumes"`
			}{})

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/images/create"):
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/containers/create"):
			var req struct {
				Image      string            `json:"Image"`
				Cmd        []string          `json:"Cmd"`
				Labels     map[string]string `json:"Labels"`
				HostConfig struct {
					Mounts       []docker.Mount                  `json:"Mounts"`
					PortBindings map[string][]docker.PortBinding `json:"PortBindings"`
				} `json:"HostConfig"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			name := r.URL.Query().Get("name")
			fd.mu.Lock()
			fd.created = append(fd.created, createdContainer{
				Name: name, Image: req.Image, Cmd: req.Cmd, Labels: req.Labels,
				Mounts: req.HostConfig.Mounts, PortBindings: req.HostConfig.PortBindings,
			})
			id := "ctr-" + name
			fd.exists[name], fd.exists[id] = true, true
			// Root-directory helpers are one-shot: report them exited so the
			// caller's wait loop finishes. Exports keep running.
			fd.inspectRun[id] = !strings.HasPrefix(name, "overcast-efs-mkdir-")
			fd.mu.Unlock()
			w.Write([]byte(`{"Id":"` + id + `"}`)) //nolint:errcheck

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/start"):
			fd.mu.Lock()
			gate := fd.startGate
			fd.mu.Unlock()
			if gate != nil {
				<-gate
			}
			fd.mu.Lock()
			fd.started = append(fd.started, containerIDFromPath(path))
			fd.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/stop"):
			fd.mu.Lock()
			fd.stopped = append(fd.stopped, containerIDFromPath(path))
			fd.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodDelete && strings.Contains(path, "/containers/"):
			id := containerIDFromPath(path)
			fd.mu.Lock()
			fd.removed = append(fd.removed, id)
			delete(fd.exists, id)
			fd.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodGet && strings.HasSuffix(path, "/containers/json"):
			fd.mu.Lock()
			out := append([]docker.ContainerSummary(nil), fd.listed...)
			fd.mu.Unlock()
			_ = json.NewEncoder(w).Encode(out)

		case r.Method == http.MethodGet && strings.HasSuffix(path, "/json"):
			id := containerIDFromPath(path)
			fd.mu.Lock()
			known := fd.exists[id]
			running, ok := fd.inspectRun[id]
			if !ok {
				running = true
			}
			fd.mu.Unlock()
			if !known {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"message":"No such container"}`)) //nolint:errcheck
				return
			}
			status := "running"
			if !running {
				status = "exited"
			}
			w.Write([]byte(`{"Id":"` + id + `","State":{"Status":"` + status + `","Running":` + //nolint:errcheck
				boolLit(running) + `,"ExitCode":0}}`))

		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(fd.srv.Close)
	return fd
}

func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// containerIDFromPath pulls the container ID out of "/v1.x/containers/<id>/<verb>".
func containerIDFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range parts {
		if p == "containers" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func (fd *fakeNFSDaemon) createdContainers() []createdContainer {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return append([]createdContainer(nil), fd.created...)
}

func (fd *fakeNFSDaemon) removedContainers() []string {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	return append([]string(nil), fd.removed...)
}

// newNFSTestService builds a live-mode Service with the NFS data plane on,
// wired to the fake daemon. Readiness probing is disabled by default so unit
// tests never dial a real socket; TestNFSReadiness covers the probe itself.
func newNFSTestService(t *testing.T, fd *fakeNFSDaemon, nfs bool) *Service {
	t.Helper()
	cfg := &config.Config{
		Region: "us-east-1", AccountID: "000000000000",
		EFSMode: config.EFSModeLive, EFSNFSExport: nfs,
		EFSNFSPortBase: 22049, EFSNFSImage: "ganesha:test",
	}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	if fd != nil {
		dc := docker.NewClient("tcp://"+fd.srv.Listener.Addr().String(), zap.NewNop())
		svc.docker = dc
		svc.puller = docker.NewImagePuller(dc)
		svc.dockerReady.Store(true)
	}
	svc.skipNFSReadiness = true
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		svc.Stop(ctx)
	})
	return svc
}

// seedFileSystem creates an available file system for mount-target tests.
func seedFileSystem(t *testing.T, svc *Service, ctx context.Context, token string) string {
	t.Helper()
	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: token})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	return fs.FileSystemId
}

// eventually polls cond until it holds, failing the test after 3s. Export
// starts run off the request path, so assertions on them poll rather than
// sleep for a fixed interval.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// awaitExport waits for a mount target's export container to be recorded and
// returns the record.
func awaitExport(t *testing.T, svc *Service, ctx context.Context, region, mtID string) *mountTargetRecord {
	t.Helper()
	var rec *mountTargetRecord
	eventually(t, "export container recorded on "+mtID, func() bool {
		got, found, err := svc.getMountTarget(ctx, region, mtID)
		if err != nil || !found || got.NFSContainerId == "" {
			return false
		}
		rec = got
		return true
	})
	return rec
}

// ─── Export lifecycle ─────────────────────────────────────────────────────────

func TestNFSExport_mountTargetStartsAndStopsGanesha(t *testing.T) {
	fd := newFakeNFSDaemon(t)
	svc := newNFSTestService(t, fd, true)
	ctx := context.Background()

	fsID := seedFileSystem(t, svc, ctx, "nfs-1")
	mt, aerr := svc.createMountTargetTyped(ctx, &createMountTargetRequest{
		FileSystemId: fsID, SubnetId: "subnet-1111",
	})
	if aerr != nil {
		t.Fatalf("CreateMountTarget: %v", aerr)
	}

	rec := awaitExport(t, svc, ctx, "us-east-1", mt.MountTargetId)

	created := fd.createdContainers()
	if len(created) != 1 {
		t.Fatalf("expected 1 export container, got %d: %+v", len(created), created)
	}
	c := created[0]
	if want := nfsContainerName(mt.MountTargetId); c.Name != want {
		t.Fatalf("container name: want %q, got %q", want, c.Name)
	}
	if c.Image != "ganesha:test" {
		t.Fatalf("image: want the configured image, got %q", c.Image)
	}
	if c.Labels[docker.LabelResourceID] != mt.MountTargetId || c.Labels[docker.LabelService] != serviceName {
		t.Fatalf("managed labels missing/wrong: %v", c.Labels)
	}
	if len(c.Mounts) != 1 || c.Mounts[0].Source != volumeName(fsID) || c.Mounts[0].Target != nfsExportRoot {
		t.Fatalf("expected the file system volume mounted at %s, got %+v", nfsExportRoot, c.Mounts)
	}
	if bindings := c.PortBindings[nfsContainerPort]; len(bindings) != 1 || bindings[0].HostPort == "" {
		t.Fatalf("expected 2049 published on a host port, got %+v", c.PortBindings)
	}

	// The mount target records the container and port so delete can reclaim
	// them, and reaches "available" once the export is up.
	if rec.NFSHostPort == 0 {
		t.Fatalf("expected a host port persisted, got %+v", rec)
	}
	eventually(t, "mount target available", func() bool {
		got, found, err := svc.getMountTarget(ctx, "us-east-1", mt.MountTargetId)
		return err == nil && found && got.LifeCycleState == stateAvailable
	})

	// Delete tears the container down.
	if _, aerr := svc.deleteMountTargetTyped(ctx, &deleteMountTargetRequest{MountTargetId: mt.MountTargetId}); aerr != nil {
		t.Fatalf("DeleteMountTarget: %v", aerr)
	}
	// Teardown runs off the request path, so poll for it.
	eventually(t, "export container removed", func() bool {
		for _, id := range fd.removedContainers() {
			if id == rec.NFSContainerId {
				return true
			}
		}
		return false
	})
	// The host port is released back to the pool.
	eventually(t, "host port released", func() bool {
		used, err := svc.nfsPortInUse(ctx, rec.NFSHostPort)
		return err == nil && !used
	})
}

func TestNFSExport_offByDefaultInLiveMode(t *testing.T) {
	fd := newFakeNFSDaemon(t)
	svc := newNFSTestService(t, fd, false)
	ctx := context.Background()

	fsID := seedFileSystem(t, svc, ctx, "nfs-off")
	mt, aerr := svc.createMountTargetTyped(ctx, &createMountTargetRequest{
		FileSystemId: fsID, SubnetId: "subnet-2222",
	})
	if aerr != nil {
		t.Fatalf("CreateMountTarget: %v", aerr)
	}
	if created := fd.createdContainers(); len(created) != 0 {
		t.Fatalf("NFS export must be opt-in; got containers %+v", created)
	}
	// The mount target still reaches "available" inline, as before step 4.
	got, found, err := svc.getMountTarget(ctx, "us-east-1", mt.MountTargetId)
	if err != nil || !found || got.LifeCycleState != stateAvailable {
		t.Fatalf("expected available mount target, got %+v (found=%v err=%v)", got, found, err)
	}
}

// TestNFSExport_deletedMidStartTearsDownContainer reproduces the leak class
// fixed for RDS (#412), ElastiCache (#459) and MSK/Lambda (#461): the delete
// lands while the container is still starting, so the record it reads carries
// no container ID and the start goroutine is the only party that can clean up.
func TestNFSExport_deletedMidStartTearsDownContainer(t *testing.T) {
	fd := newFakeNFSDaemon(t)
	fd.mu.Lock()
	fd.startGate = make(chan struct{})
	fd.mu.Unlock()

	svc := newNFSTestService(t, fd, true)
	ctx := context.Background()

	fsID := seedFileSystem(t, svc, ctx, "nfs-race")
	mt, aerr := svc.createMountTargetTyped(ctx, &createMountTargetRequest{
		FileSystemId: fsID, SubnetId: "subnet-3333",
	})
	if aerr != nil {
		t.Fatalf("CreateMountTarget: %v", aerr)
	}

	// Wait until the container exists and the start goroutine is parked inside
	// StartContainer, then delete: the stored record has no container ID yet,
	// so delete cannot reclaim it.
	eventually(t, "export container created", func() bool {
		return len(fd.createdContainers()) == 1
	})
	if _, aerr := svc.deleteMountTargetTyped(ctx, &deleteMountTargetRequest{MountTargetId: mt.MountTargetId}); aerr != nil {
		t.Fatalf("DeleteMountTarget: %v", aerr)
	}
	if removed := fd.removedContainers(); len(removed) != 0 {
		t.Fatalf("delete found a container ID it should not have had: %v", removed)
	}

	// Release the start; the goroutine must notice the record is gone and
	// remove the container it owns.
	fd.mu.Lock()
	close(fd.startGate)
	fd.startGate = nil
	fd.mu.Unlock()

	wantID := "ctr-" + nfsContainerName(mt.MountTargetId)
	deadline := time.Now().Add(3 * time.Second)
	for {
		removed := fd.removedContainers()
		for _, id := range removed {
			if id == wantID {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("start goroutine leaked container %q; removed=%v", wantID, removed)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ─── Reconciliation ───────────────────────────────────────────────────────────

func TestReconcileExports_adoptsKnownAndSweepsOrphans(t *testing.T) {
	fd := newFakeNFSDaemon(t)
	svc := newNFSTestService(t, fd, true)
	ctx := context.Background()

	// A mount target persisted across a restart, with its container running.
	if err := svc.putMountTarget(ctx, "eu-west-1", &mountTargetRecord{
		MountTargetId: "fsmt-aaaaaaaaaaaaaaaaa", FileSystemId: "fs-aaaaaaaaaaaaaaaaa",
		LifeCycleState: stateAvailable,
	}); err != nil {
		t.Fatalf("seed mount target: %v", err)
	}

	fd.mu.Lock()
	fd.listed = []docker.ContainerSummary{
		{
			ID:     "ctr-adopt",
			Names:  []string{"/" + nfsContainerName("fsmt-aaaaaaaaaaaaaaaaa")},
			State:  "running",
			Labels: docker.ManagedLabels(serviceName, "fsmt-aaaaaaaaaaaaaaaaa"),
			Ports: []struct {
				HostPort      int    `json:"PublicPort"`
				ContainerPort int    `json:"PrivatePort"`
				Type          string `json:"Type"`
			}{{HostPort: 22050, ContainerPort: 2049, Type: "tcp"}},
		},
		{
			ID:     "ctr-orphan",
			Names:  []string{"/" + nfsContainerName("fsmt-zzzzzzzzzzzzzzzzz")},
			State:  "running",
			Labels: docker.ManagedLabels(serviceName, "fsmt-zzzzzzzzzzzzzzzzz"),
		},
		{
			// A one-shot root-directory helper that outlived its process.
			ID:     "ctr-helper",
			Names:  []string{"/overcast-efs-mkdir-abc"},
			State:  "exited",
			Labels: docker.ManagedLabels(serviceName, volumeName("fs-aaaaaaaaaaaaaaaaa")),
		},
	}
	fd.mu.Unlock()

	svc.reconcileExports(ctx)

	// The surviving mount target adopts its container and port…
	rec, found, err := svc.getMountTarget(ctx, "eu-west-1", "fsmt-aaaaaaaaaaaaaaaaa")
	if err != nil || !found {
		t.Fatalf("get mount target: found=%v err=%v", found, err)
	}
	if rec.NFSContainerId != "ctr-adopt" || rec.NFSHostPort != 22050 {
		t.Fatalf("expected adoption of ctr-adopt on 22050, got %+v", rec)
	}
	if used, err := svc.nfsPortInUse(ctx, 22050); err != nil || !used {
		t.Fatalf("expected adopted port reserved, inUse=%v err=%v", used, err)
	}

	// …and both the orphaned export and the stale helper are removed.
	removed := fd.removedContainers()
	wantRemoved := map[string]bool{"ctr-orphan": false, "ctr-helper": false}
	for _, id := range removed {
		if _, ok := wantRemoved[id]; ok {
			wantRemoved[id] = true
		}
		if id == "ctr-adopt" {
			t.Fatalf("reconciliation removed a live export container")
		}
	}
	for id, got := range wantRemoved {
		if !got {
			t.Fatalf("expected %q swept, removed=%v", id, removed)
		}
	}
}

// ─── Ganesha configuration ────────────────────────────────────────────────────

func TestGaneshaConfig_rootAndAccessPointPseudoPaths(t *testing.T) {
	uid, gid := int64(1234), int64(5678)
	cfg := buildGaneshaConfig([]ganeshaExport{
		{ID: 1, Path: nfsExportRoot, Pseudo: "/"},
		{ID: 2, Path: nfsExportRoot + "/app/data", Pseudo: "/fsap-1111", AnonUID: &uid, AnonGID: &gid},
	})

	for _, want := range []string{
		"Export_Id = 1;",
		"Path = " + nfsExportRoot + ";",
		"Pseudo = /;",
		"Squash = No_Root_Squash;",
		"Export_Id = 2;",
		"Path = " + nfsExportRoot + "/app/data;",
		"Pseudo = /fsap-1111;",
		"Squash = All_Squash;",
		"Anonymous_Uid = 1234;",
		"Anonymous_Gid = 5678;",
		"Protocols = 4;",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("ganesha config missing %q:\n%s", want, cfg)
		}
	}
}

func TestGaneshaExports_accessPointsBecomePseudoPaths(t *testing.T) {
	fd := newFakeNFSDaemon(t)
	svc := newNFSTestService(t, fd, true)
	ctx := context.Background()

	fsID := seedFileSystem(t, svc, ctx, "nfs-ap")
	uid, gid := int64(1000), int64(1000)
	ap, aerr := svc.createAccessPointTyped(ctx, &createAccessPointRequest{
		ClientToken: "ap-1", FileSystemId: fsID,
		PosixUser:     &PosixUser{Uid: &uid, Gid: &gid},
		RootDirectory: &RootDirectory{Path: "/app/data", CreationInfo: &CreationInfo{OwnerUid: &uid, OwnerGid: &gid, Permissions: "0755"}},
	})
	if aerr != nil {
		t.Fatalf("CreateAccessPoint: %v", aerr)
	}

	exports, err := svc.ganeshaExportsFor(ctx, "us-east-1", fsID)
	if err != nil {
		t.Fatalf("ganeshaExportsFor: %v", err)
	}
	if len(exports) != 2 {
		t.Fatalf("expected root export plus one access point, got %+v", exports)
	}
	if exports[0].Pseudo != "/" || exports[0].Path != nfsExportRoot {
		t.Fatalf("first export must be the volume root, got %+v", exports[0])
	}
	got := exports[1]
	if got.Pseudo != "/"+ap.AccessPointId {
		t.Fatalf("access point pseudo path: want /%s, got %q", ap.AccessPointId, got.Pseudo)
	}
	if got.Path != nfsExportRoot+"/app/data" {
		t.Fatalf("access point path: want the root directory, got %q", got.Path)
	}
	if got.AnonUID == nil || *got.AnonUID != uid || got.AnonGID == nil || *got.AnonGID != gid {
		t.Fatalf("PosixUser must become Anonymous_Uid/Gid, got %+v", got)
	}
}

// TestGaneshaExports_unsafeRootDirectoryIsSkipped guards the config generator
// against an access point whose root directory cannot be embedded in
// ganesha.conf. CreateAccessPoint does not constrain the path, so a newline
// could otherwise terminate the heredoc that carries the config into the
// container and leave the rest of it running as shell.
func TestGaneshaExports_unsafeRootDirectoryIsSkipped(t *testing.T) {
	fd := newFakeNFSDaemon(t)
	svc := newNFSTestService(t, fd, true)
	ctx := context.Background()

	fsID := seedFileSystem(t, svc, ctx, "nfs-unsafe")
	for i, path := range []string{
		"/app\nOVERCAST_GANESHA_EOF\nrm -rf /",
		"/app; Export_Id = 99;",
		"/app dir",
		"/app'quote",
		"/app#comment",
	} {
		if _, aerr := svc.createAccessPointTyped(ctx, &createAccessPointRequest{
			ClientToken:   fmt.Sprintf("unsafe-%d", i),
			FileSystemId:  fsID,
			RootDirectory: &RootDirectory{Path: path},
		}); aerr != nil {
			t.Fatalf("CreateAccessPoint(%q): %v", path, aerr)
		}
	}

	exports, err := svc.ganeshaExportsFor(ctx, "us-east-1", fsID)
	if err != nil {
		t.Fatalf("ganeshaExportsFor: %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("expected only the root export, got %+v", exports)
	}
	// The generated config must contain exactly one export terminator and no
	// trace of the injected text.
	cfg := buildGaneshaConfig(exports)
	for _, banned := range []string{"OVERCAST_GANESHA_EOF", "rm -rf", "Export_Id = 99"} {
		if strings.Contains(cfg, banned) {
			t.Fatalf("generated config leaked %q:\n%s", banned, cfg)
		}
	}
	if got := strings.Count(ganeshaStartScript(cfg), "OVERCAST_GANESHA_EOF"); got != 2 {
		t.Fatalf("expected exactly one heredoc open/close pair, got %d markers", got)
	}
}

// ─── Readiness probe ──────────────────────────────────────────────────────────

// TestNFSReadiness_nullRPC checks the probe against a hand-rolled server that
// answers one NFSv4 NULL call, and against one that accepts the connection but
// says nothing — a bare TCP dial cannot tell those apart.
func TestNFSReadiness_nullRPC(t *testing.T) {
	t.Run("answers", func(t *testing.T) {
		ln := nfsNullResponder(t, true)
		if err := nfsNullPing(context.Background(), ln.Addr().String(), time.Second); err != nil {
			t.Fatalf("expected the probe to succeed: %v", err)
		}
	})
	t.Run("silent", func(t *testing.T) {
		ln := nfsNullResponder(t, false)
		if err := nfsNullPing(context.Background(), ln.Addr().String(), 200*time.Millisecond); err == nil {
			t.Fatal("expected the probe to fail against a socket that never replies")
		}
	})
	t.Run("closed", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		addr := ln.Addr().String()
		ln.Close()
		if err := nfsNullPing(context.Background(), addr, 200*time.Millisecond); err == nil {
			t.Fatal("expected the probe to fail against a closed port")
		}
	})
}

// nfsNullResponder listens on a random port and, when reply is true, answers
// the first record-marked RPC call with a MSG_ACCEPTED/SUCCESS reply echoing
// the caller's XID.
func nfsNullResponder(t *testing.T, reply bool) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		hdr := make([]byte, 4)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		n := int(be32(hdr) &^ 0x80000000)
		body := make([]byte, n)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		if !reply {
			<-time.After(time.Second)
			return
		}
		out := make([]byte, 0, 24)
		out = append(out, body[:4]...)            // xid, echoed
		out = append(out, 0, 0, 0, 1)             // msg_type: REPLY
		out = append(out, 0, 0, 0, 0)             // reply_stat: MSG_ACCEPTED
		out = append(out, 0, 0, 0, 0, 0, 0, 0, 0) // verf: AUTH_NONE, length 0
		out = append(out, 0, 0, 0, 0)             // accept_stat: SUCCESS
		frame := binary.BigEndian.AppendUint32(nil, 0x80000000|uint32(len(out)))
		conn.Write(append(frame, out...)) //nolint:errcheck
	}()
	return ln
}
