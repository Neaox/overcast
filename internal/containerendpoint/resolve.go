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
	"net/url"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
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
	return resolve(ctx, dc, network, port, logger, runningInContainer, hostReachableIP)
}

// ResolveHost is Resolve without the scheme and port, for callers that pair the
// host with a port of their own — Lambda reaches its Runtime API on one port
// and the emulator API on another, but both live at the same host.
func ResolveHost(ctx context.Context, dc networkClient, network string, logger *zap.Logger) string {
	u, err := url.Parse(Resolve(ctx, dc, network, 0, logger))
	if err != nil {
		return dockerInternalHost
	}
	return u.Hostname()
}

// resolve is Resolve with its environment probes injected, so mode selection is
// testable without a Docker daemon or a particular host interface layout.
func resolve(ctx context.Context, dc networkClient, network string, port int, logger *zap.Logger,
	inContainer func() bool, hostIP func() string) string {
	containerised := inContainer()

	// Overcast in a container: join the container network and use our address
	// on it, which is directly routable from sibling containers.
	if dc != nil && network != "" && containerised {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if ip := networkIP(ctx, dc, network, hostname); ip != "" {
				logEndpoint(logger, "container mode", network, ip)
				return endpointURL(ip, port)
			}
		}
	}

	// Overcast on the host: a routable host interface is reachable from
	// containers via the bridge, and unlike host.docker.internal it exists on
	// native Linux too.
	//
	// Deliberately skipped when Overcast is itself containerised: there our own
	// interface addresses belong to whatever networks we happen to be attached
	// to, and a sibling on the task network cannot necessarily route to them.
	// A confidently wrong IP is worse than the alias below, which ExtraHosts
	// backs with an /etc/hosts entry.
	if !containerised {
		if ip := hostIP(); ip != "" {
			logEndpoint(logger, "host mode", network, ip)
			return endpointURL(ip, port)
		}
	}

	// Last resort. Correct on Docker Desktop by itself, and made correct
	// everywhere else by the "host.docker.internal:host-gateway" entry that
	// Mapper.ExtraHosts derives from this endpoint.
	logEndpoint(logger, "fallback", network, dockerInternalHost)
	return endpointURL(dockerInternalHost, port)
}

// runningInContainer reports whether this process is itself containerised.
func runningInContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// RunningInContainer reports whether this process is itself containerised
// (Docker's /.dockerenv sentinel) — exported for callers outside the
// resolver, e.g. cmd/overcast's startup guidance.
func RunningInContainer() bool { return runningInContainer() }

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

// hostReachableIP returns a host IPv4 address that containers can dial.
//
// Preference goes to the address on the interface carrying the default route.
// Simply taking the first non-loopback address is not good enough: a developer
// machine routinely has many interfaces, net.InterfaceAddrs makes no ordering
// promise, and several of the usual candidates are dead ends — 169.254/16
// autoconfigured addresses left by a disconnected adapter, and Hyper-V or
// VirtualBox host-only switches that Docker's bridge cannot reach. Measured on
// a Windows Docker Desktop host, three of six non-loopback candidates were
// unreachable from a task container.
func hostReachableIP() string {
	if ip := defaultRouteIP(); ip != "" {
		return ip
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	return scanInterfaceIP(addrs)
}

// defaultRouteIP returns the local address the kernel would use to reach the
// wider network — i.e. the interface carrying the default route, which is the
// one container traffic arrives on. Dialling a UDP address sends no packets;
// it only asks the kernel to fix a local address, so this is a route lookup
// rather than a network call. The destination is TEST-NET-1 (RFC 5737), which
// exists to be referenced without implying a real host.
func defaultRouteIP() string {
	conn, err := net.Dial("udp4", "192.0.2.1:80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || !usableHostIP(addr.IP) {
		return ""
	}
	return addr.IP.String()
}

// scanInterfaceIP returns the first usable IPv4 address in addrs, used when
// there is no default route to learn from.
func scanInterfaceIP(addrs []net.Addr) string {
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if ok && usableHostIP(ipNet.IP) {
			return ipNet.IP.String()
		}
	}
	return ""
}

// usableHostIP reports whether ip is an address a sibling container could
// plausibly dial: IPv4, not our own loopback, and not link-local — 169.254/16
// means DHCP failed on that adapter, and nothing routes there.
func usableHostIP(ip net.IP) bool {
	return ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
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
