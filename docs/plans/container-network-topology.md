# Container network topology — what should be able to reach what

> Status: **phases 1–4 implemented** (§10). The two planes exist, every
> container-backed service is on the shared `internal/dataplane` helper, #872 is
> closed, and each region seeds a default VPC whose backing network *is* the
> default data plane. Phases 5–6 (resolver guard, enforcement) are not started,
> so **nothing is restricted yet** — every container still reaches every other,
> which is the intended state until enforcement lands.
>
> Three deviations from the plan as written, all recorded in place:
>
> 1. The control plane is created as an ordinary bridge rather than `--internal`
>    (§5) — the flag is only observable once enforcement exists, so it moves to
>    phase 6.
> 2. **A VPC-placed resource keeps the default plane as well as its VPC network**
>    (`dataplane.DataNetworks`). The "exactly one data plane" rule of §5 is the
>    target, not the current state: withdrawing the default plane now would
>    restrict reachability while the resolver guard that turns a forbidden
>    connection into a named error is still phase 5, so the failure would be the
>    hang of §2. Deleting the second entry in `DataNetworks` is the enforcement
>    change.
> 3. ElastiCache replication groups land on the default plane because the record
>    carries no `CacheSubnetGroupName` (§10, phase 6 prerequisite).
> Scope: `internal/config/config.go`, `internal/router/router.go`,
> `internal/docker/probe.go`, `internal/containerendpoint/`, `internal/dns/`,
> `internal/services/{lambda,ecs,rds,elasticache,msk,efs,eks,ec2}/`,
> `docs/dev/container-networking.md`, `docs/networking.md`, `docs/README.md`.
> Related: [#872](https://github.com/Neaox/overcast/issues/872) (ElastiCache is
> unreachable from ECS — the instance of this that prompted the question),
> [container-networking.md](../dev/container-networking.md) (the mechanism),
> [ec2.md § VPC networking strategies](../services/ec2.md) (per-VPC networks).
>
> Note: `internal/services/ec2/vpc_strategy.go:25` and `docs/services/ec2.md:174`
> both cite `docs/plans/ec2-vpc-network-strategies.md`. That file exists on no
> branch, local or remote — the link is dangling, not unmerged.

## 1. The question

Two of them, and the second only makes sense once the first has an answer:

1. With no VPC configured anywhere, should everything Overcast starts be able to
   reach everything else Overcast starts?
2. Once a resource *does* declare a VPC, what should reach what?

Today neither has a stated answer. What exists is seven Docker networks named
after **the emulator service that created the container** — `overcast_lambda`,
`overcast_ecs`, `overcast_rds`, `overcast_elasticache`, `overcast_msk`,
`overcast_eks`, `overcast_efs` — plus one per VPC, and a per-service pile of
hand-written code that re-attaches containers across that partition to undo it.
Whether any given pair can talk is decided by how completely each service
remembered to undo it, which is why #872 exists.

## 2. What the tree actually does today

Every container-backed service, its home network, and what it attaches to
beyond it. "Aliases" means the container answers to its endpoint *name* there,
not merely by IP.

| Service | Home network | Also attaches to | Aliases | Per-caller endpoint |
| --- | --- | --- | --- | --- |
| Lambda | `overcast_lambda` (`container_runtime.go:240`) | its VPC network, no aliases (`container_runtime.go:193`) | n/a — nothing resolves a function by name | n/a |
| ECS | `overcast_ecs` (`handler_tasks.go:383`) | its VPC network, no aliases (`handler_awsvpc.go:147`) | n/a — `serviceRegistries` is stored, never served | n/a |
| RDS | `overcast_rds` | `overcast_lambda`, `overcast_ecs` (`handler.go:852`), its VPC network (`handler.go:797`) | **every base**, plus whatever the record advertises (`endpoint.go:169`) | yes (`endpoint.go:88`) |
| ElastiCache | `overcast_elasticache` | `overcast_lambda` **only** (`handler.go:920`) | **one**, from `externalHostname()` (`handler.go:884`) | no — stores a raw IP or `127.0.0.1` (`handler.go:666`) |
| MSK | `overcast_msk` | nothing | none | no — `ExternalHostname():hostPort`, duplicated in `handler.go:228` and `typed_logic.go:190` |
| EFS | `overcast_efs` | nothing | none | no — a container IP on `overcast_efs`, or `127.0.0.1` |
| EKS | `overcast_eks` | nothing | none | no — published `6443` |
| EC2 | — (owns per-VPC networks) | Overcast joins every VPC network (`handler_vpc_docker.go:52`) | n/a | n/a |

