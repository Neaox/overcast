//go:build !windows

package config

// defaultDockerSocket is the standard Unix socket used by Docker on Linux and
// macOS (Docker Desktop symlinks its socket here).
const defaultDockerSocket = "/var/run/docker.sock"
