//go:build linux

package state

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// dataDirFsType returns the filesystem type hosting dir, by longest-prefix
// match against /proc/mounts, and a coarse classification the slow-probe
// messaging keys on:
//
//	"native"  — a real in-VM filesystem (ext4/xfs/btrfs/tmpfs/overlay):
//	            slowness here means host/VM I/O pressure, NOT a bind mount,
//	            and "switch to a named volume" is wrong advice.
//	"shared"  — a Docker Desktop file-sharing protocol (9p, virtiofs,
//	            grpcfuse, cifs): the classic bind-mount tax; named-volume
//	            advice applies.
//	"unknown" — couldn't determine; messaging stays hedged.
func dataDirFsType(dir string) (fsType, class string) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return "", "unknown"
	}
	defer f.Close()
	return classifyMounts(f, dir)
}

// classifyMounts is dataDirFsType's testable core: scan mounts (in
// /proc/mounts format) for the longest mount-point prefix of dir.
func classifyMounts(r io.Reader, dir string) (fsType, class string) {
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		dir = "/"
	}
	bestLen := -1
	best := ""
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		mp, typ := fields[1], fields[2]
		trimmed := strings.TrimSuffix(mp, "/")
		if trimmed == "" {
			trimmed = "/"
		}
		if dir == trimmed || strings.HasPrefix(dir+"/", trimmed+"/") || trimmed == "/" {
			if len(trimmed) > bestLen {
				bestLen = len(trimmed)
				best = typ
			}
		}
	}
	if best == "" {
		return "", "unknown"
	}
	switch best {
	case "ext4", "ext3", "xfs", "btrfs", "tmpfs", "overlay", "zfs", "f2fs":
		return best, "native"
	case "9p", "virtiofs", "fuse.grpcfuse", "grpcfuse", "cifs", "fakeowner", "drvfs", "fuse.osxfs", "osxfs":
		return best, "shared"
	default:
		return best, "unknown"
	}
}
