//go:build !windows

package config

// defaultDockerSocket is the standard Unix socket used by Docker on Linux and
// macOS (Docker Desktop symlinks its socket here).
const defaultDockerSocket = "/var/run/docker.sock"

// DefaultDockerSocket returns the endpoint Docker listens on for this platform.
// Exported because the test harness has to name the same one: a hardcoded
// "/var/run/docker.sock" is unreachable on Windows, so every Docker-dependent
// test built its server with a socket that could not answer and skipped itself
// on the platform, quietly, whatever the daemon was doing.
func DefaultDockerSocket() string { return defaultDockerSocket }
