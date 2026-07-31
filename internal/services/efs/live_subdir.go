package efs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
)

// Access points whose root directory carries CreationInfo have that directory
// created on first mount by AWS, with the declared ownership and permissions.
// Overcast mirrors this by running a short-lived helper container that mkdirs
// the subpath inside the backing volume before the first subpath mount —
// Docker rejects a volume Subpath mount whose directory does not exist.

// efsHelperImage is the image used for root-directory materialization. Small,
// multi-arch, and long-lived upstream.
const efsHelperImage = "busybox:1.36"

// subdirRunTimeout bounds one materialization run (image pull excluded — the
// puller has its own discipline).
const subdirRunTimeout = 30 * time.Second

// ensureVolumeSubdir materializes subpath inside the named volume with the
// CreationInfo's ownership and permissions. Idempotent and deduplicated:
// each volume+subpath pair runs at most one successful helper per process
// lifetime (mkdir -p makes re-runs after restart harmless). Failures are
// logged and left for the mount to surface — the same visible outcome AWS
// gives when a root directory cannot be created.
func (s *Service) ensureVolumeSubdir(ctx context.Context, volume, subpath string, ci *CreationInfo) {
	key := volume + "/" + subpath
	if _, done := s.materialized.Load(key); done {
		return
	}
	if err := s.runSubdirHelper(ctx, volume, subpath, ci); err != nil {
		s.log.Warn("efs: root-directory materialization failed — subpath mounts may be rejected",
			zap.String("volume", volume), zap.String("subpath", subpath), zap.Error(err))
		return
	}
	s.materialized.Store(key, struct{}{})
}

// runSubdirHelper runs the one-shot helper container and waits for it.
func (s *Service) runSubdirHelper(ctx context.Context, volume, subpath string, ci *CreationInfo) error {
	if s.puller == nil {
		return fmt.Errorf("no image puller wired")
	}
	if err := s.puller.Ensure(ctx, efsHelperImage); err != nil {
		return fmt.Errorf("pull helper image: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, subdirRunTimeout)
	defer cancel()

	script := buildSubdirScript(subpath, ci)
	name := "overcast-efs-mkdir-" + newEFSID("")
	id, err := s.docker.CreateContainer(ctx, name, &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image:      efsHelperImage,
			Entrypoint: []string{"/bin/sh", "-c"},
			Cmd:        []string{script},
			Labels:     docker.ManagedLabels(serviceName, volume),
		},
		HostConfig: &docker.HostConfig{
			Mounts: []docker.Mount{{Type: "volume", Source: volume, Target: "/v"}},
		},
	})
	if err != nil {
		return fmt.Errorf("create helper container: %w", err)
	}
	defer func() {
		if err := s.docker.RemoveContainer(context.Background(), id, true); err != nil {
			s.log.Warn("efs: remove materialization helper failed", zap.String("container", id), zap.Error(err))
		}
	}()

	if err := s.docker.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("start helper container: %w", err)
	}
	for {
		inspect, err := s.docker.InspectContainer(ctx, id)
		if err != nil {
			return fmt.Errorf("inspect helper container: %w", err)
		}
		if !inspect.State.Running && inspect.State.Status != "created" {
			if inspect.State.ExitCode != 0 {
				return fmt.Errorf("helper exited with code %d", inspect.State.ExitCode)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("helper container timed out: %w", ctx.Err())
		case <-s.clk.After(50 * time.Millisecond):
		}
	}
}

// buildSubdirScript renders the mkdir/chown/chmod script for a CreationInfo.
// The subpath is shell-quoted, so any path AWS accepts is safe to embed.
func buildSubdirScript(subpath string, ci *CreationInfo) string {
	dir := "/v/" + subpath
	script := "mkdir -p " + shQuote(dir)
	if ci.OwnerUid != nil && ci.OwnerGid != nil {
		script += fmt.Sprintf(" && chown %d:%d %s", *ci.OwnerUid, *ci.OwnerGid, shQuote(dir))
	}
	if ci.Permissions != "" {
		script += fmt.Sprintf(" && chmod %s %s", shQuote(ci.Permissions), shQuote(dir))
	}
	return script
}

// shQuote single-quotes a string for POSIX sh, escaping embedded quotes.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
