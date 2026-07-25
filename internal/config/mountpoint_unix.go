//go:build !windows

package config

import (
	"os"
	"path/filepath"
	"syscall"
)

// isMountpoint reports whether path is a filesystem mountpoint — i.e. its
// device differs from its immediate parent directory's device. This is the
// same test `mountpoint -q` performs, implemented directly so it works on
// minimal images (Alpine's busybox mountpoint is present, but this avoids
// shelling out and works identically on any Unix).
//
// Used by the OVERCAST_STATE=auto resolver (see resolveAutoState) to detect
// a Docker named volume or bind mount at OVERCAST_DATA_DIR: both present as
// a different device than the container's root filesystem, whereas an
// unmounted directory (created by `mkdir -p /data` in the image) shares its
// parent's device.
//
// Returns false (not a mountpoint) if either stat fails — e.g. the
// directory doesn't exist yet — since that is not evidence of a mount.
func isMountpoint(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}

	parentInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return false
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}

	return stat.Dev != parentStat.Dev
}
