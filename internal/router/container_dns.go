package router

import (
	"context"
	"net"
	"net/netip"
	"strconv"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/containerendpoint"
	"github.com/Neaox/overcast/internal/dns"
)

// startContainerDNS binds the resolver that answers Overcast's split-horizon
// hostnames — and, crucially, every subdomain of them — for the containers
// Overcast starts. It returns a stop function, or nil when no resolver runs.
//
// Docker's --add-host writes an exact-match table, so /etc/hosts can shadow
// localhost.overcast.sh but never bucket.s3.localhost.overcast.sh. Those
// subdomain forms are what AWS SDKs build for virtual-hosted S3, API Gateway,
// AppSync and Lambda function URLs, and they resolve to 127.0.0.1 in public
// DNS — the container itself. See internal/dns.
//
// Binding happens here, synchronously and before any service is constructed,
// so cfg.DNSListening is settled before a container could be created: pointing
// a container at a resolver that is not listening would cost it every name it
// looks up, so the flag has to be true only when the socket is really open.
//
// No zone address is set. Overcast attaches to the control plane, the default
// data plane and any VPC networks it has work in, so it has an address on each;
// the server picks the one reachable from whichever container is asking.
//
// The returned server is handed back so the data-plane guard can be attached
// once Docker is available — see setDNSGuard. Binding cannot wait for that: the
// flag below must be settled before any container is pointed at the resolver.
func startContainerDNS(cfg *config.Config, logger *zap.Logger) (srv *dns.Server, stop func()) {
	if cfg == nil || !cfg.DNSEnabled {
		return nil, nil
	}
	zone := dns.NewZone(netip.Addr{}, containerendpoint.Hostnames(cfg)...)
	if !zone.Valid() {
		return nil, nil
	}

	// Without an upstream we must not become anybody's resolver: a container
	// pointed at a server that cannot forward loses every name it does not
	// own, which is far worse than the gap this closes.
	upstreams := dns.SystemResolvers(dns.DefaultResolvConf)
	if len(upstreams) == 0 {
		logger.Debug("dns resolver: no upstream resolvers available, not starting",
			zap.String("resolv_conf", dns.DefaultResolvConf))
		return nil, nil
	}
	forwarder := dns.NewUpstreamForwarder(upstreams...)

	srv = dns.NewServer(net.JoinHostPort("", strconv.Itoa(cfg.DNSPort)), zone, forwarder)
	if err := srv.Listen(); err != nil {
		// Not fatal. Port 53 needs privilege outside a container, and the
		// /etc/hosts entries still cover the exact hostnames.
		logger.Info("dns resolver not started; containers will resolve only the exact split-horizon hostnames",
			zap.Int("port", cfg.DNSPort), zap.Error(err))
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	cfg.DNSListening = true

	logger.Info("dns resolver listening",
		zap.String("addr", srv.UDPAddr()),
		zap.Strings("domains", zone.Domains()),
		zap.Strings("upstreams", forwarder.Servers()))

	return srv, func() {
		cancel()
		_ = srv.Close()
	}
}
