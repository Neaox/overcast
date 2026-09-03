---
title: "Bind address and port"
description: "Which addresses and ports Overcast binds for the AWS API and the web console, how to bind several at once, and what LocalStack's GATEWAY_LISTEN maps onto."
section: "Reference"
tags:
  - address
  - bind
  - configuration
  - docs
  - port
  - ports
---

# Bind address and port

Overcast serves the AWS API on `127.0.0.1:4566` natively and `0.0.0.0:4566` in a
container. `OVERCAST_LISTEN` and `OVERCAST_PORT` move it:

```bash
OVERCAST_LISTEN=127.0.0.1,172.17.0.1 OVERCAST_PORT=4576 overcast serve
```

| Variable                        | Default                                     | Sets                                                            |
| ------------------------------- | ------------------------------------------- | --------------------------------------------------------------- |
| `OVERCAST_LISTEN`               | `0.0.0.0` containerised, `127.0.0.1` native | Address(es) the AWS API binds; comma-separate to bind several   |
| `OVERCAST_PORT`                 | `4566`                                      | TCP port for the AWS API                                        |
| `OVERCAST_UI_PORT`              | `4567`                                      | Port for the web console; `0` disables it                       |
| `GATEWAY_LISTEN` *(LocalStack)* | _(none)_                                    | Both of the first two at once, as `<ip>:<port>[,…]`             |

## Binding more than one address

A comma-separated list is how one instance stays reachable from this machine
*and* from its containers over the Docker bridge, without being on any network
the machine is attached to. A wildcard cannot be combined with a specific
address, and the web console binds the first address only.

An explicit value always wins over the environment-dependent default in either
direction — `OVERCAST_LISTEN=0.0.0.0` restores the native reach from a VM or
another machine. `OVERCAST_HOST` was renamed to `OVERCAST_LISTEN` and removed; a
leftover one fails startup naming the replacement rather than being silently
ignored.

## `GATEWAY_LISTEN`

LocalStack's `GATEWAY_LISTEN` maps to `OVERCAST_LISTEN` and `OVERCAST_PORT`
together, and counts as an explicit bind-address setting. Every entry must share
one port: a value naming two has no single `OVERCAST_PORT` to become, so startup
fails rather than dropping a bind.

## Related

- [Running two instances on one host](./two-instances.md) — the other ports, and
  which of them get out of the way on their own
- [Environment variable reference](./reference.md)
- [Configuration](../configuration.md)
