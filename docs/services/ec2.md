# EC2 — Elastic Compute Cloud

> AWS docs: https://docs.aws.amazon.com/AWSEC2/latest/APIReference/Welcome.html

EC2 uses the AWS Query protocol (form-encoded POST, XML responses). Operations are
identified by the `Action` parameter with API version `2016-11-15`.

When Docker is available, each VPC is backed by a real Docker bridge network.
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

### Advanced: VPC networking strategies

Real AWS allows overlapping VPC CIDR blocks in the same account/region.
Docker bridge networks on one host do not: overlapping subnets collide at the
kernel routing table level. Overcast exposes a strategy switch so users can
choose the behavior that best matches their workflow.

Configure with `OVERCAST_EC2_VPC_STRATEGY`:

| Strategy           | Behavior                                                                                                                                       | Best For                                                                    |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| `shared` (default) | Overlapping VPCs reuse a single Docker network; `NetworkStatus=shared`.                                                                        | Most local-dev setups where convenience matters more than strict isolation. |
| `strict`           | Overlapping `CreateVpc` requests fail with `InvalidVpc.Range`; conflicting persisted VPCs are marked `NetworkStatus=conflict`.                 | Teams that want loud failures on accidental overlap.                        |
| `remapped`         | Overlapping VPCs get a unique Docker shadow subnet in `100.64.0.0/10`; `NetworkStatus=remapped` and `DockerCidrBlock` records the shadow CIDR. | Multi-VPC simulations that need overlap without Docker subnet collisions.   |

`DescribeVpcs` includes synthetic tag `overcast:network-status=<value>` and
`/_debug/ec2/vpcs` exposes internal fields (`NetworkStatus`,
`DockerNetworkID`, `DockerCidrBlock`) for diagnostics.

**Important caveat for `remapped`:** data-plane packet routing still follows
Docker's real subnet assignment. API metadata keeps the user-requested
`CidrBlock`, but workloads that hardcode raw private IPs are less portable than
DNS-based SDK flows.

`netns` is reserved for future work and is currently rejected at startup with a
configuration error.

---

## Limitations and divergences from AWS

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
  `overcast_lambda` network) when a function has a `VpcConfig`. This provides real
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

| Strategy             | Status                              | Behaviour on overlapping CIDRs                                                                                                      |
| -------------------- | ----------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `shared` _(default)_ | ✅ Implemented                      | VPCs with the same CIDR share a single Docker network. Container isolation between sharers is not enforced.                         |
| `strict`             | ⏳ Not yet — falls back to `shared` | Reject overlapping CIDRs at `CreateVpc`. Startup always tolerates pre-existing overlaps (first-one-wins, losers marked `conflict`). |
| `remapped`           | ⏳ Not yet — falls back to `shared` | Allocate a shadow `/16` from `100.64.0.0/10` when the requested CIDR collides. API responses still show the user's CIDR.            |
| `netns`              | ⏳ Not yet — falls back to `shared` | Per-VPC Linux network namespace. Real overlap with real isolation. Requires root / `CAP_NET_ADMIN`.                                 |

Values other than `shared` are accepted today but log a warning at
startup and fall back to `shared`. The design for each is captured in
[docs/plans/ec2-vpc-network-strategies.md](../plans/ec2-vpc-network-strategies.md).

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
  otherwise. If you care, wait for `remapped` or `netns`.
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

### `strict` (planned)

- **What it will do.** `CreateVpc` rejects any CIDR that overlaps an
  existing VPC with `InvalidVpc.Range`. Startup reconcile never fails —
  VPCs whose CIDR collides with another existing VPC are marked
  `NetworkStatus=conflict` and refused for container-backed operations
  (`RunInstances`, `CreateDbInstance`, etc.) with a clear emulator
  error.
- **When you'd use it.** You want loud, early failure on accidental
  overlap — ideal for CI pipelines or tests where overlapping CIDRs
  signal a bug in your IaC, not an intended configuration.
- **When _not_ to use it.** You're running CDK apps or CloudFormation
  templates that legitimately create overlapping CIDRs (multi-account
  simulation, dev/prod parity tests). They'll fail at deploy.

### `remapped` (planned)

- **What it will do.** When a new VPC's CIDR collides, Overcast
  silently carves a shadow `/16` out of `100.64.0.0/10` (CGNAT space),
  stores it as `DockerCidrBlock`, and creates the Docker network there.
  `DescribeVpcs` and every other API response still reports the user's
  `CidrBlock`. A translation layer converts between fabricated and real
  IPs for `PrivateIpAddress` fields, ENI descriptions, etc.