Read down the last three columns and the pattern is unmistakable: **RDS is
correct, and every other service is somewhere on the road to it.** RDS is
correct because this class of bug was hit there first, was expensive, and was
fixed properly. Nothing propagated the fix outward, because there is nothing to
propagate it *through* — each service open-codes its own answer.

The failure mode is always the same and it is always a hang, for the reason
recorded at `internal/services/rds/endpoint.go:158`: these names are subdomains
of the split-horizon domains, which Overcast's own resolver is authoritative
for. A missing Docker alias is not `NXDOMAIN`. The query falls through to
Overcast, which answers with **Overcast's** address, and the client opens a
connection to the emulator on 6379 or 9092 and waits for a protocol nobody
there speaks. Every one of these gaps costs someone an afternoon.

Two more consequences of the partition worth naming:

- **`internal/dns.Locator` exists solely because of it.** Its own doc comment
  says so: "Overcast is attached to more than one Docker network by default —
  `overcast_lambda` and `overcast_ecs` — and therefore has more than one
  address."
- **Five networks are created before a single resource exists.** `router.go:713`
  registers them and `docker/probe.go:53` creates each one eagerly at startup.
  Docker's default address pool is finite (`172.17.0.0/12` in /16s, then
  `192.168.0.0/16` in /20s); an emulator that burns five of them idle is a bad
  neighbour to the user's own compose stacks.

## 3. What AWS does, which is the thing we are meant to be replicating

There is no AWS concept that a service-shaped network partition corresponds to.
The real model has two halves, and the second is a **restriction**:

**No VPC named.** Every account has a default VPC per region, with a default
subnet per AZ and a default security group that permits all traffic between its
own members. RDS, ElastiCache, MSK, EFS and ECS tasks created without an
explicit VPC all land there, in that group, and can all reach each other.

**A VPC named.** Placement *subtracts*. A Lambda with a `VpcConfig` gives up the
internet access it had and reaches only what its ENIs can route to. A Fargate
task with an `awsvpc` configuration is the same. Neither can reach another VPC
without peering, a transit gateway or PrivateLink; neither reaches the AWS APIs
at all without a NAT gateway or a VPC endpoint. RDS is reachable from outside
its VPC only with `PubliclyAccessible=true`; ElastiCache never is.

So AWS's answer to question 1 is **yes, everything reaches everything**, and to
question 2 it is **only what the VPC routes to** — and declaring a VPC is an
explicit act. Somebody who wrote `vpc:` meant it. An emulator that quietly
ignores the declaration is not being helpful; it is withholding the single most
common AWS networking mistake there is until the code reaches an environment
where the failure is a five-minute timeout with no explanation attached.

## 4. Why the current shape cannot express that

The blocker is that the compute networks carry two unrelated things at once,
and there is no way to have one without the other:

| Carried on `overcast_lambda` / `overcast_ecs` | AWS's equivalent | Must survive VPC placement? |
| --- | --- | --- |
| The Lambda Runtime API; `AWS_ENDPOINT_URL` back into the emulator | link-local `169.254.100.1`, and VPC endpoints / NAT | **yes, always** |
| Reaching an RDS instance, a cache node, a broker | VPC routing, subject to security groups | **no — this is what a VPC restricts** |

The first is not networking at all. The Runtime API is *inside the execution
sandbox* on AWS — always present, regardless of VPC, because it is the
mechanism by which a function runs. Sever it and the container never leaves
INIT.

Because both ride the same Docker network, "keep the Runtime API" has been
inseparable from "keep reaching every database", and every service has resolved
it the only way available: attach to everything. The union is not a decision
anyone made. It is a consequence of the fusion.

Split them and the choice becomes available.

## 5. The rule

> **Three kinds of plane. A container joins the control plane, plus exactly one
> data plane — its VPC's if it declared one, the default one otherwise. Its
> reachability by name is exactly its data plane.**

| Plane | Members | `--internal` | Endpoint aliases registered | AWS analogue |
| --- | --- | --- | --- | --- |
| **Control** — `overcast_control` | Overcast, and every container it starts | see below | **never** — Overcast is the only thing on it worth reaching | the link-local Runtime API, plus an interface endpoint for every service |
| **Default data** — `overcast` | every resource that named no VPC | no | yes | the default VPC and its default security group |
| **VPC data** — `overcast-vpc-*` | the resources placed in that VPC | unless an IGW is attached | yes | that VPC |

