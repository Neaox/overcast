//go:build windows

package config

// defaultDockerSocket is the Docker Desktop named pipe on Windows.
const defaultDockerSocket = `npipe:////./pipe/docker_engine`

// DefaultDockerSocket returns the endpoint Docker listens on for this platform.
// See the !windows build of this file for why it is exported.
func DefaultDockerSocket() string { return defaultDockerSocket }