- **When you'd use it.** You're running CDK or Terraform workloads
  where overlap is expected and you rely on API responses matching the
  CIDR you asked for. Highest fidelity.
- **When _not_ to use it.** Your containers talk to each other by raw
  private IP (hardcoded in config files, not resolved via DNS). The
  fabricated IPs will not be reachable — only the shadow addresses are
  real. Workloads that use service discovery, ENI DNS, or RDS/ELB
  endpoint DNS are unaffected.

### `netns` (planned, speculative)

- **What it will do.** Create containers with `--network=none` and
  move their veth into a per-VPC Linux network namespace with its own
  bridge and routing table. Each netns has an independent address
  space, so `10.0.0.0/16` in `vpc-A` is genuinely unrelated to the same
  CIDR in `vpc-B`.
- **When you'd use it.** You need real AWS-grade VPC isolation with
  real overlap support. The only option that's faithful to both the
  AWS model and the network behaviour simultaneously.
- **When _not_ to use it.** You're not running overcastd as root
  inside a container with `CAP_NET_ADMIN`. The netns plumbing Docker
  doesn't expose requires elevated privileges that most dev setups
  don't grant. Also: it's a substantially heavier code path than the
  other three, so the performance overhead is real.

### Picking a strategy

| Situation                                                                     | Use                                      |
| ----------------------------------------------------------------------------- | ---------------------------------------- |
| Single VPC, or multiple non-overlapping CIDRs                                 | `shared` _(default)_                     |
| CI that should fail loudly on accidental CIDR collisions                      | `strict` (today: fallback to `shared`)   |
| CDK/TF apps with legitimate overlapping CIDRs that care about API-visible IPs | `remapped` (today: fallback to `shared`) |
| Testing real container-level VPC isolation with overlapping CIDRs             | `netns` (today: fallback to `shared`)    |

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
| `shared`   | This VPC reuses a Docker network owned by another VPC (shared mode).   |
| `unbacked` | No Docker network (Docker was unavailable, or the last create failed). |
| `conflict` | Reserved for `strict` mode — CIDR collided with another existing VPC.  |
| `remapped` | Reserved for `remapped` mode — backed by a shadow CIDR.                |

`NetworkStatus` is persisted on each VPC record and written into the
startup reconcile logs (`reconcile networks: …`). Debug-endpoint and
web UI surfacing is planned alongside the future strategies — see
[the plan](../plans/ec2-vpc-network-strategies.md).

<!-- BEGIN overcast:capabilities -->

## Summary

| Category           | ✅ Supported |
| ------------------ | ------------ |
| General            | 69           |
| VPC network states | 3            |

---

## Endpoints

### General

