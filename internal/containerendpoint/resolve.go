package containerendpoint

// resolve.go derives the address that containers on a given Docker network use
// to reach Overcast.
//
// "host.docker.internal" is not a safe answer: Docker Desktop synthesises that
// name, but on native Linux it does not resolve at all unless the container was
// created with an explicit --add-host, so a container handed it cannot reach
// Overcast. Prefer a real IP — Overcast's own address on the shared network
// when it runs in a container, or a routable host interface when it does not —
// and fall back to the Docker Desktop name only when neither is available.

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
)

// dockerInternalHost is Docker Desktop's host alias, used only as a last resort.
const dockerInternalHost = "host.docker.internal"

// networkClient is the slice of *docker.Client that Resolve needs.
type networkClient interface {
	ConnectNetwork(ctx context.Context, networkID, containerID string) error
	InspectContainer(ctx context.Context, id string) (*docker.ContainerInspect, error)
}

// Resolve returns the origin ("http://host:port") that containers on network
// can use to reach Overcast's HTTP API. Never returns empty: callers use the
// result for AWS_ENDPOINT_URL, and a wrong-but-plausible address degrades
// better than an unset one.
func Resolve(ctx context.Context, dc networkClient, network string, port int, logger *zap.Logger) string {
	// Overcast in a container: join the container network and use our address
	// on it, which is directly routable from sibling containers.
	if dc != nil && network != "" && runningInContainer() {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if ip := networkIP(ctx, dc, network, hostname); ip != "" {
				logEndpoint(logger, "container mode", network, ip)
				return endpointURL(ip, port)
			}
		}
	}

	// Overcast on the host: a non-loopback host interface is reachable from
	// containers via the bridge, and unlike host.docker.internal it exists on
	// native Linux too.
	if ip := hostReachableIP(); ip != "" {
		logEndpoint(logger, "host mode", network, ip)
		return endpointURL(ip, port)
	}

	// Last resort — correct on Docker Desktop, unresolvable elsewhere.
	logEndpoint(logger, "fallback", network, dockerInternalHost)
	return endpointURL(dockerInternalHost, port)
}

// runningInContainer reports whether this process is itself containerised.
func runningInContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// networkIP attaches container to network and returns its IP there, or "" if
// the address cannot be determined. Connecting is idempotent, so this is safe
// to call when already attached.
func networkIP(ctx context.Context, dc networkClient, network, container string) string {
	_ = dc.ConnectNetwork(ctx, network, container)

	info, err := dc.InspectContainer(ctx, container)
	if err != nil || info == nil {
		return ""
	}
	if n, ok := info.NetworkSettings.Networks[network]; ok {
		return n.IPAddress
	}
	return ""
}

// hostReachableIP returns the first non-loopback IPv4 address on the host.
func hostReachableIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return ""
}

// endpointURL builds the origin containers use for AWS_ENDPOINT_URL.
func endpointURL(host string, port int) string {
	return fmt.Sprintf("http://%s:%d", host, port)
}

func logEndpoint(logger *zap.Logger, mode, network, host string) {
	if logger == nil {
		return
	}
	logger.Info("resolved container-reachable Overcast address",
		zap.String("mode", mode),
		zap.String("network", network),
		zap.String("host", host))
}
