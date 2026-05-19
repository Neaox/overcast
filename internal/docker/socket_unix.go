//go:build !windows

package docker

import (
	"context"
	"net"
	"strings"
	"time"
)

// dialEndpoint returns a dial function and host string for the given endpoint.
func dialEndpoint(endpoint string) (dialContextFn func(ctx context.Context, _, _ string) (net.Conn, error), host string) {
	if strings.HasPrefix(endpoint, "tcp://") {
		return nil, "http://" + strings.TrimPrefix(endpoint, "tcp://")
	}
	// Unix socket path
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", endpoint)
	}, "http://docker"
}