Note what each column buys:

- **One data plane per container, not a union.** Simpler than what is there
  today and simpler than a permissive model — there is no set to compute, no
  list of "networks emulated compute runs on" to keep updated, and no way for a
  service to be half-attached.
- **`--internal` becomes load-bearing.** Today it is decorative: a VPC network
  without an IGW is `--internal`, but every container on it also sits on two
  non-internal compute networks, so it has internet anyway. With the control
  plane internal and carrying only Overcast, a container in an IGW-less VPC
  genuinely has no route out — which is what a private subnet without a NAT
  gateway is.
- **The resolver answer stops depending on the caller.** Every container reaches
  Overcast on one address, its control-plane address, whatever VPC it is in.
  `dns.Locator`'s multi-address case shrinks to the host-vs-container split it
  should have been.

**The control plane ships as an ordinary bridge, not `--internal`.** The flag
changes nothing observable until VPC-placed containers stop joining the default
plane — until then every container has egress through that plane regardless —
so it lands with enforcement in phase 6 rather than costing phase 1 a
create-inspect-recreate dance against the Runtime API's bind path. When it does
land, **whether it can be `--internal` is decided by the same probe that already
picks the Runtime API bind address**
(`containerendpoint.ResolveListen`, the three-row table in
[container-networking.md](../dev/container-networking.md) § 1a):

| Overcast is | Containers dial | Control plane `--internal`? |
| --- | --- | --- |
| in a container | Overcast's address on the control plane — on-link | **yes** |
| on a native Linux host | the control network's gateway — on-link | **yes** (an internal bridge still has its gateway; only routing *beyond* it is cut) |
| on a Docker Desktop host | the host's routable address — **not** on-link | **no** — `--internal` severs exactly that path, and every invocation would hang at INIT |

So `--internal` is a consequence of the existing bind-mode probe, not a new
decision. On the Desktop-host row the isolation story for IGW-less VPCs is
weaker (the control plane is a route out); that is a property of Desktop's
proxied networking, and the doc row above is where it gets said.

### 5a. The one deliberate divergence

**Overcast's own APIs are reachable from every container, VPC or not.** On AWS a
VPC Lambda calling DynamoDB needs a gateway endpoint or a NAT; here it always
works. Stated as a model rather than an omission: *Overcast behaves as if every
VPC has an interface endpoint for every service.* The alternative is that every
VPC-placed function fails at INIT — the Runtime API rides the same channel —
and no user could reasonably diagnose that. This one is not negotiable; the rest
of the restrictions are real.

### 5b. The escape hatches are AWS's own fields

Nothing new for a user to learn, and each one is a thing they would also have to
set on AWS:

| To reach across | Set | Status in the tree |
| --- | --- | --- |
| into a VPC from outside, for RDS | `PubliclyAccessible=true` → also join the default plane | **not stored at all** — would need adding |
| out of a VPC task to the internet | `assignPublicIp: ENABLED` | stored (`ecs/store.go:234`), unused |
| VPC A ↔ VPC B | a peering connection → attach both sides | metadata-only today, and becomes implementable *because* isolation is now real |
| a VPC to the internet | attach an internet gateway | implemented (toggles `--internal`) |

That last column is the honest summary of the work: enforcement makes three
currently-decorative AWS features mean something.

## 6. Collapse the per-service networks

`LAMBDA_NETWORK`, `ECS_NETWORK`, `RDS_NETWORK`, `ELASTICACHE_NETWORK`,
`MSK_NETWORK`, `EKS_NETWORK`, `EFS_NETWORK` all disappear, replaced by
`OVERCAST_NETWORK` (default `overcast`, the default data plane) and its control
counterpart.

The partition they create is by *which emulator package called Docker*. That is
an implementation detail of Overcast, exposed as network topology, with no AWS
analogue and no user-facing meaning. Everything downstream of it is
compensation: `connectToComputeNetworks`, `connectToLambdaNetwork`, the
"networks emulated compute runs on" list that every new container service must
remember to update and half of them didn't, and a multi-address `Locator`.

Steelmanning the current shape, the arguments for keeping it are grouping for
`docker network inspect` and blast-radius on cleanup. Both are already served
better by the labels every managed container carries —
`docker ps --filter label=overcast.service=rds`. Neither is worth a bug class.

Alpha is the time, and the variables are simply **dropped** — no error, no
warning, no alias. Decision recorded 2026-08-11: every current Overcast user is
known, none sets an explicit network, so compatibility shims would be dead code
on arrival. The config fields, their `envOr` lines, and the docs/README rows go
in the same commit as the code that read them.

