package containerendpoint

// listen.go answers resolve.go's question from the other side.
//
// Resolve says which address a container should dial. That is the whole answer
// for a server bound to every interface, which is what Overcast's own API is by
// default — whatever the container reaches us on, something is listening. A
// server that picks its own bind addresses needs the pair: the address
// containers dial *and* the local addresses that has to be listening on for the
// dial to arrive. The Lambda Runtime API is the case in point — nothing off this
// machine has any business reaching it, but Lambda containers connect back to it
// over the control plane, so loopback alone would strand every invocation.
//
// The two are resolved together, in one function, because a bind set derived
// separately from the address containers were handed is a silent misconfiguration
// the first time either changes.

import (
	"context"
	"net"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
)

// loopbackHost is always in a narrowed bind set: it costs nothing, it is
// reachable from nowhere else, and it is what a developer or a test on this
// machine reaches for.
const loopbackHost = "127.0.0.1"

// wildcardHost binds every interface — the pre-narrowing behaviour, kept as the
// last resort for a host whose reachable address could not be established.
const wildcardHost = "0.0.0.0"

// Listen is where a container-facing server should listen and what containers
// should be told to dial.
type Listen struct {
	// ContainerHost is the host containers use to reach the server. Never
	// empty: callers put it in an env var, and a wrong-but-plausible address
	// degrades better than an unset one.
	ContainerHost string

	// BindHosts are the local addresses to listen on, most important first —
	// the caller binds BindHosts[0] to settle a port of 0, so ContainerHost
	// leads whenever it is bindable here.
	BindHosts []string

	// Wildcard reports that BindHosts is the every-interface fallback rather
	// than a resolved set, so the caller can say so rather than looking narrow
	// when it is not.
	Wildcard bool
}

// listenClient is the slice of *docker.Client that ResolveListen needs.
type listenClient interface {
	networkClient
	InspectNetwork(ctx context.Context, nameOrID string) (*docker.NetworkInspect, error)
}

// ResolveListen returns the address containers on network dial to reach a
// server on this host, and the local addresses that server must bind for it to
// work. Falls back to the wildcard rather than guessing: a server nobody can
// reach fails worse than one bound too widely, and Wildcard says which it is.
func ResolveListen(ctx context.Context, dc listenClient, network string, logger *zap.Logger) Listen {
	return resolveListen(ctx, dc, network, logger, runningInContainer, hostReachableIP, bindableHost)
}

// resolveListen is ResolveListen with its environment probes injected, so mode
// selection is testable without a Docker daemon, a particular host interface
// layout, or the privilege to bind a given address.
func resolveListen(ctx context.Context, dc listenClient, network string, logger *zap.Logger,
	inContainer func() bool, hostIP func() string, bindable func(string) bool) Listen {
	containerised := inContainer()

	// Overcast in a container: our own address on the shared network, which is
	// directly routable from siblings on it and is what Resolve hands them.
	if containerised && dc != nil && network != "" {
		if hostname, err := os.Hostname(); err == nil && hostname != "" {
			nctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			ip := networkIP(nctx, dc, network, hostname)
			cancel()
			if ip != "" && bindable(ip) {
				return narrowedTo("container mode", ip, network, logger)
			}
		}
	}

	if !containerised {
		// The network's own gateway is this host's address on that bridge. It
		// beats a routable interface address on both counts that matter here:
		// it is reachable from this machine's containers and from nowhere else,
		// and it is on-link for every container attached to the network, so it
		// keeps working when a function joins a VPC network that takes over the
		// default route.
		if gateway := networkGateway(ctx, dc, network); gateway != "" && bindable(gateway) {
			return narrowedTo("gateway mode", gateway, network, logger)
		}

		// Docker Desktop lands here: its networks have a gateway too, but the
		// address belongs to the daemon's Linux VM and no listener on this
		// kernel can hold it, so the probe above declined it. The VM routes to
		// the host's own address instead, which is one interface rather than
		// every interface.
		if ip := hostIP(); ip != "" && bindable(ip) {
			return narrowedTo("host mode", ip, network, logger)
		}
	}

	// Neither our address on the network nor a bindable host address could be
	// established. Binding every interface is what Overcast did before it
	// narrowed anything, and it is still the only answer that cannot strand a
	// container; host.docker.internal matches what Resolve tells them, and is
	// backed by the "host-gateway" /etc/hosts entry Mapper.ExtraHosts adds.
	if logger != nil {
		logger.Warn("no container-reachable address resolved — binding every interface",
			zap.String("network", network),
			zap.String("container_host", dockerInternalHost))
	}
	return Listen{
		ContainerHost: dockerInternalHost,
		BindHosts:     []string{wildcardHost},
		Wildcard:      true,
	}
}

// narrowedTo builds the answer for a resolved host: containers dial it, and it
// is bound alongside loopback.
func narrowedTo(mode, host, network string, logger *zap.Logger) Listen {
	if logger != nil {
		logger.Info("resolved container-reachable listen address",
			zap.String("mode", mode),
			zap.String("network", network),
			zap.String("host", host))
	}
	return Listen{ContainerHost: host, BindHosts: []string{host, loopbackHost}}
}

// networkGateway returns the IPv4 gateway of a Docker network, or "" when the
// network has none, cannot be inspected, or names an address no container could
// usefully dial. A network created without IPAM configuration (the "host" and
// "none" networks among them) has no gateway, which is not an error — it means
// this mode does not apply.
func networkGateway(ctx context.Context, dc listenClient, network string) string {
	if dc == nil || network == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	info, err := dc.InspectNetwork(ctx, network)
	if err != nil || info == nil {
		return ""
	}
	for _, pool := range info.IPAM.Config {
		if ip := net.ParseIP(pool.Gateway); ip != nil && usableHostIP(ip) {
			return pool.Gateway
		}
	}
	return ""
}

// bindableHost reports whether this kernel can listen on host. It is the
// evidence the gateway mode turns on, rather than a guess from runtime.GOOS:
// "is this Docker Desktop" and "is this a native Linux daemon" are the same
// question asked less directly, and getting it wrong by inspection leaves
// Lambda unable to answer an invocation.
//
// Port 0 asks for any free port, so this proves the address is local without
// depending on the port the caller is about to want — which is settled, and
// reported honestly, by the real listen.
func bindableHost(host string) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
