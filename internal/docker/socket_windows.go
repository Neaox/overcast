//go:build windows

package docker

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

// dialEndpoint returns an http.Transport configured for the given endpoint.
// On Windows this handles the npipe:// scheme in addition to tcp://.
func dialEndpoint(endpoint string) (dialContextFn func(ctx context.Context, _, _ string) (net.Conn, error), host string) {
	if strings.HasPrefix(endpoint, "tcp://") {
		return nil, "http://" + strings.TrimPrefix(endpoint, "tcp://")
	}
	if strings.HasPrefix(endpoint, "npipe://") {
		// Convert npipe://... to a Windows UNC pipe path: \\.\pipe\docker_engine
		pipe := strings.TrimPrefix(endpoint, "npipe://")
		pipe = strings.ReplaceAll(pipe, "/", `\`)
		return func(ctx context.Context, _, _ string) (net.Conn, error) {
			return winio.DialPipeContext(ctx, pipe)
		}, "http://docker"
	}
	// Bare path — treat as Unix socket (useful in WSL or if a Unix-compatible
	// layer is present).
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", endpoint)
	}, "http://docker"
}