Two operational notes that belong to the same change:

- **Stale networks from earlier versions.** An upgraded installation still has
  `overcast_lambda` et al. lying around, possibly with reused containers
  attached. Startup reconcile attaches adopted containers to the new planes
  (the attacher is idempotent, so this is the normal path, not a special case)
  and removes any empty `overcast_`-prefixed network it recognises. Anything
  non-empty is left alone and logged.
- **One Docker daemon is assumed, and now stated.** The per-service
  `*_DOCKER_SOCKET` variables remain, but cross-network attachment has only
  ever worked when they point at the same daemon — RDS already attaches to
  Lambda's network through its own client today. A single shared plane makes
  that assumption structural, so `docs/README.md` says it where the socket
  variables are documented.

## 7. The shared helper — where the DRY win is

Collapsing the networks removes half the duplication. The other half is that
every container-backed resource answers the same three questions and each
service answers them from scratch:

1. **What names can a caller be handed for me?** `ResourceHostnames(cfg)` ×
   a per-resource name template. Bounded, four or five entries.
2. **Which planes do I join?** Control, plus one data plane. Now a lookup, not
   a per-service list.
3. **What does *this* caller dial?** The sibling-container/host split — engine
   port on the plane, published port on the host — that `rds/endpoint.go`
   already gets right and nobody else attempts.

Only (1) is per-service. The shape:

```go
// package containerendpoint (or internal/dataplane)

// Resource is one container-backed endpoint: what it is called, where it runs.
type Resource struct {
    ContainerID string
    VpcID       string   // "" — the default data plane
    Public      bool     // PubliclyAccessible / assignPublicIp: also the default plane
    Names       []string // every base, from Hostnames below
    Advertised  string   // what the stored record already says, if anything
    Port        int      // the engine's own port, inside a plane
    HostPort    int      // the published port, on the host
}

// Hostnames applies name to every base a resource can be minted under.
func Hostnames(cfg *config.Config, name func(base string) string) []string

// Attach joins r to the control plane and its one data plane, carrying r's
// names as aliases on the latter. Idempotent; safe to repeat on reconcile.
func (a *Attacher) Attach(ctx context.Context, r Resource) error

// DialFor returns the address and port this caller should be handed.
func DialFor(ctx context.Context, r Resource) (host string, port int)
```

RDS's `connectToComputeNetworks` + `instanceEndpointAliases` +
`instanceEndpointFor` (≈60 lines it got right) become three calls. ElastiCache
gets all of it, including the per-caller minting it has never had. MSK gets a
resolvable hostname instead of `ExternalHostname():hostPort`, which today
resolves to Overcast from inside any container and fails on a port nothing
serves. The next container-backed service cannot get it wrong, because there is
nothing left to get wrong.

## 8. The resolver guard is now mandatory, not optional

Enforcement without a diagnostic is the worst of both worlds: the connection a
VPC forbids is exactly the one that hangs, per §2, and the user's conclusion
would be "Overcast is broken" rather than "this would not work on AWS either".

So `internal/dns` learns the data-plane grammars (`*.rds.{base}`, `*.cfg.{base}`,
…) and, for a name it recognises as a data-plane endpoint that the caller's own
plane does not carry, refuses instead of answering with Overcast's address —
with a log line naming both sides:

```
elasticache: cache-1.us-east-1.cfg.localhost.overcast.sh is in vpc-0abc; the
caller (ecs task arn:…:task/app/9f2) is in vpc-0def. On AWS this connection
would time out. Peer the VPCs, or place both in one.
```

That is strictly better than AWS, which gives you the timeout and nothing else,
and it is the thing that makes enforcement worth having rather than merely
faithful. It is also what converts every *future* instance of this bug class
from an afternoon into a log line.

## 9. What this plan does not yet enforce

Docker network membership is a coarse instrument: it expresses "in this VPC or
not", and nothing finer. Three things stay unenforced when §10 lands. None of
them is permanent — see §11 — but each is a tier of machinery beyond the one
above it, and the ordering matters more than the ambition.

- **Security groups and NACLs.** Port- and source-level rules between containers
  on one bridge. Unchanged by this plan.
- **Subnets within a VPC.** One flat network per VPC; no public/private
  distinction, no per-subnet routing, so a resource in a "private" subnet has
  whatever internet its VPC has.
