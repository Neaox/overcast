# Container networking — how emulated compute reaches emulated services

Overcast starts real containers for several services. Two questions come up
every time a new one is added, and both already have an answer in the tree.
Reach for these before writing anything new: the last agent to need
container-to-container name resolution built a second DNS mechanism next to the
one that already worked.

**The one-line version.** Two resolvers answer inside a container Overcast
started, and they answer different questions:

| Question | Answered by | Mechanism |
| --- | --- | --- |
| Where is **Overcast**? | `internal/dns`, via `--dns` | Owns the split-horizon domains and every subdomain; answers with Overcast's own address, chosen per caller |
| Where is **that other container**? | Docker's embedded resolver, `127.0.0.11` | Network **aliases** on containers attached to the caller's network — consulted *before* anything is forwarded to `internal/dns` |

Getting these the wrong way round is the standing trap. Overcast's resolver is
authoritative for the domains an endpoint name ends in, so it will happily
answer for `my-db.us-east-1.rds.localhost.overcast.sh` — with **Overcast's**
address. A missing alias therefore does not surface as an unknown host; it
surfaces as a connection to Overcast on port 3306 that hangs.

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

### 1a. …and the server it is calling has to be listening there

`Resolve` is the whole answer only for a server bound to every interface, which
Overcast's own API is by default. A server that chooses its own bind addresses
needs the pair, and `containerendpoint.ResolveListen` returns it: the host
containers dial, and the local addresses that has to be listening on.

The Lambda Runtime API is the case that needs it. Nothing off this machine is a
legitimate caller — it is an unauthenticated control channel for every Lambda
container — but loopback alone strands every invocation, because the RIC dials
back over `LAMBDA_NETWORK`. So it binds loopback plus exactly one more address:

| Overcast is | Containers dial | Also bound |
| --- | --- | --- |
| in a container | our address on `LAMBDA_NETWORK` | loopback |
| on a native Linux host | that network's **gateway** — host-local, and on-link from every container attached, so it survives a function joining a VPC network that takes over the default route | loopback |
| on a Docker Desktop host | the host's routable address (Desktop's networks have a gateway too, but it belongs to the daemon's VM) | loopback |

Which of the last two applies is decided by **binding the gateway**, not by
`runtime.GOOS`: "native daemon or Desktop VM" is the same question asked less
directly, and `uname -s` says `Linux` under WSL2 either way. When nothing
resolves, it binds the wildcard and logs that it did — a Runtime API nobody can
reach fails worse than one bound too widely, and every invocation would hang at
INIT.

### 1b. …and a container's source address is not its identity

The Runtime API is an unauthenticated control channel shared by every Lambda
container, so it has to know *which* container is calling. It used to answer
that from `r.RemoteAddr`, matched against the bridge IP `InspectContainer`
reported at registration. **That only holds while the container's packets
arrive on-link**, which is the Docker Desktop row above and nowhere else:

| Overcast is | Listener sees | Matches the registered IP |
| --- | --- | --- |
| in a container, container dials our address on `LAMBDA_NETWORK` | the container's bridge IP | yes |
| on a native Linux host, container dials the bridge gateway | the container's bridge IP | yes |
| on a Docker Desktop host, container dials the host's address | the host's own address | **no** |

Desktop's userspace proxy re-originates the connection, so a natively-built
Overcast on Windows or macOS matched no container and failed every invocation at
INIT — reported to the user as their function's `Runtime.InitError`.

So identity comes from **the listener the request arrived on**, not from where
it came from. Each execution environment gets a listener of its own
(`RuntimeAPIServer.AddContainerListener`, bound at port 0 across the same
`BindHosts`), is told about only that port through `AWS_LAMBDA_RUNTIME_API`, and
gives it up in `containerInstance.Close`; the count is bounded by
`LAMBDA_MAX_INSTANCES`. The source-address lookup remains as the fallback, which
is what the containerised row above still uses.

Nothing else was available: the RIC builds its own requests, so no header or
token can be injected, and `AWS_LAMBDA_RUNTIME_API` is parsed as a bare
`host:port`, so there is no path prefix either. "Attribute the only initialising
container" is racy the moment two cold starts overlap, which the cold-start
semaphore explicitly permits.

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
| `containerendpoint.ResourceHostnames(cfg)` | Every base a resource name can be minted under — the split-horizon set plus `localhost`. Build one alias per entry |
| `docker.Client.ConnectNetworkWithAliases(ctx, network, container, aliases)` | Attaches an existing container to a network, advertising those aliases |
| `docker.Client.ConnectNetwork(ctx, network, container)` | The same with no aliases — reachable by IP, **not resolvable by name** |
| `NetworkingConfig.EndpointsConfig[net] = {Aliases: …}` | The same at container-create time |

The established shape, as RDS uses it
(`internal/services/rds/endpoint.go` — ElastiCache still registers only the
configured name):

```go
// aliases: every hostname the API could hand a caller for this resource.
func (h *Handler) instanceEndpointAliases(region string, inst *Instance) []string {
    bases := containerendpoint.ResourceHostnames(h.cfg)
    names := make([]string, 0, len(bases))
    for _, base := range bases {
        names = append(names, instanceEndpointHostname(inst.ID, region, base))
    }
    return docker.EndpointAliases(names...)
}

// Attach to the networks emulated compute runs on, so a Lambda function or an
// ECS task can resolve the endpoint name the API gave it.
for _, network := range []string{h.cfg.LambdaNetwork, h.cfg.ECSNetwork} { … }
```

Note the **set**, not the one name. Endpoint names are minted on the hostname
the caller reached Overcast on (`docs/networking.md` § Data-plane endpoints), so
the same instance is `db.us-east-1.rds.localhost.overcast.sh` to one caller and
`db.us-east-1.rds.localhost` to another. Docker aliases are exact-match: a name
that was not registered does not resolve — and under a split-horizon domain it
is worse than that, because the query then reaches Overcast's own resolver,
which answers *any* subdomain of those domains with Overcast's address. The
caller connects to Overcast on 3306 and hangs. Register every name you can mint.

Three more things worth knowing:

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
