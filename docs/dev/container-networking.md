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
| Where does **a customer's own domain** resolve? | `internal/dns` again, but from Route 53's store, not the split-horizon zone | A hosted zone's records answer authoritatively (A/AAAA/CNAME/MX/TXT/NS/SOA, wildcards, ALIAS-to-Overcast's-address); see [route53.md](../services/route53.md#dns-serving) |

## 0. Two planes, and one package that owns them

Every container Overcast starts sits on exactly two networks, and
`internal/dataplane` decides both. Nothing else should be reaching for a
network name.

| Plane | Name | Members | Carries |
| --- | --- | --- | --- |
| **Control** | `overcast_control` (`cfg.ControlNetwork()`) | Overcast, and every container it starts | The Lambda Runtime API, and the `AWS_ENDPOINT_URL` calls function and task code makes back into the emulator |
| **Data** | `overcast` (`OVERCAST_NETWORK`), and the resource's VPC network when it named one | one per container in the target model; see below | Traffic *between* resources — a task reaching a cache node, a function reaching a database |

The split exists because those two are not the same kind of thing. On AWS the
Runtime API lives inside the execution sandbox and is reachable whatever VPC a
function joins — it is the mechanism by which the function runs at all — and
Overcast's own API stands in for a VPC endpoint per service. Both must survive
VPC placement. Reaching a database must not. One network cannot express that,
and while they shared one, "keep the Runtime API" was inseparable from "keep
reaching every database".

The API is small on purpose:

| Call | Use |
| --- | --- |
| `dataplane.Primary(cfg)` | The network to create a container on (`HostConfig.NetworkMode`). Always the control plane — Docker accepts one network at create, and this is the one that must be up from the first packet |
| `dataplane.PrimaryEndpoints(cfg)` | The same as a `NetworkingConfig` |
| `dataplane.PlaceInVPC(ctx, resolver, vpcID)` | Resolve a VPC to a `Placement`. Refuses an unlaunchable VPC rather than silently falling back |
| `dataplane.Attach(ctx, dc, cfg, id, placement)` | Join the data plane(s), advertising the placement's aliases. Call it **after create, before start** |
| `dataplane.AttachAdopted(...)` | The same for a container reused after a restart — joins the control plane too, since one adopted from an earlier version was never created there |
| `dataplane.Hostnames(cfg, name, advertised...)` | The alias set: `name` applied to every base an endpoint could be minted under, plus what the record already advertises |
| `dataplane.ContainerAddr(ctx, dc, cfg, id)` | The address *Overcast itself* dials a managed container on, or `""` meaning "use the published port on loopback" |

**Historical note, because the shape of the old bug is instructive.** There used
to be one network per emulator service — `overcast_lambda`, `overcast_rds`,
`overcast_elasticache`, and four more — partitioned by *which package called
Docker*, which is not a boundary AWS has. Everything downstream was
compensation: RDS attached its containers to two compute networks, ElastiCache
to one, MSK to none. Whether a given pair could talk came down to how completely
each service remembered to undo the partition, and #872 is what that costs.

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
back over the control plane. So it binds loopback plus exactly one more address:

| Overcast is | Containers dial | Also bound |
| --- | --- | --- |
| in a container | our address on the control plane | loopback |
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
| in a container, container dials our address on the control plane | the container's bridge IP | yes |
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

The established shape, as every container-backed service now uses it:

```go
// aliases: every hostname the API could hand a caller for this resource.
func (h *Handler) instanceEndpointAliases(region string, inst *Instance) []string {
    return dataplane.Hostnames(h.cfg, func(base string) string {
        return instanceEndpointHostname(inst.ID, region, base)
    }, advertised...)
}

// Create on the control plane, then join the one data plane this resource
// belongs on — its VPC's network, or the default one.
placement, err := dataplane.PlaceInVPC(ctx, h.vpcResolver, inst.VpcID)
placement.Aliases = aliases
err = dataplane.Attach(ctx, h.docker, h.cfg, containerID, placement)
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

- **Attach after create, before start.** A container that starts before it has
  joined its data plane can race its own first outbound connection — function
  code that opens a database connection during INIT runs before the name it
  dials resolves. The one exception is ECS's `awsvpc` attachment, which reads
  back the address Docker assigned and therefore cannot run until the container
  does; the same task joins the default plane first, if it is entitled to one.
- **An ECS task is attached once, not once per container.** Every container in
  an `awsvpc` task shares one network namespace, held open by a container of its
  own (`internal/services/ecs/task_netns.go`) the way the ECS agent's
  `~internal~ecs~pause` does. That container is the one attached to the planes
  and the one carrying the task's ENI; the application containers are created
  with `NetworkMode: container:<id>` and inherit all of it, which is what makes
  `127.0.0.1` reach across a task as it does on AWS. Docker rejects a container
  in that mode that declares ports, resolvers, hosts entries or network
  endpoints of its own, so all four belong to the namespace container.
- **A VPC-placed resource gets its VPC network and nothing else.** That is the
  restriction, and `dataplane.DataNetworks` is where it lives. Two things widen
  it back: `Placement.Public`, set from AWS's own `PubliclyAccessible` and
  `assignPublicIp`; and a deployment where Overcast's resolver is not running,
  since the restriction is only safe where a forbidden connection fails by name
  rather than hanging.
- **So a service must pass a VPC when the resource has one.** Before enforcement
  a service that never called `PlaceInVPC` merely lost some fidelity; now it
  strands the resource on the default plane where its own consumers cannot see
  it. If you are adding a container-backed service, resolving its VPC is not
  optional.
- **A VPC attachment needs aliases too**, which is why they live on `Placement`
  rather than being passed only on the default path. Attaching without them
  leaves the container reachable by IP but unresolvable by name — so the caller
  falls through to Overcast's resolver and connects to a port nothing is
  listening on.
- **Overcast attaches itself** to the control plane, and for VPC work to the VPC
  networks, which is why the fallback answer is reachable at all.

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
