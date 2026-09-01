---
title: "EC2 — Elastic Compute Cloud"
description: "EC2 uses the AWS Query protocol (form-encoded POST, XML responses). Operations are identified by the Action parameter with API version 2016-11-15."
section: "Service Reference"
tags:
  - cloud
  - compute
  - docs
  - ec2
  - elastic
  - services
---

# EC2 — Elastic Compute Cloud

EC2 uses the AWS Query protocol (form-encoded POST, XML responses). Operations are
identified by the `Action` parameter with API version `2016-11-15`.

### The default VPC

Each region seeds a default VPC the first time something reads VPCs or subnets
in it — `IsDefault: true`, `172.31.0.0/16`, a default subnet per availability
zone, an attached internet gateway, a main route table with the default route,
and a `default` security group. That is the set `DescribeVpcs --filters
Name=isDefault,Values=true` and CDK's `Vpc.fromLookup(isDefault: true)` read.

Its backing network is not a per-VPC bridge: it is the shared data plane
(`OVERCAST_NETWORK`), which is where every container that named no VPC already
sits. "No VPC" and "the default VPC" are therefore the same place, by
construction rather than by coincidence.

Two consequences follow from that network being the emulator's own:

- `DeleteVpc` on the default VPC removes the record and leaves the network. AWS
  allows the delete, but the network has every container Overcast started
  attached to it.
- Attaching or detaching an internet gateway on it is ignored with a warning.
  The toggle recreates the network, which would sever every attached container.

When Docker is available, each non-default VPC is backed by a real Docker bridge network.
The VPC's CIDR block maps to the Docker subnet, and the network's isolation mode
(`--internal`) reflects whether an internet gateway is attached. When Docker is
unavailable, VPC operations are metadata-only.

On startup, the EC2 service reconciles its stored VPC state against actual Docker
networks — recreating missing networks, updating drifted IDs, and removing
orphaned networks that no longer match a stored VPC. Docker network lifecycle
events (create, destroy, connect, disconnect) are forwarded through the event bus.

### Docker network conventions

| Label                  | Value                   | Purpose                           |
| ---------------------- | ----------------------- | --------------------------------- |
| `overcast.managed`     | `true`                  | Identifies Overcast-managed nets  |
| `overcast.service`     | `ec2`                   | Service that owns the network     |
| `overcast.resource-id` | VPC ID (e.g. `vpc-abc`) | Links network back to the VPC     |
| `overcast.vpc-id`      | VPC ID                  | Additional VPC lookup convenience |

Network naming: `overcast-vpc-{vpcID}`.

