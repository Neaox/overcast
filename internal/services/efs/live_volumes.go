package efs

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Live mode (OVERCAST_EFS_MODE=live) backs each file system with a named
// Docker volume so emulated compute can share real file data. Volume
// operations are best-effort: the control plane never fails because Docker is
// slow or absent — a file system without its volume is exactly what mock mode
// provides, and reconciliation heals the gap once Docker returns.

// volumeNamePrefix names live-mode volumes "overcast-efs-<FileSystemId>".
const volumeNamePrefix = "overcast-efs-"

// volumeOpTimeout bounds each background Docker volume call.
const volumeOpTimeout = 30 * time.Second

func volumeName(fsID string) string { return volumeNamePrefix + fsID }

// SetDocker wires the Docker client used for live-mode volumes. Called by the
// router once the Docker probe succeeds; kicks off asynchronous volume
// reconciliation so file systems persisted before a restart regain their
// volumes and orphaned volumes are removed.
func (s *Service) SetDocker(dc *docker.Client) {
	s.docker = dc
	s.dockerReady.Store(dc != nil)
	if dc != nil && s.liveModeEnabled() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), volumeOpTimeout)
			defer cancel()
			s.reconcileVolumes(ctx)
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

// removeVolume removes the backing volume for a deleted file system.
func (s *Service) removeVolume(ctx context.Context, fsID string) {
	if !s.volumesActive() {
		return
	}
	if err := s.docker.RemoveVolume(ctx, volumeName(fsID), true); err != nil {
		s.log.Warn("efs: remove volume failed — volume may be orphaned until reconciliation",
			zap.String("file_system", fsID), zap.Error(err))
	}
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
