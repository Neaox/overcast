# Container networking — how emulated compute reaches emulated services

Overcast starts real containers for several services. Two questions come up
every time a new one is added, and both already have an answer in the tree.
Reach for these before writing anything new: the last agent to need
container-to-container name resolution built a second DNS mechanism next to the
one that already worked.

## 1. A container calling Overcast

`internal/containerendpoint` — resolves an address the container can dial,
rewrites loopback URLs baked into env vars, supplies `/etc/hosts` entries for
the split-horizon hostnames, and hands over the CA bundle under TLS.

Container-facing DNS for those hostnames is `internal/dns`, started by
`internal/router/container_dns.go`. It is authoritative for
`localhost.overcast.sh` (and the LocalStack/floci aliases) and forwards
everything else upstream. It answers with **Overcast's own** address, chosen per
caller by `dns.Locator` because Overcast sits on several Docker networks.

**Do not add per-name overrides here.** This resolver answers "where is
Overcast". A name that belongs to some other container is question 2.

## 2. A container calling another container

This is Docker's embedded resolver (`127.0.0.11`), not Overcast's. Every
container on a user-defined network gets it, and it resolves **network aliases**
from the containers attached to that network *before* forwarding anything
upstream to Overcast. So the way to make `my-db.us-east-1.rds.localhost.overcast.sh`
reach the database is to attach the database container to the caller's network
carrying that name as an alias.

The utilities:

| Helper | Use |
| --- | --- |
| `docker.EndpointAliases(addrs...)` | Filters a set of endpoint addresses down to unique, non-IP hostnames usable as aliases |
| `docker.Client.ConnectNetworkWithAliases(ctx, network, container, aliases)` | Attaches an existing container to a network, advertising those aliases |
| `docker.Client.ConnectNetwork(ctx, network, container)` | The same with no aliases — reachable by IP, **not resolvable by name** |
| `NetworkingConfig.EndpointsConfig[net] = {Aliases: …}` | The same at container-create time |

The established shape, as ElastiCache and RDS use it:

```go
// aliases: the endpoint hostnames the API hands callers.
func (h *Handler) instanceEndpointAliases(inst *Instance) []string {
    return docker.EndpointAliases(inst.Endpoint.Address, canonicalHostname)
}

// Attach to the networks emulated compute runs on, so a Lambda function or an
// ECS task can resolve the endpoint name the API gave it.
for _, network := range []string{h.cfg.LambdaNetwork, h.cfg.ECSNetwork} { … }
```

Three things worth knowing:

- **Every consumer network needs its own attachment.** An alias on
  `overcast_lambda` does nothing for an ECS task; that was the bug behind a
  Fargate task being unable to reach its RDS instance.
- **A VPC attachment needs aliases too.** When a service places its container on
  a VPC's Docker network (`DockerNetworkForVpc`), attaching without aliases
  leaves it reachable by IP but unresolvable by name — so the caller falls
  through to Overcast's resolver and connects to a port nothing is listening on.
- **Overcast attaches itself** to `overcast_lambda`/`overcast_ecs` and, for VPC
  work, to the VPC networks, which is why the fallback answer is reachable
  at all.

## 3. VPC networks

`internal/services/ec2` owns one Docker network per VPC and exposes
`VpcIDForSubnet`, `VPCNetworkStatus`, `DockerNetworkForVpc` and
`AvailabilityZoneForSubnet` to other services (wired in
`internal/router/router.go`). A VPC whose network could not be created reports
status `unbacked`, and services refuse to place into it rather than starting
something unreachable.

The common cause of `unbacked` is a CIDR collision with a leftover
`overcast-vpc-*` network from an earlier run — Docker refuses the pool overlap.
`docker network ls | grep overcast-vpc-` and remove the stale ones.