See [Advanced: VPC networking strategies](#advanced-vpc-networking-strategies)
below for `OVERCAST_EC2_VPC_STRATEGY` — what each strategy does today, and how
to pick one.

---

## Differences from AWS
The VPC emulation provides enough structure for CDK deployments and SDK-based workflows,
but several aspects differ materially from real AWS networking:

### Networking model

- **No real IP routing between subnets.** On AWS, subnets within a VPC can route to each
  other via the implicit local route. In Overcast, each VPC is a single flat Docker bridge
  network — all containers in the same VPC can reach each other, but there is no per-subnet
  isolation or inter-subnet routing. The CIDR blocks are recorded as metadata but do not
  partition Docker's address space.
- **No NAT gateway, VPN gateway, or transit gateway data plane.** NAT gateways and VPN
  gateways are emulated as metadata only (state and associations tracked, but no real NAT
  or VPN routing). Only internet gateways affect the Docker network topology. Attaching an
  IGW toggles the Docker network between `--internal` (isolated) and normal bridge mode
  (host-routable).
- **Elastic IPs are metadata-only.** EIPs can be allocated, associated, and released, but
  the synthetic IPs assigned are not routable. Containers receive Docker-assigned IPs only.
- **VPC peering is metadata-only.** The state machine (`pending-acceptance` → `active` →
  `deleted`) is emulated, but no cross-network Docker routing is established. Containers in
  peered VPCs cannot actually communicate through the peering connection.
- **Route tables are metadata-only.** Routes are stored and returned correctly in API
  responses, but they do not affect Docker packet routing. The `CreateRoute`,
  `AssociateRouteTable`, and `DisassociateRouteTable` operations are recorded but have no
  effect on traffic.
- **CDK subnet lookup metadata.** CloudFormation-created VPC resources preserve EC2 tags,
  `DescribeSubnets` returns subnet `tagSet`, and `DescribeRouteTables` returns NAT gateway
  routes so CDK can classify private subnet groups during VPC lookups. This metadata does
  not imply NAT data-plane routing.

### Filters

Every `Describe*` **refuses a filter name it does not implement**, with AWS's
`InvalidParameterValue: The filter '<name>' is invalid`. The error goes on to
name every filter that operation does support, and the same sets are in the
Notes column of the [EC2 / VPC operations](ec2/operations.md) table.

That is stricter than AWS in one direction: AWS refuses a name it does not
model, and Overcast additionally refuses a name AWS models but Overcast has not
implemented. It is deliberate. A filter that is accepted and then ignored
answers a question the emulator could not answer — a `describe-vpcs` filtered on
`tag:Name` that returns every VPC in the region reads as "your VPC exists" to a
find-or-create script, which then adopts the wrong one. An error costs a minute;
a wrong answer costs an afternoon. If you hit one, drop the filter or narrow the
call by resource ID.

A filter **name** is matched exactly, as AWS matches it — `Name=VPC-ID` is
refused, because real EC2 refuses it too.

A filter **value** is a pattern, as on AWS: `*` stands for any run of characters
including none, `?` for exactly one, and a backslash escapes either so you can
ask for a literal one.

```sh
aws ec2 describe-vpcs        --filters 'Name=tag:Name,Values=overcast-*'
aws ec2 describe-images      --filters 'Name=name,Values=Amazon Linux 2*'
aws ec2 describe-subnets     --filters 'Name=availability-zone,Values=us-east-1?'
```

Filters are AND-ed with each other and the values within one are OR-ed, as on
AWS, and a `<Resource>Id.N` parameter is AND-ed with them.

### Security groups

- **Security group rules are metadata-only.** Ingress/egress rules are stored and returned
  in `DescribeSecurityGroups`, but they are not enforced at the Docker network level. All
  containers on the same Docker network can communicate freely regardless of security group
  rules. This matches the common local-dev use case where you want connectivity, not
  firewall testing.

### Instances

- **EC2 instances are metadata-only.** `RunInstances` creates state records with async
  `pending` → `running` transitions, but no actual VMs or containers are launched. Instance
  metadata (IDs, state, security groups, subnet placement) is tracked for API compatibility
  with CDK and Terraform, but there is no compute behind it.

### Lambda VPC integration

- **Lambda containers are connected to the VPC's Docker network** (in addition to the
  control plane) when a function has a `VpcConfig`. This provides real
  connectivity between Lambda and other containers on the same VPC network (e.g. RDS,
  ECS tasks). However, subnet-level and security-group-level isolation is not enforced —
  a Lambda connected to one subnet can reach resources in any other subnet within the same
  VPC network.

### General

- **No DHCP option sets** beyond a default stub response.
- **No NACLs (Network ACLs).** Only security groups are emulated (as metadata).
- **No VPC Flow Logs.**
- **Docker dependency.** All networking features degrade gracefully to metadata-only when
  Docker is not available. API responses remain correct; only actual container-level
  connectivity is lost.

---

## Advanced: VPC networking strategies

> **TL;DR** — most users can skip this section. The default works unless
> you're intentionally creating VPCs with overlapping CIDRs.

### The problem

In real AWS, every VPC is an isolated virtual network. Two VPCs in the
same account can legally share or overlap CIDRs (`10.0.0.0/16` twice is
perfectly valid) — the only time overlap matters is when you try to
connect them via peering, Transit Gateway, or a VPN.

