//go:build !linux

package state

// dataDirFsType on non-Linux hosts: no /proc/mounts to consult. The slow
// probe's messaging stays hedged ("unknown"). The Docker-container case —
// the one where the bind-vs-volume distinction matters — is always linux.
func dataDirFsType(string) (fsType, class string) { return "", "unknown" }
