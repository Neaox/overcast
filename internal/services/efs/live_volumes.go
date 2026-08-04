package efs

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Live mode (the default; OVERCAST_EFS_MODE=mock opts out) backs each file
// system with a named Docker volume so emulated compute can share real file
// data. Volume operations are best-effort: the control plane never fails
// because Docker is slow or absent — a file system without its volume is
// exactly what mock mode provides, and reconciliation heals the gap once
// Docker returns. That degradation is what makes live safe as a default.

// volumeNamePrefix names live-mode volumes "overcast-efs-<FileSystemId>".
const volumeNamePrefix = "overcast-efs-"

// volumeOpTimeout bounds each background Docker volume call.
const volumeOpTimeout = 30 * time.Second

func volumeName(fsID string) string { return volumeNamePrefix + fsID }

// SetDocker wires the Docker client used for live-mode volumes. Called by the
// router once the Docker probe succeeds; kicks off asynchronous reconciliation
// so file systems persisted before a restart regain their volumes, mount
// targets re-adopt or restart their NFS exports, and orphans are swept.
func (s *Service) SetDocker(dc *docker.Client) {
	s.docker = dc
	if dc != nil {
		s.puller = docker.NewImagePuller(dc)
	}
	s.dockerReady.Store(dc != nil)
	if dc != nil && s.liveModeEnabled() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), volumeOpTimeout)
			defer cancel()
			s.reconcileVolumes(ctx)
			// Containers second: an export needs its file system's volume to
			// exist before it can be started.
			s.reconcileExports(ctx)
		}()
	}
}

func (s *Service) liveModeEnabled() bool {
	return s.cfg != nil && s.cfg.EFSMode == config.EFSModeLive
}

// volumesActive reports whether live-mode volume operations should run.
func (s *Service) volumesActive() bool {
	return s.liveModeEnabled() && s.dockerReady.Load()
}

// ensureVolume creates the backing volume for a file system. Safe to repeat:
// Docker's volume create is idempotent by name.
func (s *Service) ensureVolume(ctx context.Context, fsID string) {
	if !s.volumesActive() {
		return
	}
	if err := s.docker.CreateVolume(ctx, volumeName(fsID), docker.ManagedLabels(serviceName, fsID)); err != nil {
		s.log.Warn("efs: create volume failed — file system remains metadata-only",
			zap.String("file_system", fsID), zap.Error(err))
	}
}

// Volume removal retry policy: a volume still held by a container (e.g. a
// task that hasn't exited yet) fails with 409; a few spaced retries cover the
// normal shutdown window, and startup reconciliation remains the backstop.
const (
	volumeRemoveRetries    = 3
	volumeRemoveRetryDelay = 30 * time.Second
)

// removeVolume removes the backing volume for a deleted file system,
// retrying while the volume is still in use.
func (s *Service) removeVolume(ctx context.Context, fsID string) {
	s.removeVolumeAttempt(ctx, fsID, 0)
}

func (s *Service) removeVolumeAttempt(ctx context.Context, fsID string, attempt int) {
	if !s.volumesActive() {
		return
	}
	err := s.docker.RemoveVolume(ctx, volumeName(fsID), true)
	if err == nil {
		return
	}
	if attempt >= volumeRemoveRetries {
		s.log.Warn("efs: remove volume failed after retries — orphaned until next startup reconciliation",
			zap.String("file_system", fsID), zap.Error(err))
		return
	}
	s.log.Warn("efs: remove volume failed — will retry",
		zap.String("file_system", fsID), zap.Int("attempt", attempt+1), zap.Error(err))
	s.scheduler.AfterScoped(s.region(ctx), fsID, "volume-remove", volumeRemoveRetryDelay, func(ctx context.Context) {
		s.removeVolumeAttempt(ctx, fsID, attempt+1)
	})
}