Overcast backs each VPC with a Docker bridge network so that real
containers (Lambda, ECS, RDS) launched into a VPC can actually talk to
each other. But every Docker bridge on a host shares a **single kernel
routing table**. The Linux networking stack flat-out refuses to have two
bridges claiming overlapping subnets — it returns
`Pool overlaps with other one on this address space`. That's the
fundamental impedance mismatch: AWS's VPC model assumes per-VPC
isolation, and Docker's default bridge driver assumes host-global
uniqueness.

Overcast can't make that go away. Instead it offers a **strategy** knob
so you can pick how the emulator should behave when the two models
disagree, set via the `OVERCAST_EC2_VPC_STRATEGY` environment variable.

### Strategies

| Strategy             | Status                                    | Behaviour on overlapping CIDRs                                                                                                      |
| -------------------- | ------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------- |
| `shared` _(default)_ | ✅ Implemented                             | VPCs with the same CIDR share a single Docker network. Container isolation between sharers is not enforced.                         |
| `strict`             | ✅ Implemented                             | Reject overlapping CIDRs at `CreateVpc`. Startup always tolerates pre-existing overlaps (first-one-wins, losers marked `conflict`). |
| `remapped`           | ✅ Implemented                             | Allocate a shadow `/16` from `100.64.0.0/10` when the requested CIDR collides. API responses still show the user's CIDR.            |
| `netns`              | ❌ Rejected at startup (not implemented)   | Per-VPC Linux network namespace. Real overlap with real isolation. Requires root / `CAP_NET_ADMIN`.                                 |

`OVERCAST_EC2_VPC_STRATEGY=netns` fails startup with a configuration error
naming the strategies that do exist — it is the one value this variable
refuses outright rather than falling back from. An unrecognised value that
isn't one of the four above (a typo, say) falls back to `shared` with a
logged warning instead.

### `shared` — the default

- **What it does.** For each distinct CIDR in your stored VPCs, Overcast
  creates exactly one Docker bridge network. Additional VPCs requesting
  the same CIDR reuse that network and are marked
  `NetworkStatus=shared`. Reconcile on startup deterministically picks
  one owner per CIDR group (sorted by `VpcID`), adopts existing networks
  by label or IPAM subnet before creating anything new, and removes
  networks that no VPC references.
- **When it's fine** (the common case — single VPC, or multiple VPCs
  with non-overlapping CIDRs): `shared` behaves byte-identically to
  `strict` because no collisions exist to share. You pay zero cost.
- **When to pick a different one**: you're running workloads that
  actually test container-to-container isolation _between_ VPCs that
  share a CIDR. Under `shared`, a container in `vpc-A` (10.0.0.0/16) can
  reach a container in `vpc-B` (10.0.0.0/16) because they're on the same
  bridge. That's wrong in real AWS, and `shared` doesn't pretend
  otherwise. If you care, use `strict` or `remapped` instead.
- **On `CreateVpc` failure modes.** If Docker is unavailable the VPC is
  stored with `NetworkStatus=unbacked` and reconcile picks it up later.
  If Docker is available but the create fails, we log and still store
  the VPC — the API call succeeds, the network is best-effort.
- **On `DeleteVpc`.** The Docker network is only torn down when the
  VPC being deleted was the last one using it. Deleting a sharer leaves
  the owner's network alone.
- **On IGW attach/detach.** Toggling a VPC's `--internal` flag requires
  recreating the backing Docker network. `shared` refuses to do this
  when the network is shared (it would affect every sharer), logs a
  `Warn`, and leaves the existing network in place.

### `strict`

- **What it does.** `CreateVpc` rejects any CIDR that overlaps an
  existing VPC with `InvalidVpc.Range`. Startup reconcile never fails —
  VPCs whose CIDR collides with another existing VPC are marked
  `NetworkStatus=conflict` and refused for container-backed operations
  (`RunInstances`, `CreateDbInstance`, etc.) with a clear emulator
  error.
- **When to use it.** You want loud, early failure on accidental
  overlap — ideal for CI pipelines or tests where overlapping CIDRs
  signal a bug in your IaC, not an intended configuration.
