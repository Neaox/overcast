---
title: "Network state verification"
description: "Docker returns an existing network unchanged, so Overcast checks every field of each network it reuses on start. What is checked, what is repaired automatically, and what network reset does."
section: "Networking"
tags:
  - docker
  - docs
  - networking
  - reset
  - status
---

# Network state verification

Docker's create-network call returns an existing network **unchanged** — no
isolation, no subnet, no driver option applied. A network created by an older
Overcast, a different egress mode, or by hand therefore keeps every setting it
was born with, while `docker network ls` says the name is present and everything
looks fine. So Overcast checks, on every start, that each network it reuses is in
the exact state it would have created it in.

The commands that report and repair what it finds are
[`overcast network status` and `overcast network reset`](../cli/networks.md).

## What is checked, and why

Every field:

| Checked | Why it matters |
| --- | --- |
| `driver` | A network of the right name under the wrong driver behaves nothing like the one asked for |
| `internal` | Decides whether containers on it reach anything outside the machine |
| IPv6 | Changes which addresses containers get, and which Overcast's resolver can answer with |
| IPAM subnet and gateway | Only when Overcast pinned them — a VPC network takes its range from the VPC's CIDR |
| Driver options | `enable_icc`, `enable_ip_masquerade`. A network with masquerading off looks routable and behaves isolated |
| `overcast.network.spec-hash` | The identity of the whole desired state. **A network with no such label is treated as mismatched** — it predates this check, and those are the networks that have actually been wrong |

Three more labels record how the network came to be as it is. None is compared;
they are there so `docker network inspect` answers on its own:

| Label | On | Says |
| --- | --- | --- |
| `overcast.network.version` | every network | the Overcast version that created it, so a mismatch can be traced to a release |
| `overcast.network.egress` | every network | the `OVERCAST_VPC_EGRESS` mode in force when it was created |
| `overcast.network.gateway` | VPC networks | whether the VPC had an internet gateway. This is what lets `overcast network status` work out what the network *should* be without a state store to ask — and a network created before the label exists is one it declines to judge |

## What is repaired, and what waits for you

The two planes (`overcast` and `overcast_control`) and the per-VPC networks are
repaired differently, because only one of them can move its containers across.

| | The planes | Per-VPC networks |
| --- | --- | --- |
| Nothing attached | Removed and recreated to match | Removed and recreated to match |
| Containers attached | **Left alone.** Warned at startup naming every differing field and every attached container, `/_overcast/health` marked **degraded**, console advisory raised, `overcast network reset` named as the fix | **Rebuilt under them.** Each container is disconnected, the network is recreated, and each is reconnected at the address and DNS aliases it had. Connections across the VPC bridge drop; the control-plane connection does not, so an in-flight invocation keeps its Runtime API |
| Owned by another Overcast instance | Left alone, always | Left alone, always |
| Owned by another tool (`docker compose` and friends) | Left alone, always | Left alone, always |

A plane carries every container Overcast has started, so rebuilding it under them
would sever the Runtime API mid-invocation — that repair has to wait for a moment
somebody chose. A VPC network carries only that VPC's resources and Overcast
knows how to put them back, which is what makes the automatic rebuild safe there.

An instance never removes a network it cannot prove it created: every network
Overcast creates carries the identity of the instance that created it, and a
network carrying another tool's ownership labels is left alone whatever its name.

> [!IMPORTANT]
> **On the first start after upgrading**, no network on the machine carries a
> spec-hash label yet, so every one of them mismatches. Every VPC network is
> rebuilt once, dropping open connections across its VPC bridge; containers come
> back at the address and names they had. The two planes are rebuilt too if
> nothing is attached — if your stack is running, they are not, and you get a
> startup warning, `/_overcast/health` at `degraded`, and a console advisory
> naming `overcast network reset`. Stopping your stack before upgrading avoids
> both; otherwise, expect one reconnect and one advisory to clear.

`overcast network reset` is the fix in every case but one.

> [!WARNING]
> **`overcast network reset` cannot repair a VPC network from before the
> upgrade.** Those carry no `overcast.network.gateway` label, so the command
> cannot tell an isolated bridge from a gateway-attached one, and declines rather
> than rebuilding on a guess — it says so, and changes nothing. `--force` too.
> Restart Overcast instead: its startup reconcile has the state store to ask, and
> repairs them.

## Related

- [The Docker networks Overcast uses](./docker-networks.md) — what each network is for
- [Egress modes](./egress.md) — what decides the `internal` flag
- [CLI reference](../cli.md) — the rest of `overcast network`
- [Networking and host-based addressing](../networking.md) — the rest of the addressing story
