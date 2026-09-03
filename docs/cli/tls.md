---
title: "HTTPS and the trust store"
description: "overcast https enable sets up browser-trusted TLS in one command; overcast trust manages the Overcast CA in the system trust store on its own."
section: "Reference"
tags:
  - certificates
  - cli
  - docs
  - https
  - overcast
  - tls
  - trust
---

# HTTPS and the trust store

`overcast https enable` is the one command most people run: it creates the
local Overcast CA, installs it into the system trust store, and mints the
server certificate. `overcast trust` exposes the trust-store half on its own.

```bash
overcast https enable            # once per machine
OVERCAST_TLS=auto overcast serve # HTTPS + HTTP/2 on both listeners
```

Part of the [CLI reference](../cli.md). The full guide, including the Docker
and WSL routes, is [HTTPS and HTTP/2](../https.md).

## `overcast https enable|disable|status`

```bash
overcast https enable
overcast https status
overcast https disable
```

With `--endpoint`, `enable` fetches the CA from a running daemon instead of
minting one — the Docker route, where the container owns the CA. Installing a
CA fetched from a non-loopback endpoint additionally needs `--trust-remote`, an
explicit acknowledgement that the remote host could then impersonate any TLS
site to this machine.

## `overcast trust install|uninstall|status`

Lower-level management of the Overcast CA in the system trust store, which
`https enable`/`disable` build on. Reach for it when scripting the pieces
separately.

```bash
overcast trust install
overcast trust status
overcast trust uninstall
```

`install` takes the same `--endpoint` and `--trust-remote` pair as
`https enable`, and for the same reason.

## Related

- [HTTPS and HTTP/2](../https.md) — why the console wants HTTP/2, and the per-platform detail
- [Reaching Overcast by name](./bridge.md) — `overcast bridge`
- [Configuration reference](../configuration.md) — `OVERCAST_TLS`, `OVERCAST_CA_DIR` and the rest