- **When _not_ to use it.** You're running CDK apps or CloudFormation
  templates that legitimately create overlapping CIDRs (multi-account
  simulation, dev/prod parity tests). They'll fail at deploy.

### `remapped`

- **What it does.** When a new VPC's CIDR collides, Overcast
  silently carves a shadow `/16` out of `100.64.0.0/10` (CGNAT space),
  stores it as `DockerCidrBlock`, and creates the Docker network there.
  `DescribeVpcs` and every other API response still reports the user's
  `CidrBlock`. A translation layer converts between fabricated and real
  IPs for `PrivateIpAddress` fields, ENI descriptions, etc.
- **When to use it.** You're running CDK or Terraform workloads
  where overlap is expected and you rely on API responses matching the
  CIDR you asked for. Highest fidelity.
- **When _not_ to use it.** Your containers talk to each other by raw
  private IP (hardcoded in config files, not resolved via DNS). The
  fabricated IPs will not be reachable — only the shadow addresses are
  real. Workloads that use service discovery, ENI DNS, or RDS/ELB
  endpoint DNS are unaffected.

### `netns` (planned, not implemented)

Unlike `strict` and `remapped`, this one is not a fallback case — Overcast
refuses to start at all when `OVERCAST_EC2_VPC_STRATEGY=netns` is set, naming
the strategies that do exist.

- **What it would do.** Create containers with `--network=none` and
  move their veth into a per-VPC Linux network namespace with its own
  bridge and routing table. Each netns has an independent address
  space, so `10.0.0.0/16` in `vpc-A` is genuinely unrelated to the same
  CIDR in `vpc-B` — the only strategy with real overlap *and* real
  isolation.
- **Why it isn't implemented yet.** The netns plumbing Docker doesn't
  expose requires elevated privileges (root, `CAP_NET_ADMIN`) most dev
  setups don't grant, and it is a substantially heavier code path than
  the other three.

### Picking a strategy

| Situation                                                                     | Use                       |
| ----------------------------------------------------------------------------- | -------------------------- |
| Single VPC, or multiple non-overlapping CIDRs                                 | `shared` _(default)_       |
| CI that should fail loudly on accidental CIDR collisions                      | `strict`                   |
| CDK/TF apps with legitimate overlapping CIDRs that care about API-visible IPs | `remapped`                 |
| Testing real container-level VPC isolation with overlapping CIDRs            | not available — `netns` is rejected at startup |

### Why `shared` is the default

The overwhelmingly common Overcast workload is one VPC, or a handful of
VPCs with distinct CIDRs. In both cases `shared` never triggers the
sharing code path and is indistinguishable from a hypothetical
"perfectly isolated" implementation. Users who _don't_ hit the edge
case pay nothing. Users who _do_ hit it get silent, working behavior
with a documented isolation compromise — instead of the alternative
(a noisy reconcile error every startup) which is what overcast did
before strategies existed.

### Inspecting network state

Each VPC carries a `NetworkStatus` value that tells you what the
active strategy decided:

| Value      | Meaning                                                                |
| ---------- | ---------------------------------------------------------------------- |
| `ok`       | This VPC owns its backing Docker network.                              |
| `shared`   | This VPC reuses a Docker network owned by another VPC (`shared` mode). |
| `unbacked` | No Docker network (Docker was unavailable, or the last create failed). |
| `conflict` | `strict` mode — this VPC's CIDR collided with another existing VPC.    |
| `remapped` | `remapped` mode — backed by a shadow CIDR.                             |

`NetworkStatus` is persisted on each VPC record and written into the
startup reconcile logs (`reconcile networks: …`), and exposed on
`/_overcast/debug/ec2/vpcs` alongside `DockerNetworkID` and `DockerCidrBlock`
for diagnostics.

<!-- BEGIN overcast:capabilities -->

## Operations

All 72 listed operations are implemented.
Per-operation status, notes and AWS API links: [EC2 / VPC operations](ec2/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
