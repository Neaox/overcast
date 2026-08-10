package rds

import (
	"context"
	"net"
	"net/url"

	"github.com/Neaox/overcast/internal/dataplane"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Endpoint addresses, and why they are minted per caller.
//
// An RDS endpoint is a *data-plane* name: it points at the engine container
// Overcast started, not at Overcast. AWS's shape is
// `{id}.{hash}.{region}.rds.amazonaws.com`; Overcast mints
// `{id}.{region}.rds.{base}` — the same grammar with the account-specific hash
// dropped, on the base the caller reached Overcast on. That last part is the
// rule every other service already follows for the URLs it hands back
// (docs/plans/client-facing-url-minting.md, serviceutil.ClientBaseURL): a value
// Overcast returns must be dialable by whoever asked for it, and a caller who
// reached us on `localhost.overcast.sh` cannot resolve a name ending in a
// hostname they have never heard of.
//
// Reachability is Docker's, not ours: the engine container carries the endpoint
// name as a network alias on every network emulated compute runs on, so a
// Lambda function or ECS task resolving it gets the container's address from
// Docker's embedded resolver before the query ever reaches Overcast's. Because
// the name minted depends on the caller, the alias set covers every hostname
// Overcast could mint under — see instanceEndpointAliases and
// docs/dev/container-networking.md.

// instanceEndpointHostname builds the DNS name for a DB instance on base.
func instanceEndpointHostname(id, region, base string) string {
	return id + "." + region + ".rds." + base
}

// clusterEndpointHostname builds the DNS name for an Aurora cluster endpoint on
// base. role is AWS's endpoint discriminator — "cluster-rw" for the writer,
// "cluster-ro" for the reader.
func clusterEndpointHostname(id, role, region, base string) string {
	return id + "." + role + "." + region + ".rds." + base
}

// endpointBase returns the hostname new endpoint names should be minted under
// for this caller: the configured OVERCAST_HOSTNAME when set, else the host
// they reached Overcast on.
//
// An IP-literal origin falls back to the configured external hostname, because
// a name cannot be a subdomain of an address. Host-side callers are unaffected
// by that fallback — they are handed a loopback address anyway when the base
// carries no wildcard DNS (see instanceEndpointFor).
func (h *Handler) endpointBase(ctx context.Context) string {
	base := serviceutil.ClientBaseURLFromOrigin(h.cfg, middleware.ClientEndpointFromContext(ctx))
	if u, err := url.Parse(base); err == nil {
		if host := u.Hostname(); host != "" && net.ParseIP(host) == nil {
			return host
		}
	}
	return h.externalHostname()
}

// instanceEndpointFor returns the address and port this caller should dial for
// inst.
//
// Both halves are per-caller, for the same reason and with the same precedent
// as the API's own published-vs-listen port split (docs/networking.md § Which
// host and port a URL carries):
//
//   - A sibling container resolves the endpoint name to the engine container
//     through Docker's embedded resolver, and talks to it on the engine's own
//     port — 3306, exactly as on AWS.
//   - The host reaches the same container through its published port binding,
//     and only through that; the engine port is not bound on the host. It is
//     given a loopback address when the base carries no wildcard DNS, since
//     `*.localhost` does not resolve on Windows.
//
// An instance with no container behind it (Docker unavailable, or the engine
// image could not be resolved) keeps the AWS-shaped name and its configured
// port: there is nothing dialable to offer, and a name is a better answer than
// a loopback address that is certainly wrong.
func (h *Handler) instanceEndpointFor(ctx context.Context, inst *DBInstance) (string, int) {
	if inst == nil {
		return "", 0
	}
	base := h.endpointBase(ctx)
	name := instanceEndpointHostname(inst.DBInstanceIdentifier, h.instanceRegion(ctx), base)

	if serviceutil.CallerIsSiblingContainer(middleware.ClientAddrFromContext(ctx)) {
		if inst.DockerContainerID != "" {
			if ecfg, ok := engineEnvConfig[inst.Engine]; ok && ecfg.ContainerPort > 0 {
				return name, ecfg.ContainerPort
			}
		}
		return name, inst.Port
	}

	if inst.HostPort > 0 {
		if serviceutil.SupportsHostRouting("http://" + base) {
			return name, inst.HostPort
		}
		return "127.0.0.1", inst.HostPort
	}
	return name, inst.Port
}

// clusterEndpointsFor returns the writer and reader endpoint names for this
// caller. A cluster has no container of its own — its member instances do — so
// only the name is per-caller here; the port is the cluster's.
func (h *Handler) clusterEndpointsFor(ctx context.Context, c *DBCluster) (writer, reader string) {
	if c == nil {
		return "", ""
	}
	base := h.endpointBase(ctx)
	region := h.instanceRegion(ctx)
	return clusterEndpointHostname(c.DBClusterIdentifier, "cluster-rw", region, base),
		clusterEndpointHostname(c.DBClusterIdentifier, "cluster-ro", region, base)
}

// instanceRegion is the region to render names for: the caller's, which is the
// region the record was read from.
func (h *Handler) instanceRegion(ctx context.Context) string {
	if h.store != nil {
		if region := h.store.region(ctx); region != "" {
			return region
		}
	}
	return h.region()
}

// dialTarget is the address and port *Overcast itself* uses to reach the engine
// container — health checks, not responses. It is deliberately not the endpoint
// a client is given: Overcast may be on the host (where only the published port
// is bound) or in a container beside the engine (where only the engine port is).
func dialTarget(inst *DBInstance) (string, int) {
	if inst == nil {
		return "", 0
	}
	if inst.DialAddress != "" && inst.DialPort > 0 {
		return inst.DialAddress, inst.DialPort
	}
	if inst.Endpoint != nil {
		return inst.Endpoint.Address, inst.Endpoint.Port
	}
	return "", 0
}

// instanceEndpointAliases is the set of DNS names the engine container must
// answer to on the networks emulated compute runs on.
//
// It covers the endpoint name under every hostname Overcast could mint it
// under, not only the configured one, because the name a caller holds depends
// on the endpoint *they* used: a stack deployed through `localhost.overcast.sh`
// bakes that name into a task definition, while a Lambda calling
// DescribeDBInstances over the container endpoint is handed the same instance
// under whichever name its own request carried.
//
// Docker aliases are exact-match, so an unregistered name resolves nowhere —
// and under a split-horizon domain it is worse than nowhere: the query falls
// through to Overcast's own resolver, which answers any subdomain of those
// domains with Overcast's address, so the client connects to Overcast on 3306
// and hangs. See docs/dev/container-networking.md for which resolver answers
// what.
//
// The set is bounded (four or five names) and costs one entry each in Docker's
// per-network alias table.
func (h *Handler) instanceEndpointAliases(region string, inst *DBInstance) []string {
	if inst == nil || inst.DBInstanceIdentifier == "" {
		return nil
	}
	if region == "" {
		region = h.region()
	}
	// Whatever the record already advertises goes in too, in case it was
	// minted under a hostname the current configuration no longer lists.
	var advertised []string
	if inst.Endpoint != nil {
		advertised = append(advertised, inst.Endpoint.Address)
	}
	return dataplane.Hostnames(h.cfg, func(base string) string {
		return instanceEndpointHostname(inst.DBInstanceIdentifier, region, base)
	}, advertised...)
}
