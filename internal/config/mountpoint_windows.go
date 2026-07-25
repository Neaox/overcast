//go:build windows

package config

// isMountpoint always reports false on native Windows. Reliably comparing
// device identity for arbitrary paths on Windows requires additional Win32
// APIs (GetVolumePathNameW / GetVolumeNameForVolumeMountPointW) that aren't
// worth the complexity here: this binary's Docker image — where the
// OVERCAST_STATE=auto mountpoint signal actually matters — always runs
// Linux, where mountpoint_unix.go's syscall.Stat_t comparison is fully
// reliable. Native Windows runs fall back to the other two auto signals
// (explicit OVERCAST_DATA_DIR, an existing overcast.db) — see
// resolveAutoState.
func isMountpoint(path string) bool {
	return false
}
