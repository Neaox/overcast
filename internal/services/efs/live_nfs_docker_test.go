package efs

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/dataplane"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/state"
)

// This file exercises the NFS data plane against a real Docker daemon: a real
// NFS-Ganesha container, a real published port, and a real NFSv4 call over the
// wire. It is skipped only when no daemon is reachable — the emulator's Docker
// paths are first-class and CI runs them (see compat/AGENTS.md § Docker-
// dependent tests are first-class).
//
// Everything here polls with a bounded retry budget; there are no fixed
// sleeps, so the test finishes as soon as the daemon does its part.

// dockerForTest returns a client for the daemon, or skips.
func dockerForTest(t *testing.T) *docker.Client {
	t.Helper()
	socket := os.Getenv("EFS_DOCKER_SOCKET")
	if socket == "" {
		socket = os.Getenv("LAMBDA_DOCKER_SOCKET")
	}
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	dc := docker.NewClient(socket, zap.NewNop())
	if !dc.Available(5 * time.Second) {
		t.Skip("Docker not available, skipping EFS NFS data-plane test")
	}
	return dc
}

// newDockerNFSService builds a live-mode service wired to a real daemon, on
// its own port base and export network so two tests can run back to back. It
// returns the service and a diagnostic renderer for poll failures.
func newDockerNFSService(t *testing.T, dc *docker.Client, portBase int, network string) (*Service, func() string) {
	t.Helper()
	// Log at warn so a failure can name the emulator's own reason instead of
	// just reporting a timeout.
	logs, logged := observer.New(zap.WarnLevel)

	cfg := &config.Config{
		Region: "us-east-1", AccountID: "000000000000",
		EFSMode: config.EFSModeLive, EFSNFSExport: true,
		EFSNFSPortBase: portBase, EFSNFSImage: config.DefaultEFSNFSImage,
		Network: network,
	}
	// The Docker supervisor creates both planes before it hands a service its
	// client, so services no longer ensure networks themselves. This test has
	// no supervisor, so it stands in for one.
	for _, plane := range dataplane.Networks(cfg, dataplane.Placement{}) {
		if _, err := dc.CreateNetwork(context.Background(), plane); err != nil {
			t.Fatalf("create plane %s: %v", plane, err)
		}
	}

	svc := New(cfg, state.NewMemoryStore(), zap.New(logs), clock.New())
	svc.docker = dc
	svc.puller = docker.NewImagePuller(dc)
	svc.dockerReady.Store(true)

	// warnings renders everything the service complained about, so a timeout
	// says why rather than only that it happened.
	warnings := func() string {
		var b strings.Builder
		for _, e := range logged.All() {
			b.WriteString("\n  " + e.Message + " " + fmt.Sprint(e.ContextMap()))
		}
		if b.Len() == 0 {
			return "\n  (the service logged no warnings)"
		}
		return b.String()
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		svc.Stop(stopCtx)
	})
	return svc, warnings
}

// cleanupFileSystem reclaims a file system's export containers, its backing
// volume and the test's own export network, even when an assertion failed.
func cleanupFileSystem(t *testing.T, svc *Service, dc *docker.Client, fsID, network string) {
	t.Helper()
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if mts, err := svc.listMountTargetsForFileSystem(cctx, "us-east-1", fsID); err == nil {
			for _, mt := range mts {
				svc.stopExport(cctx, mt)
			}
		}
		if err := dc.RemoveVolume(cctx, volumeName(fsID), true); err != nil {
			t.Logf("cleanup: remove volume: %v", err)
		}
		// The planes are this test's own; production reuses long-lived ones, so
		// only the test removes them. When the test itself runs in a container,
		// exportProbeAddr attached us to the control plane — Docker refuses to
		// remove a network with endpoints, so detach first.
		hostname, hostErr := os.Hostname()
		for _, plane := range dataplane.Networks(&config.Config{Network: network}, dataplane.Placement{}) {
			if hostErr == nil && hostname != "" {
				_ = dc.DisconnectNetwork(cctx, plane, hostname) //nolint:errcheck
			}
			if err := dc.RemoveNetwork(cctx, plane); err != nil {
				t.Logf("cleanup: remove network %s: %v", plane, err)
			}
		}
	})
}

// ensureExportImage fetches the export image as setup rather than as part of
// what is being measured. It is a few hundred megabytes, so a cold pull on a
// CI runner takes minutes — folding that into a lifecycle assertion would make
// a slow network look like a broken export.
func ensureExportImage(t *testing.T, dc *docker.Client, ctx context.Context) {
	t.Helper()
	if err := docker.NewImagePuller(dc).Ensure(ctx, config.DefaultEFSNFSImage); err != nil {
		t.Skipf("cannot fetch the NFS export image %s: %v", config.DefaultEFSNFSImage, err)
	}
}

