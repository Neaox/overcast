# Container networking egress — what a VPC should mean for a container

> **Status:** decided and shipped. `OVERCAST_VPC_EGRESS=open|routed|none`
> (default `open`), the Runtime-API reachability probe decoupled from the egress
> decision, and exact-state verification of every network Overcast reuses.
> `routed` is refused at startup pending [#1571](https://github.com/overcast-sh/overcast/issues/1571).
> **Scope:** `internal/config`, `internal/dataplane`, `internal/docker`
> (`netspec.go`, `probe.go`), `internal/services/ec2` (network ownership),
> `internal/router` (health, advisories), `cmd/overcast/cmd_network.go`,
> `docs/networking.md`.
> **Related:** [container network topology](./container-network-topology.md) —
> the two-plane model this builds on.
> **Audience:** any contributor or agent. Read [CONTRIBUTING.md](../../CONTRIBUTING.md)
> and [AGENTS.md](../../AGENTS.md) first; all their rules apply.

## 1. The question

Since 0.0.1-alpha.37 the control plane was created `--internal` whenever a
runtime probe said it was safe — Overcast containerised, or a native Linux
daemon. That closed a real leak: a container in a gateway-less VPC sits on both
its VPC network and the control plane, so if the control plane has egress the
VPC's `--internal` flag is decoration.

It also, on those hosts, broke the common case — a hybrid stack whose function
calls real AWS or a third-party API — with a failure that surfaces minutes later
inside application code as `ENETUNREACH`. And because the answer came from the
*host*, two engineers on one pinned image got different behaviour with nothing
anywhere saying which they had ([#1564](https://github.com/overcast-sh/overcast/issues/1564)).

One flag was answering two unrelated questions:

1. **May containers reach the outside?** A property of the deployment.
2. **Can containers still reach the Runtime API if this network is isolated?**
   A property of the host.

## 2. Evidence

### 2.1 Docker layer — measured on Docker Desktop 29.7.2 / Windows 11 / WSL2

Containers attached directly, `alpine:3`, probing `1.1.1.1:80` and
`sts.us-east-1.amazonaws.com:443`. All networks created fresh by the run.

| # | Control plane | Data / VPC network | Default route | External |
| --- | --- | --- | --- | --- |
| A | `--internal` | routable (VPC with IGW) | via VPC net | reached |
| B | `--internal` | `--internal` | **none** | ENETUNREACH |
| C | `--internal` | routable (default data plane) | via data plane | reached |
| D | `--internal` | *(none)* | **none** | ENETUNREACH |
| E | routable | `--internal` (isolated VPC) | **via control plane** | **reached** |
| F | `--internal` | `--internal` | **none** | ENETUNREACH |
| G | routable | routable | via control plane | reached |

**Row E is the finding.** A VPC-attached container whose VPC network is internal
takes its default route from the control plane. Docker's own documentation says
an internal network has no default route configured, so the routable network is
the only gateway candidate.

### 2.2 End-to-end — Lambda invocation matrix

CloudFormation-built VPCs, so the real `VPC → InternetGateway →
VPCGatewayAttachment → subnet → route table → route` ordering. Each cell ran
three probes: `GET https://checkip.amazonaws.com`, `GET` real
`sts.us-east-1.amazonaws.com` (a **403** proves packets left the machine), and
`dns.resolve4` of a public name.

With the control plane routable — what `auto` resolved to on this host, i.e. the
shipped default — **every** VPC shape had full egress under all three VPC
strategies:

| strategy | shape | checkip | real STS | DNS | VPC network |
| --- | --- | --- | --- | --- | --- |
| shared | novpc | 200 | 403 | ok | data plane, `Internal=false` |
| shared | pubigw | 200 | 403 | ok | `Internal=false` |
| shared | natgw | 200 | 403 | ok | `Internal=false` |
| shared | **isolated** | **200** | **403** | ok | **`Internal=true`** |
| strict, remapped | all four | 200 | 403 | ok | as above |

The isolated VPC's network was correctly `--internal` and isolated nothing.
Row E, confirmed end to end. **Egress was decided entirely by one bit on one
network, and route tables had no bearing on it at all.**

13 of 28 cells; the `internal=true` column was not run (on Docker Desktop it
strands invocations at INIT, before egress is reached) and ECS was not covered.

### 2.3 What other tools do

| Tool | VPC-attached function's egress |
| --- | --- |
| Real AWS | Isolated subnet: no. Private + NAT: yes. Public subnet + IGW, no NAT: no. No `VpcConfig`: yes |
| LocalStack | Full egress, always. `internal=True` appears nowhere; the container-client API cannot express it. `vpc_config` is stored and referenced nowhere under `services/lambda_/invocation/` |
| Floci | Full egress. Single flat network; no VPC awareness |
| Moto | Full egress. `vpc_config` shapes API responses and never reaches `run_kwargs` |
| AWS SAM CLI | Full egress. `--docker-network` is additive; `VpcConfig` is not modelled |

Every comparable tool gives full egress and none models VPC networking.
Overcast is alone in attempting it — a genuine differentiator, and also the
reason alpha.37's behaviour was unexpected by everyone arriving from an
adjacent tool.

### 2.4 What Overcast reads

`vpc_strategy.go` has zero occurrences of `NatGateway`, `RouteTable` or
`DestinationCidrBlock`; `handler_natgw.go` and `handler_routetables.go` contain
the string `docker` not at all. Route tables, `0.0.0.0/0` routes and NAT
gateways are CRUD metadata and nothing else.

## 3. The decision

**Split the flag into a named mode, and default it to `open`.**

| | |
| --- | --- |
| New setting | `OVERCAST_VPC_EGRESS=open \| routed \| none`, default **`open`** |
| `open` | The control plane and the default data plane are routable, so every container has egress |
| `none` | Every network Overcast creates is `--internal`. Nothing reaches outside the machine; the Runtime API still works |
| A VPC network under `open` | Still `--internal` when the VPC has no internet gateway. Row E means that costs nothing — egress comes from the routable control plane — so the flag stays honest about the template and `routed` inherits a bit that means something |
| `routed` | Egress per subnet from its route table. **Refused at startup** — see #1571 |
| Deprecated | `OVERCAST_CONTROL_PLANE_INTERNAL` stays, still wins where pinned, logs a deprecation notice |
| Decoupled | The Runtime-API reachability probe decides only whether an isolated control plane is *deliverable*, never whether egress is *wanted* |

**Why `open` and not `routed` now.** `routed` is the honest AWS-faithful answer
and where this ends up. Overcast cannot implement it today — route tables are
read nowhere (§2.4) — so shipping isolation now delivers only the *withholding*
half of AWS's model. That is not fidelity; it is a strictly harsher network than
AWS, in which a private-with-NAT subnet is indistinguishable from an isolated
one.

**Why not half-ship `routed`.** Every approximation available is a different
mode wearing this one's name. `open` would grant egress the route tables
withhold; `none` would withhold egress they grant. Either silently contradicts
the template the operator pointed at, which is worse than a startup error naming
what is missing.

**Why the VPC bridge keeps following its gateway under `open`.** Flattening it
would buy nothing and cost two things. Nothing, because row E says a container
in an `--internal` VPC takes its default route from the routable control plane
and reaches the internet regardless — measured end-to-end, including a Lambda in
an isolated subnet getting a 403 from real `sts.us-east-1`. And it would cost
the flag's honesty about the template, plus the gateway machinery (#1570) that
keeps it true and that `routed` needs intact. What changed is authority, not the
bit: the gateway no longer *decides* egress on its own.

**Why `none` isolates the data plane too.** Before this, the default `overcast`
plane was never `--internal`, so a machine could isolate its control plane and
every VPC network and still have every non-VPC function reach the internet
(row C). "Hermetic" that leaks on the most common placement there is.

**Why `none` degrades rather than refuses on Docker Desktop.** Containers there
dial the host's routable address, which `--internal` severs; isolating anyway
strands every invocation at INIT. Refusing to start would turn a
partly-achievable mode into no mode at all on the most common developer
platform. So the control plane is left routable, the shortfall is warned about
at startup and reported in health, and every data plane is isolated as asked.

**Migration.** `open` is what most machines already did — `auto` resolved to
not-internal on Docker Desktop, the majority developer host. The machines that
change are containerised and native-Linux hosts, which silently isolated. They
get egress back: a strict widening, so nothing that worked stops working.

## 4. Exact-state verification

Docker's create-network call returns an existing network **unchanged**. It
applies no isolation, no subnet, no driver options. So a network created by an
older Overcast keeps every setting it was born with, for the life of the
machine, while every log line says the name is present and correct. That is
#1564's actual mechanism, and a check that compares only the flag that mattered
last time does not close it.

`docker.NetworkSpec` therefore describes the complete desired state, and
`docker.EnsureNetwork` compares a live network against it field by field on
every start:

| Field | Why |
| --- | --- |
| `driver` | A network of the right name under the wrong driver behaves nothing like the one asked for |
| `internal` | The egress bit |
| `EnableIPv6` | Changes which addresses containers get and which the resolver can answer with |
| IPAM subnet, gateway | Compared only where the spec pins them — a spec that asked for no range has no opinion about the one Docker picked |
| Driver options | Only the keys the spec pins. `enable_ip_masquerade` off makes a network look routable and behave isolated |
| `overcast.network.spec-hash` | SHA-256 of the behavioural fields. **Absent ⇒ mismatched** |

The hash covers behaviour and not identity: version, owner, labels and egress
mode are excluded, so two instances asking for the same state agree on the hash
and a release that changed nothing does not invalidate every network on the
machine.

Outcomes: absent → created. Matches → left alone. Differs with nothing attached
→ removed and recreated (free by construction). Differs with containers attached
→ left alone, WARN naming every field and every container, health **degraded**,
console advisory, `overcast network reset` as the fix. Owned by another instance
→ left alone, always.

### 4.1 Ownership

`ec2.reconcileNetworks` removed every `overcast.service=ec2` network its own
store did not claim. On a daemon running more than one Overcast that deleted
neighbours' live VPC networks — observed, with Docker events
([#1569](https://github.com/overcast-sh/overcast/issues/1569)). VPC networks are
the one Overcast resource named from an emulated resource id rather than from
configuration, so nothing in the name says who made it.

Every network now carries `overcast.instance` (`serviceutil.InstanceDomain`, the
store-scoped identity every container and volume already uses), the sweep sees
only networks matching its own, and the rule that governs it is the one already
written on `docker.LabelInstance`: **absence is not permission**. A memory-backed
instance mints a fresh identity per start and therefore sweeps nothing that
predates it — that leaks a network, where the alternative leaks a neighbour's
running VPC.

Network names also moved under `OVERCAST_NETWORK`
(`{OVERCAST_NETWORK}-vpc-{vpcID}`), reproducing the historical name exactly at
the default so existing installations keep adopting what they have.

## 4.2 Serialising the rebuilds

Every path that changes a network's isolation, driver, addressing or options has
to remove it and create it again, because Docker changes none of those in place.
There are three such paths — startup verification, the internet-gateway
isolation flip, and `overcast network reset` — and they can run concurrently: two
attach/detach calls on a shared network, or an API call racing a reconcile after
the Docker watcher reconnects.

Unserialised they interleave, and **each failure looks like success**:

- Two removes race. The loser gets 404, which `RemoveNetwork` reports as success
  because a missing network is normally the outcome wanted — so the loser goes on
  to create over the winner's freshly created network.
- Two creates race. The loser gets "already exists", which
  `CreateNetworkWithOptions` resolves by returning the existing network — so it
  reports a network created to its spec when it was created to another's.
- A record is rewritten with a network id the other path has already removed,
  leaving every resource in that VPC pointing at nothing.

So `docker.LockNetwork` holds one process-wide mutex per network, every rebuild
path takes it, and every path **re-reads the network after acquiring it**: what
was true before the wait is not what the next call acts on.

**The key is the network name, not its id.** An id is exactly what a rebuild
changes, so a lock keyed by id stops protecting the network at the moment it
matters most. The name is stable across the whole operation and unique per
daemon.

And a removal is confirmed rather than inferred: after removing by id, the
network is inspected again, and the create happens only when it is genuinely
absent. Otherwise a 404 from a network somebody else rebuilt is indistinguishable
from a removal this call performed.

Two Overcast *processes* on one daemon are not serialised by this, and do not
need to be — the ownership label keeps them off each other's networks entirely
(§4.1).

## 5. Not possible under any of this

Real NAT semantics (EIP source addresses, per-gateway SNAT), VPC peering to real
AWS, private endpoints without a VPN, security-group or NACL packet filtering,
and per-subnet routing within one VPC beyond the egress class.

## 6. Known constraints for `routed`

- A network per (VPC × egress class), not per VPC — roughly double. Docker's
  default address pools give about 31 networks total, and an egress-matrix run
  exhausted them on a development host. Needs a `default-address-pools` story.
- Re-placement on route-table mutation: `CreateRoute` after a function is
  running would move containers between networks on a hot path.
- `gw-priority` must be set explicitly once a container can be on two routable
  networks.
- Depends on #1569 being fixed first — no point reading route tables while the
  internet-gateway toggle silently no-ops.

Tracked in [#1571](https://github.com/overcast-sh/overcast/issues/1571).

## 7. Adjacent findings, filed separately

- [#1572](https://github.com/overcast-sh/overcast/issues/1572) — the Runtime API
  address probe tests bindability, not reachability from a container. On a
  Windows host it picks an address the firewall blocks, and every invocation
  fails at INIT with nothing saying why. Same class as #1564: a probe answering
  an adjacent question and reporting it as the real one.
- [#1573](https://github.com/overcast-sh/overcast/issues/1573) — the Lambda init
  volume is keyed by build hash with no owner label, so one instance deletes
  another's. Same ownership class as §4.1.
