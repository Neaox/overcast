//go:build windows

package config

// defaultDockerSocket is the Docker Desktop named pipe on Windows.
const defaultDockerSocket = `npipe:////./pipe/docker_engine`