func TestNFSExport_realGaneshaServesNFSv4(t *testing.T) {
	dc := dockerForTest(t)

	ctx := context.Background()
	ensureExportImage(t, dc, ctx)

	svc, warnings := newDockerNFSService(t, dc, 22600, "overcast_efs_test")

	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "nfs-e2e-" + newEFSID("")})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	cleanupFileSystem(t, svc, dc, fs.FileSystemId, "overcast_efs_test")

	mt, aerr := svc.createMountTargetTyped(ctx, &createMountTargetRequest{
		FileSystemId: fs.FileSystemId, SubnetId: "subnet-nfs-e2e",
	})
	if aerr != nil {
		t.Fatalf("CreateMountTarget: %v", aerr)
	}

	// The export starts off the request path, but the image is already local,
	// so this is a container start plus a readiness probe.
	var rec *mountTargetRecord
	pollUntil(t, 2*time.Minute, "mount target available with an export", warnings, func() bool {
		got, found, err := svc.getMountTarget(ctx, "us-east-1", mt.MountTargetId)
		if err != nil || !found || got.LifeCycleState != stateAvailable || got.NFSContainerId == "" {
			return false
		}
		rec = got
		return true
	})

	if rec.NFSHostPort < 22600 {
		t.Fatalf("expected a published host port at or above the configured base, got %d", rec.NFSHostPort)
	}

	// The export answers a real NFSv4 NULL call. This is the whole point of
	// step 4: a client that speaks NFS can reach the file system's data.
	// exportProbeAddr resolves the same way production does — the published
	// host port normally, the container IP when the test itself runs in a
	// container and cannot reach the host's port bindings.
	addr := svc.exportProbeAddr(ctx, rec.NFSContainerId, rec.NFSHostPort)
	if err := nfsNullPing(ctx, addr, 5*time.Second); err != nil {
		logs, _ := dc.ContainerLogs(ctx, rec.NFSContainerId, "40")
		t.Fatalf("NFSv4 NULL call to %s failed: %v\nganesha logs:\n%s", addr, err, logs)
	}

	// Deleting the mount target reclaims the container and the port.
	containerID := rec.NFSContainerId
	if _, aerr := svc.deleteMountTargetTyped(ctx, &deleteMountTargetRequest{MountTargetId: mt.MountTargetId}); aerr != nil {
		t.Fatalf("DeleteMountTarget: %v", aerr)
	}
	pollUntil(t, time.Minute, "export container removed", warnings, func() bool {
		_, err := dc.InspectContainer(ctx, containerID)
		return docker.IsNotFound(err)
	})
	if used, err := svc.nfsPortInUse(ctx, rec.NFSHostPort); err != nil || used {
		t.Fatalf("expected host port %d released, inUse=%v err=%v", rec.NFSHostPort, used, err)
	}
}

// TestNFSExport_realGaneshaWithAccessPoint is the regression test for an
// access point taking the whole export down. Ganesha grafts each export's
// Pseudo path onto its pseudo-filesystem and cannot create a directory inside
// a non-PSEUDO export, so with the volume exported at "/" the access point's
// "/fsap-…" made the daemon exit FATAL within seconds — leaving a mount target
// that reported "available" with no export behind it at all.
func TestNFSExport_realGaneshaWithAccessPoint(t *testing.T) {
	dc := dockerForTest(t)

	ctx := context.Background()
	ensureExportImage(t, dc, ctx)

	svc, warnings := newDockerNFSService(t, dc, 22700, "overcast_efs_ap_test")

	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "nfs-ap-e2e-" + newEFSID("")})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	cleanupFileSystem(t, svc, dc, fs.FileSystemId, "overcast_efs_ap_test")

	uid, gid := int64(1000), int64(1000)
	ap, aerr := svc.createAccessPointTyped(ctx, &createAccessPointRequest{
		ClientToken:  "nfs-ap-e2e",
		FileSystemId: fs.FileSystemId,
		PosixUser:    &PosixUser{Uid: &uid, Gid: &gid},
		RootDirectory: &RootDirectory{
			Path:         "/apone",
			CreationInfo: &CreationInfo{OwnerUid: &uid, OwnerGid: &gid, Permissions: "0777"},
		},
	})
	if aerr != nil {
		t.Fatalf("CreateAccessPoint: %v", aerr)
	}

	mt, aerr := svc.createMountTargetTyped(ctx, &createMountTargetRequest{
		FileSystemId: fs.FileSystemId, SubnetId: "subnet-nfs-ap-e2e",
	})
	if aerr != nil {
		t.Fatalf("CreateMountTarget: %v", aerr)
	}

	var rec *mountTargetRecord
	pollUntil(t, 2*time.Minute, "mount target available with an export", warnings, func() bool {
		got, found, err := svc.getMountTarget(ctx, "us-east-1", mt.MountTargetId)
		if err != nil || !found || got.LifeCycleState != stateAvailable || got.NFSContainerId == "" {
			return false
		}
		rec = got
		return true
	})

	// The daemon must still be alive: a pseudo-filesystem it cannot build is a
	// FATAL exit, not a degraded export.
	info, err := dc.InspectContainer(ctx, rec.NFSContainerId)
	if err != nil {
		t.Fatalf("inspect export container: %v", err)
	}
	if !info.State.Running {
		logs, _ := dc.ContainerLogs(ctx, rec.NFSContainerId, "40")
		t.Fatalf("export container for access point %s exited (%s, code %d):\n%s",
			ap.AccessPointId, info.State.Status, info.State.ExitCode, logs)
	}
	if err := nfsNullPing(ctx, svc.exportProbeAddr(ctx, rec.NFSContainerId, rec.NFSHostPort), 5*time.Second); err != nil {
		logs, _ := dc.ContainerLogs(ctx, rec.NFSContainerId, "40")
		t.Fatalf("NFSv4 NULL call failed with an access point exported: %v\nganesha logs:\n%s", err, logs)
	}
}

// pollUntil retries cond on a short interval until it holds or the budget runs
// out. Bounded retries rather than a fixed sleep: the test is as fast as the
// daemon allows and still deterministic on a slow machine. diag is rendered
// into the failure so a timeout explains itself.
func pollUntil(t *testing.T, budget time.Duration, what string, diag func() string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s; service warnings:%s", budget, what, diag())
		}
		time.Sleep(250 * time.Millisecond)
	}
}
