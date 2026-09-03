---
title: "routed: egress from your route tables"
description: "OVERCAST_VPC_EGRESS=routed decides egress per subnet from the route table associated with it, so a missing NAT gateway fails locally the way it would deployed."
section: "Networking"
tags:
  - docs
  - egress
  - nat
  - networking
  - routing
  - vpc
---

# `routed`: egress from your route tables

Back to [Networking](../networking.md).

`OVERCAST_VPC_EGRESS=routed` decides egress **per subnet**, from the route table
associated with it. LocalStack, Moto and SAM CLI give a VPC-attached function
full egress and model no VPC networking at all, so this is where local emulation
can catch a missing NAT gateway before a deploy does.

| The subnet's `0.0.0.0/0` route | What its containers get |
| --- | --- |
| → an internet gateway **attached to this VPC** | A route out |
| → a NAT gateway that exists and is available | A route out |
| → a gateway that is detached, deleted or gone | Nothing — a blackhole on AWS, and here |
| → anything else (a virtual private gateway, a peering connection, an instance) | Nothing — none of those reaches the internet through anything Overcast runs |
| absent | Nothing. Outbound connections fail with `ENETUNREACH` |

A subnet with no explicit route-table association uses its VPC's main table,
exactly as on AWS. A container placed in several subnets gets a route out when
**any** of them grants one: on AWS such a function reaches the internet from some
of its ENIs and not others, which is not a state one container can be in, and
granting is the reading that does not fail a working stack locally.

**Resources outside a VPC are unaffected.** They sit on the shared data plane,
which `routed` leaves routable, because that is what they get on AWS. So does
anything in a **default VPC**, whose subnets are public on AWS.

> [!IMPORTANT]
> **Run Overcast in a container for this mode.** On Docker Desktop with Overcast
> running natively — and on any native Windows or macOS host — `routed` **cannot
> withhold egress**: a subnet with no `0.0.0.0/0` route still reaches the
> internet. **Two warnings in the startup log** and the
> `vpc-egress-not-withheld` advisory on the console's **Metrics & Health** page
> say so, naming whichever mode you set. Running Overcast **in a container**, or
> against a **native Linux Docker daemon**, is what makes the mode enforceable.
>
> Two host limits cause it, and both the warnings and the advisory name whichever
> applies:
>
> - The control plane stays routable, so containers keep a route out — [the same
>   limit `none` has](./egress.md).
> - VPC placement is not enforced where Overcast's DNS resolver cannot start (no
>   `/etc/resolv.conf` on those hosts), so a VPC-placed container also joins the
>   routable shared data plane — see
>   [what VPC membership does not restrict](./vpcs.md#what-a-vpc-does-not-restrict).

## How the route out is carried

The VPC's plane stays one `--internal` bridge that every container in the VPC
joins, whatever its subnets route to: an isolated database and a NAT-routed
function in one VPC reach each other on AWS, and they could not if each egress
class were its own bridge. A container whose subnet grants egress *also* joins a
second, routable bridge, `overcast-vpc-<vpc-id>-egress`, and takes its default
route from there.

That shape is what makes a route-table change safe on a hot path:

- **A container placed after the change** gets the answer its route table gives
  now. Nothing to restart.
- **A container already running** is moved on or off the egress network in place,
  by one `docker network connect` or `disconnect`. Its plane, its address, its
  DNS names and its control-plane connection are untouched, so an in-flight
  invocation keeps its Runtime API. That is the AWS shape too: a route-table
  change reroutes an ENI in place.

`CreateRoute`, `DeleteRoute`, `AssociateRouteTable`, `DisassociateRouteTable`,
`DeleteRouteTable`, `CreateNatGateway`, `DeleteNatGateway`,
`AttachInternetGateway` and `DetachInternetGateway` each revisit every container
in the VPC, and each move is logged with the subnet and the route table that
decided it. A move Docker refuses does not fail the API call — AWS never refuses
a route for a reason like a daemon's — but is logged at `error`, raised as an
advisory in `GET /_overcast/debug/metrics` (with `OVERCAST_DEBUG=true`), and
retried at the next start.

> [!NOTE]
> `routed` isolates the control plane, as `none` does. A routable control plane
> would hand every container a route out whatever its route table said — which is
> what the measurements behind
> [#1571](https://github.com/overcast-sh/overcast/issues/1571) found.

## The address-pool ceiling

`routed` needs a second Docker network per VPC, and Docker's own default address
pools stretch to about **31 networks in total** on a stock daemon — shared with
every other tool on the machine. Doubling the per-VPC count against that ceiling
is how a run ends in `all predefined address pools have been fully subnetted`.

So the egress networks never draw on those pools. Each is pinned to a `/24`
carved from `OVERCAST_VPC_EGRESS_POOL`, which defaults to `198.18.0.0/16` — the
RFC 2544 benchmarking range, never routed on the internet, and untouched by both
Docker's defaults and the `remapped` VPC strategy's `100.64.0.0/10`.

| | |
| --- | --- |
| VPCs with egress the default pool supports | **256** |
| Address per VPC | One `/24`, allocated once and kept on the VPC's record, so a restart brings the network back at the same range |
| When one is created | On the first placement whose subnet grants a route out. A VPC whose subnets all route nowhere never gets one |
| When one is removed | With its VPC, and at startup when no VPC names it — including every one left behind by a `routed` run after you switch back to `open` or `none` |

Set a wider range if you need more:

```sh
OVERCAST_VPC_EGRESS_POOL=198.18.0.0/15   # 512 VPCs
```

It must be an IPv4 CIDR between `/8` and `/24`, and is validated at startup in
every mode, so a pool written for a `routed` deployment is not found to be
malformed on the day you switch to it. Running out names the pool and how to
widen it, and **fails the placement** rather than quietly starting a container
without the egress its template grants.

## Related

- [Egress modes](./egress.md) — `open` and `none`, and what the setting covers
- [The Docker networks Overcast uses](./docker-networks.md) — the bridges this mode adds
- [Networking troubleshooting](./troubleshooting.md) — when a container has the wrong answer
