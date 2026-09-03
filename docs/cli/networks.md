---
title: "Docker networks"
description: "overcast network status compares every Overcast-managed Docker network against what your configuration asks for and exits non-zero on drift; overcast network reset rebuilds the ones that differ."
section: "Reference"
tags:
  - cli
  - docker
  - docs
  - network
  - networks
  - overcast
---

# Docker networks

`overcast network status` reports every Overcast-managed Docker network that is
not in the state your configuration asks for, and `overcast network reset`
rebuilds the ones that differ. Why a network can differ at all, and what is
checked, is [Network state verification](../networking/network-state.md).

```bash
overcast network status          # read-only; non-zero exit means drift
overcast network reset --dry-run # the repair plan, before doing it
```

Part of the [CLI reference](../cli.md).

## `overcast network status`

Compares every network Overcast manages — the two planes, each per-VPC network,
and under `OVERCAST_VPC_EGRESS=routed` each VPC's egress network beside it —
against the state your configuration would create, and reports each field that
differs. Each line also says where a container on that network gets its route
out, which `docker network inspect` will not tell you. Reads only.

**It exits non-zero when anything differs**, which makes it a CI gate: run it
after an upgrade to catch a machine that has drifted between releases. Exit `0`
means every network it could judge is in the state your configuration asks for.

```text
overcast: ok (internal=false, spec a1b2c3d4e5f6)
overcast_control: NOT in the configured state
    internal: want false, got true
    overcast.network.spec-hash: want a1b2c3d4e5f6, got (absent — created before Overcast labelled its networks, or not by Overcast)
    stop       overcast-lambda-orders (running)
    disconnect my-compose-db (not Overcast's — left running)
```

## `overcast network reset [network...]`

Rebuilds each network that differs, because Docker cannot change isolation,
driver, addressing or driver options on an existing one. Containers **Overcast**
started are stopped first; containers it did not start are disconnected and left
running. A network already in the right state is left alone unless `--force`.
Name one or more networks to narrow it.

> [!CAUTION]
> This stops containers. In an interactive terminal you are asked to confirm
> unless `--yes`/`-y` is given; a non-interactive caller proceeds without
> prompting.

| Flag | Effect |
| --- | --- |
| `--dry-run` | Print what would be stopped, disconnected and rebuilt, and change nothing |
| `--force` | Rebuild a network even though it already matches |
| `--yes`, `-y` | Skip the confirmation prompt |

```bash
overcast network reset --dry-run          # see the plan first
overcast network reset                    # rebuild whatever differs
overcast network reset overcast_control   # just that one
```

Restart Overcast afterwards, along with anything that was stopped — containers
rejoin on their next start.

### Two things to expect afterwards

**The daemon stops reporting the network rather than reporting the new state.**
It sees the removal and drops the entry, so the drift and its advisory clear,
but nothing re-inspects: the network is absent from `/_overcast/health`
until the next startup or Docker reconnect. Absence there means "nothing to
say" — run `overcast network status` to confirm the rebuild in the meantime.
Reporting a positive result instead is
[#1599](https://github.com/overcast-sh/overcast/issues/1599).

**One class of network it will not rebuild:** a per-VPC network created before
Overcast recorded the internet-gateway state on it, which it declines to judge —
see [Network state verification](../networking/network-state.md). Restart
Overcast instead; its startup reconcile has the state store to ask. You will only
meet this on the first start after upgrading.

## Related

- [Network state verification](../networking/network-state.md) — what is checked, and what is repaired without asking
- [Networking and host-based addressing](../networking.md) — the network layout these commands police
- [Environment variable reference](../configuration/reference.md) — `OVERCAST_VPC_EGRESS` and the other network variables
- [Troubleshooting](../troubleshooting.md) — a symptom, and where its answer lives