// EFSVolumeForAccessPoint implements the volume-resolver interface consumed
// by Lambda: it maps an access-point ARN to the Docker volume backing its
// file system, plus the access point's root directory as a volume subpath
// ("" for the volume root). ok is false outside live mode, while Docker is
// unavailable, or for an unknown access point — the caller then runs the
// workload without the mount rather than failing it.
func (s *Service) EFSVolumeForAccessPoint(ctx context.Context, accessPointARN string) (string, string, bool) {
	region := serviceutil.ARNRegion(accessPointARN)
	if region == "" {
		region = s.cfg.Region
	}
	return s.accessPointVolume(ctx, region, resourceIDFromARN(accessPointARN))
}

// EFSVolumeForAccessPointID is EFSVolumeForAccessPoint for callers that hold
// the bare access-point ID (ECS authorizationConfig); the access point is
// resolved in the caller's request region.
func (s *Service) EFSVolumeForAccessPointID(ctx context.Context, accessPointID string) (string, string, bool) {
	return s.accessPointVolume(ctx, s.region(ctx), accessPointID)
}

// accessPointVolume resolves an access point to (volume, subpath). When the
// access point declares a root directory with CreationInfo, the directory is
// materialized in the volume first (AWS creates it on first mount) — without
// CreationInfo a missing directory makes the Docker mount fail, which mirrors
// AWS's mount failure.
func (s *Service) accessPointVolume(ctx context.Context, region, accessPointID string) (string, string, bool) {
	if !s.volumesActive() || !strings.HasPrefix(accessPointID, "fsap-") {
		return "", "", false
	}
	ap, found, err := s.getAccessPoint(ctx, region, accessPointID)
	if err != nil || !found {
		return "", "", false
	}
	volume := volumeName(ap.FileSystemId)
	subpath := ""
	if ap.RootDirectory != nil {
		subpath = strings.Trim(ap.RootDirectory.Path, "/")
	}
	if subpath != "" && ap.RootDirectory.CreationInfo != nil {
		s.ensureVolumeSubdir(ctx, volume, subpath, ap.RootDirectory.CreationInfo)
	}
	return volume, subpath, true
}

// EFSVolumeForFileSystem implements the volume-resolver interface consumed by
// ECS: it maps a file system ID (resolved in the caller's request region) to
// its backing Docker volume. Same ok=false semantics as
// EFSVolumeForAccessPoint.
func (s *Service) EFSVolumeForFileSystem(ctx context.Context, fileSystemID string) (string, bool) {
	if !s.volumesActive() {
		return "", false
	}
	rec, found, err := s.getFileSystem(ctx, s.region(ctx), fileSystemID)
	if err != nil || !found {
		return "", false
	}
	return volumeName(rec.FileSystemId), true
}

// reconcileVolumes aligns Docker volumes with persisted file systems across
// every region: file systems missing their volume get one, and managed
// volumes whose file system no longer exists are removed. Idempotent.
func (s *Service) reconcileVolumes(ctx context.Context) {
	if !s.volumesActive() {
		return
	}

	// Every region: scan with an empty region so keys keep their region prefix.
	pairs, err := s.store.Scan(ctx, nsFileSystems, "")
	if err != nil {
		s.log.Warn("efs: volume reconciliation: list file systems", zap.Error(err))
		return
	}
	known := make(map[string]struct{}, len(pairs))
	for _, kv := range pairs {
		_, fsID := serviceutil.SplitRegionKey(kv.Key)
		if fsID == "" {
			continue
		}
		known[fsID] = struct{}{}
		s.ensureVolume(ctx, fsID)
	}

	volumes, err := s.docker.ListVolumes(ctx, serviceName)
	if err != nil {
		s.log.Warn("efs: volume reconciliation: list volumes", zap.Error(err))
		return
	}
	for _, v := range volumes {
		fsID := v.ResourceID()
		if fsID == "" {
			continue
		}
		if _, ok := known[fsID]; ok {
			continue
		}
		s.log.Info("efs: removing orphaned volume", zap.String("volume", v.Name))
		if err := s.docker.RemoveVolume(ctx, v.Name, true); err != nil {
			s.log.Warn("efs: remove orphaned volume failed", zap.String("volume", v.Name), zap.Error(err))
		}
	}
}