- **Same-CIDR VPCs share a network under the default `shared` strategy**, so two
  VPCs that collide are not isolated from each other. Enforcement is per Docker
  network, so it is exactly as strong as the strategy in use — `strict` and
  `remapped` give real separation, `shared` does not. Say which you are getting.

## 10. Work, in order

**Phase 1 — the two default planes.** `OVERCAST_NETWORK` plus the control plane
replace the seven; the supervisor creates both; the removed variables are
dropped outright (§6), and stale `overcast_`-prefixed networks are cleaned up
per the same section. Retarget the Runtime API bind-host resolution
(`containerendpoint.ResolveListen`) and the `--dns` / `ExtraHosts` target at the
control plane, with `--internal` decided by the §5 probe table. Verify the
gateway-binding path still works on a `--internal` network on a native Linux
daemon — an internal bridge has a gateway, but this is the one mechanism in the
plan that depends on behaviour worth confirming rather than assuming.

**Phase 2 — the shared helper.** §7. RDS moves onto it with no behaviour change
(it is the model), which is what proves the helper before anything depends on it.

**Phase 3 — the services that are behind.** ElastiCache (clusters, replication
groups, serverless — all three paths), MSK, EFS exports, EKS. Each is then a
name template and a call. **This closes #872 as a special case of the general
fix rather than as a patch to one handler.**

**Phase 4 — make "no VPC" literally the default VPC.** AWS seeds one per
region; Overcast seeds none, so `DescribeVpcs --filters Name=isDefault` and
CDK's `Vpc.fromLookup(isDefault: true)` find nothing. Seed a default VPC whose
`DockerNetworkID` *is* the default data plane, and §5's two data-plane rows
collapse into one rule with no special case.

This runs **before** the guard and enforcement on purpose (decision recorded
2026-08-11): both of those answer "is the caller's plane the target's plane?",
and with the default VPC in place that is one `VpcID` comparison. Ordered the
other way, both get written against a `""`-means-default special case that this
phase then deletes — the same logic written twice.

What seeding means, concretely:

- Per region, lazily on first EC2 use of that region (matching how regional
  stores materialise): a VPC with `IsDefault: true`, a default subnet per
  advertised AZ, an attached IGW, a main route table with the IGW route, and a
  default security group. That is the set `Vpc.fromLookup` and CDK's subnet
  classification actually read.
- CIDR: try AWS's own `172.31.0.0/16` when creating the default plane; on
  collision with an existing Docker network, fall back to Docker's allocation
  and let the VPC record report the subnet the network really has. The record
  never lies about the network backing it.
- Every regional default VPC maps to the **same** Docker network — regions are
  not network-isolated in Overcast, and `shared` status already expresses
  several VPCs on one network.
- Guard rails, and why this phase is more than a seed call: `DeleteVpc` on a
  default VPC deletes the record but never the backing network (AWS allows the
  delete; `CreateDefaultVpc` is the way back). EC2's reconcile must neither
  adopt the default plane as an orphan nor remove it — it is supervisor-owned
  and carries no `overcast.service=ec2` label, and reconcile keys off those
  labels. `SetInternal` (IGW detach) must refuse on the default plane rather
  than recreate the emulator's primary network out from under every attached
  container.

**Phase 5 — the resolver guard.** §8. Lands with, or before, enforcement.

**Phase 6 — enforcement.** Only after everything above is in and the guard is
proving itself: stop attaching VPC-placed resources to the default plane. This
is the behaviour change; everything before it is structure. Add
`PubliclyAccessible` to RDS and honour `assignPublicIp` in the same phase, so
the escape hatches exist the day the restriction does.

