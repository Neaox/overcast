package efs

import (
	"context"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
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

func TestNFSExport_realGaneshaServesNFSv4(t *testing.T) {
	dc := dockerForTest(t)

	svc := New(&config.Config{
		Region: "us-east-1", AccountID: "000000000000",
		EFSMode: config.EFSModeLive, EFSNFSExport: true,
		EFSNFSPortBase: 22600, EFSNFSImage: config.DefaultEFSNFSImage,
		EFSNetwork: "overcast_efs_test",
	}, state.NewMemoryStore(), zap.NewNop(), clock.New())
	svc.docker = dc
	svc.puller = docker.NewImagePuller(dc)
	svc.dockerReady.Store(true)

	ctx := context.Background()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		svc.Stop(stopCtx)
	})

	fs, aerr := svc.createFileSystemTyped(ctx, &createFileSystemRequest{CreationToken: "nfs-e2e-" + newEFSID("")})
	if aerr != nil {
		t.Fatalf("CreateFileSystem: %v", aerr)
	}
	// Reclaim the volume and any container even if an assertion below fails.
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if mts, err := svc.listMountTargetsForFileSystem(cctx, "us-east-1", fs.FileSystemId); err == nil {
			for _, mt := range mts {
				svc.stopExport(cctx, mt)
			}
		}
		if err := dc.RemoveVolume(cctx, volumeName(fs.FileSystemId), true); err != nil {
			t.Logf("cleanup: remove volume: %v", err)
		}
		// The export network is this test's own; production reuses a
		// long-lived one, so only the test removes it. When the test itself
		// runs in a container, exportProbeAddr attached us to the network —
		// Docker refuses to remove a network with endpoints, so detach first.
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			_ = dc.DisconnectNetwork(cctx, "overcast_efs_test", hostname) //nolint:errcheck
		}
		if err := dc.RemoveNetwork(cctx, "overcast_efs_test"); err != nil {
			t.Logf("cleanup: remove network: %v", err)
		}
	})

	mt, aerr := svc.createMountTargetTyped(ctx, &createMountTargetRequest{
		FileSystemId: fs.FileSystemId, SubnetId: "subnet-nfs-e2e",
	})
	if aerr != nil {
		t.Fatalf("CreateMountTarget: %v", aerr)
	}

	// The export starts off the request path and may pull the image on a cold
	// machine, so the budget is generous; the loop exits as soon as the mount
	// target reports available.
	var rec *mountTargetRecord
	pollUntil(t, 5*time.Minute, "mount target available with an export", func() bool {
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
	pollUntil(t, time.Minute, "export container removed", func() bool {
		_, err := dc.InspectContainer(ctx, containerID)
		return docker.IsNotFound(err)
	})
	if used, err := svc.nfsPortInUse(ctx, rec.NFSHostPort); err != nil || used {
		t.Fatalf("expected host port %d released, inUse=%v err=%v", rec.NFSHostPort, used, err)
	}
}

// pollUntil retries cond on a short interval until it holds or the budget runs
// out. Bounded retries rather than a fixed sleep: the test is as fast as the
// daemon allows and still deterministic on a slow machine.
func pollUntil(t *testing.T, budget time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", budget, what)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