| Operation                       | Status       | Notes                                                                                                     | AWS Docs                                                                                              |
| ------------------------------- | ------------ | --------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `AcceptVpcPeeringConnection`    | ✅ Supported | Transitions from `pending-acceptance` to `active`                                                         | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AcceptVpcPeeringConnection.html)    |
| `AllocateAddress`               | ✅ Supported | Generates eipalloc- ID and synthetic public IP                                                            | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AllocateAddress.html)               |
| `AssociateAddress`              | ✅ Supported | Associates EIP with instance; generates eipassoc- ID                                                      | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AssociateAddress.html)              |
| `AssociateRouteTable`           | ✅ Supported | Associates route table with subnet                                                                        | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AssociateRouteTable.html)           |
| `AttachInternetGateway`         | ✅ Supported | Toggles VPC Docker network from `--internal` to external (bridge)                                         | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AttachInternetGateway.html)         |
| `AttachVpnGateway`              | ✅ Supported | Metadata-only VPC attachment                                                                              | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AttachVpnGateway.html)              |
| `AuthorizeSecurityGroupEgress`  | ✅ Supported |                                                                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AuthorizeSecurityGroupEgress.html)  |
| `AuthorizeSecurityGroupIngress` | ✅ Supported | IpPermissions with protocol, ports, CIDR ranges                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AuthorizeSecurityGroupIngress.html) |
| `CreateInternetGateway`         | ✅ Supported | Generates igw-xxx ID                                                                                      | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateInternetGateway.html)         |
| `CreateKeyPair`                 | ✅ Supported | Generates dummy fingerprint and key material                                                              | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateKeyPair.html)                 |
| `CreateNatGateway`              | ✅ Supported | Requires subnet and EIP; supports TagSpecification                                                        | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateNatGateway.html)              |
| `CreateNetworkInterface`        | ✅ Supported | Requires subnet; assigns synthetic private IP                                                             | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateNetworkInterface.html)        |
| `CreateRoute`                   | ✅ Supported | DestinationCidrBlock + GatewayId                                                                          | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateRoute.html)                   |
| `CreateRouteTable`              | ✅ Supported | VPC must exist; auto-creates local route                                                                  | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateRouteTable.html)              |
| `CreateSecurityGroup`           | ✅ Supported | Default egress allow-all rule added on create                                                             | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateSecurityGroup.html)           |
| `CreateSubnet`                  | ✅ Supported | VPC must exist; AZ defaults to region+"a"                                                                 | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateSubnet.html)                  |
| `CreateTags`                    | ✅ Supported | Tag any resource by ID                                                                                    | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateTags.html)                    |
| `CreateVpc`                     | ✅ Supported | CidrBlock required; creates Docker bridge network (`--internal` unless IGW attached) and main route table | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateVpc.html)                     |
| `CreateVpcEndpoint`             | ✅ Supported | Metadata-only; Gateway and Interface types accepted; state always "available"                             | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateVpcEndpoint.html)             |
| `CreateVpnGateway`              | ✅ Supported | Metadata-only; type ipsec.1 with AmazonSideAsn                                                            | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateVpnGateway.html)              |
| `CreateVpcPeeringConnection`    | ✅ Supported | Both VPCs must exist; starts in `pending-acceptance` state                                                | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateVpcPeeringConnection.html)    |
| `DeleteInternetGateway`         | ✅ Supported | Must be detached first                                                                                    | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteInternetGateway.html)         |
| `DeleteKeyPair`                 | ✅ Supported | Idempotent (no error if not found)                                                                        | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteKeyPair.html)                 |
| `DeleteNatGateway`              | ✅ Supported | Marks as deleted                                                                                          | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteNatGateway.html)              |
| `DeleteNetworkInterface`        | ✅ Supported |                                                                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteNetworkInterface.html)        |
| `DeleteRoute`                   | ✅ Supported | Removes route by RouteTableId + DestinationCidrBlock                                                      | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteRoute.html)                   |
| `DeleteRouteTable`              | ✅ Supported | Cannot delete main route table                                                                            | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteRouteTable.html)              |
| `DeleteSecurityGroup`           | ✅ Supported |                                                                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteSecurityGroup.html)           |
| `DeleteSubnet`                  | ✅ Supported |                                                                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteSubnet.html)                  |
| `DeleteTags`                    | ✅ Supported | Remove tags by key from resources                                                                         | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteTags.html)                    |
| `DeleteVpc`                     | ✅ Supported | Removes Docker network; fails if subnets/IGW attached                                                     | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteVpc.html)                     |
| `DeleteVpcEndpoints`            | ✅ Supported | Accepts VpcEndpointId.N; silently skips unknown IDs                                                       | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteVpcEndpoints.html)            |
| `DeleteVpnGateway`              | ✅ Supported | Requires gateway to be detached                                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteVpnGateway.html)              |
| `DeleteVpcPeeringConnection`    | ✅ Supported | From `active` or `pending-acceptance`; transitions to `deleted`                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteVpcPeeringConnection.html)    |
| `DescribeAccountAttributes`     | ✅ Supported | Hardcoded defaults (supported-platforms, max-instances…)                                                  | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeAccountAttributes.html)     |
| `DescribeAddresses`             | ✅ Supported | Filter by AllocationId                                                                                    | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeAddresses.html)             |
| `DescribeAvailabilityZones`     | ✅ Supported | 3 AZs per region (a, b, c)                                                                                | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeAvailabilityZones.html)     |
| `DescribeDhcpOptions`           | ✅ Supported | Returns default DHCP options set for the VPC                                                              | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeDhcpOptions.html)           |
| `DescribeImages`                | ✅ Supported | Hardcoded set of 4 AMIs (AL2, Ubuntu, Windows, AL2023); filter by ImageId                                 | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeImages.html)                |
| `DescribeInstanceTypes`         | ✅ Supported | Hardcoded set: t3.micro/small/medium, m5.large/xlarge                                                     | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInstanceTypes.html)         |
| `DescribeInstances`             | ✅ Supported | Filter by instance-id, instance-state-name                                                                | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInstances.html)             |
| `DescribeInternetGateways`      | ✅ Supported | Filter by internet-gateway-id, attachment.vpc-id                                                          | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInternetGateways.html)      |
| `DescribeKeyPairs`              | ✅ Supported | Filter by KeyName                                                                                         | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeKeyPairs.html)              |
| `DescribeNatGateways`           | ✅ Supported | Filter by nat-gateway-id, vpc-id, subnet-id, state                                                        | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeNatGateways.html)           |
| `DescribeNetworkInterfaces`     | ✅ Supported | Filter by network-interface-id, subnet-id                                                                 | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeNetworkInterfaces.html)     |
| `DescribeRegions`               | ✅ Supported | Hardcoded list of 8 regions                                                                               | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeRegions.html)               |
| `DescribeRouteTables`           | ✅ Supported | Filter by route-table-id, vpc-id                                                                          | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeRouteTables.html)           |
| `DescribeSecurityGroups`        | ✅ Supported | Filter by group-id, group-name, vpc-id                                                                    | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeSecurityGroups.html)        |
| `DescribeSubnets`               | ✅ Supported | Filter by subnet-id, vpc-id, availability-zone                                                            | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeSubnets.html)               |
| `DescribeTags`                  | ✅ Supported | Filter by resource-id, resource-type, key                                                                 | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeTags.html)                  |
| `DescribeVpcAttribute`          | ✅ Supported | Returns enableDnsSupport or enableDnsHostnames                                                            | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeVpcAttribute.html)          |
| `DescribeVpcEndpoints`          | ✅ Supported | Filter by VpcEndpointId.N, vpc-id filter, service-name filter                                             | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeVpcEndpoints.html)          |
| `DescribeVpnGateways`           | ✅ Supported | Filter by vpn-gateway-id, state, type, attachment.vpc-id, attachment.state                                | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeVpnGateways.html)           |
| `DescribeVpcPeeringConnections` | ✅ Supported | Filter by ID, status-code, requester/accepter VPC                                                         | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeVpcPeeringConnections.html) |
| `DescribeVpcs`                  | ✅ Supported | Lists all VPCs; filter by VpcId                                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeVpcs.html)                  |
| `DetachInternetGateway`         | ✅ Supported | Toggles VPC Docker network back to `--internal`                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DetachInternetGateway.html)         |
| `DetachVpnGateway`              | ✅ Supported | Metadata-only VPC detachment                                                                              | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DetachVpnGateway.html)              |
| `DisassociateAddress`           | ✅ Supported | By AssociationId                                                                                          | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DisassociateAddress.html)           |
| `DisassociateRouteTable`        | ✅ Supported | Cannot disassociate main association                                                                      | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DisassociateRouteTable.html)        |
| `ModifyInstanceAttribute`       | ✅ Supported | InstanceType.Value persisted; all other attributes accepted                                               | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_ModifyInstanceAttribute.html)       |
| `ModifySubnetAttribute`         | ✅ Supported | MapPublicIpOnLaunch (metadata only)                                                                       | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_ModifySubnetAttribute.html)         |
| `ModifyVpcAttribute`            | ✅ Supported | EnableDnsSupport, EnableDnsHostnames (metadata only)                                                      | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_ModifyVpcAttribute.html)            |
| `ReleaseAddress`                | ✅ Supported | By AllocationId                                                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_ReleaseAddress.html)                |
| `RevokeSecurityGroupEgress`     | ✅ Supported |                                                                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_RevokeSecurityGroupEgress.html)     |
| `RevokeSecurityGroupIngress`    | ✅ Supported |                                                                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_RevokeSecurityGroupIngress.html)    |
| `RunInstances`                  | ✅ Supported | MinCount/MaxCount, TagSpecifications, async pending→running                                               | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_RunInstances.html)                  |
| `StartInstances`                | ✅ Supported | From stopped state only                                                                                   | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_StartInstances.html)                |
| `StopInstances`                 | ✅ Supported | From running state only; async stopping→stopped                                                           | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_StopInstances.html)                 |
| `TerminateInstances`            | ✅ Supported | Async shutting-down→terminated transition                                                                 | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_TerminateInstances.html)            |

### VPC network states

| Operation  | Status       | Notes                                                                 | AWS Docs                                                                         |
| ---------- | ------------ | --------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `unbacked` | ✅ Supported | No Docker network (Docker unavailable, or the last create failed)     | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_unbacked.html) |
| `conflict` | ✅ Supported | Reserved for strict mode when CIDR collides with another existing VPC | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_conflict.html) |
| `remapped` | ✅ Supported | Reserved for remapped mode and backed by a shadow CIDR                | [docs](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_remapped.html) |

<!-- END overcast:capabilities -->