**Current session scope (decision 2026-08-11): phases 1–4.** Everything through
the default VPC, nothing that restricts: the goal of the first phases is that
every pair that cannot talk today — ECS → ElastiCache (#872), anything → MSK,
EFS, EKS — can, and that the next service cannot reintroduce the gap.

Two adjacent wins this unlocks, neither in scope: **VPC peering becomes
implementable** (attach both sides — meaningful only once something is denied),
and ECS `serviceRegistries` / Cloud Map service discovery becomes a name
template on the task container.

## 11. Closing the §9 gaps — later phases

Each of the three has a route. They are ordered by **what they cost the user**,
not by fidelity, because the cheap tier catches most of what the expensive tier
would and runs everywhere.

### 11a. Security groups — two tiers, and the portable one comes first

**Tier 1 — evaluate at resolve time. No privileges, works on Docker Desktop.**
Phase 5's resolver guard already sees "who is asking, for what". It knows the
caller's container, hence its security groups, and the target's. So it can
answer the question the guard already asks — *is this connection permitted?* —
against the stored rules instead of only against plane membership, and refuse
with the same named message when no rule permits any port between the two.

This is per-name, not per-port, so it is an approximation. It is also the
approximation that matters: "I forgot the ingress rule" is the common security
group mistake by a wide margin, and it is currently invisible until AWS. Cost is
a store lookup on a DNS query that only happens when a Docker alias missed.

**Tier 2 — enforce in the packet path. Native Linux daemon only.**
Docker maintains a `DOCKER-USER` iptables chain, evaluated before its own rules
and never flushed by the daemon — the supported hook for exactly this. Per-VPC
default-drop between container addresses, plus an ALLOW per security group rule,
plus `ESTABLISHED,RELATED` gives real stateful security group semantics, and
stateless NACLs on top once §11b exists to give them a subnet to attach to.

The constraint is hard and worth stating before anyone starts: it needs
`CAP_NET_ADMIN` in the **daemon's** network namespace. On a native Linux daemon
Overcast can have that. On Docker Desktop the daemon is inside a VM Overcast
cannot reach, so tier 2 is unavailable on the primary development platform here
and would be a CI-and-Linux-only capability. That asymmetry is why tier 1 is
not merely a stepping stone — for most users it is the whole feature.

### 11b. Subnets — two planes per VPC, not one per subnet

The tempting shape is a Docker network per subnet. It is the wrong one: address
pool exhaustion is already a live concern (§2), and Docker networks do not route
to each other without a router container.

The shape that fits is the one the control plane already demonstrates — an
internal plane that grants reach but not egress:

| Plane | Members | `--internal` | Gives |
| --- | --- | --- | --- |
| `overcast-vpc-{id}` | every resource in the VPC | yes | the VPC's local route — intra-VPC reachability, no internet |
| `overcast-vpc-{id}-egress` | only resources in a subnet whose route table has `0.0.0.0/0 → igw` | no | the internet |

Two networks per VPC, bounded, no privileges, no router container. A resource in
a private subnet reaches its peers and not the internet, which is the
public/private distinction people actually test. It also makes `assignPublicIp`
and NAT gateways mean something, and gives NACLs a subnet identity to hang off
for §11a tier 2.

What it still does not give is per-subnet routing *between* subnets of one VPC —
they share the local plane, so they always reach each other. That matches AWS's
implicit local route, so the gap is narrower than it sounds.

### 11c. Same-CIDR VPCs — flip the default strategy

Today `shared` is a convenience trade-off: colliding VPCs get one network, and
the cost is isolation nobody was enforcing anyway. After §10 the cost changes
character — it is now a hole in something the emulator claims to do.

`remapped` already solves it: unique Docker subnets from `100.64.0.0/10`, the
user-requested CIDR preserved in API output, translation state persisted, and
ECS already handles the mode explicitly (`placement.remapped` skips ENI address
pinning). The phase is a default flip plus whatever the flip shakes out, not new
machinery.

The narrower alternative, if the flip proves disruptive: leave `shared` the
default but have it degrade to `remapped` on an actual collision, so the
convenient path stays convenient right up to the point where it would be wrong.
`netns` remains the endgame for real overlap with real isolation, and remains
unimplemented.

## 12. Tests

- A cache node is resolvable and connectable from an ECS task in the same VPC —
  the #872 acceptance, as an integration test rather than a unit assertion on
  which networks were touched.
- A resource is resolvable under **every** base in `ResourceHostnames`, not just
  the configured one. Table-driven over the base set; this is the assertion
  `rds/endpoint_test.go` already makes and the one nobody else makes.
- Both no-VPC resources reach each other on the default plane.
- A VPC-placed resource is **not** reachable from a no-VPC function, and the
  attempt produces the named refusal of §8 rather than a hang. Same for VPC A →
  VPC B.
- `PubliclyAccessible=true` restores reach from outside the VPC; `false` does not.
- Every container reaches Overcast's API and the Runtime API from inside a VPC,
  including one with no internet gateway (§5a).
- `DescribeVpcs --filters Name=isDefault,Values=true` returns the seeded VPC
  with subnets, an IGW and a route table CDK's `Vpc.fromLookup` can classify;
  `DeleteVpc` on it leaves the backing network intact.
- Startup with stale `overcast_lambda`-era networks present: adopted containers
  are re-attached to the new planes, empty old networks are removed, non-empty
  ones are left and logged.
