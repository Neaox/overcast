---
title: "Reaching Overcast by name"
description: "overcast bridge publishes overcast.local and every API Gateway custom domain over mDNS and proxies port 80 by Host header, with the per-platform setup each one needs."
section: "Reference"
tags:
  - bridge
  - cli
  - dns
  - docs
  - mdns
  - overcast
---

# Reaching Overcast by name

`overcast bridge` publishes `overcast.local` and `overcast-app.local` over
mDNS, advertises every API Gateway custom domain the daemon registers, and runs
a reverse proxy on port 80 that routes by `Host` header. No hosts-file edits,
no port numbers.

```bash
overcast bridge         # a second terminal, alongside overcast serve
overcast serve --bridge # or inline with the daemon
```

Part of the [CLI reference](../cli.md).

## `overcast bridge`

Runs in the foreground; Ctrl+C withdraws every advertisement.

| Flag | Default | Description |
| --- | --- | --- |
| `--bind-ip` | `127.0.0.1` | IP advertised for every registered hostname. |
| `--http-port` | `80` | Reverse proxy port (`0` disables it — mDNS only). |
| `--api-addr` | `http://localhost:4566` | Emulator backend for the proxy. |
| `--ui-addr` | `http://localhost:4567` | Web console backend for the proxy. |

| URL | Routed to |
| --- | --- |
| `http://overcast.local` | Emulator API (port 4566) |
| `http://overcast-app.local` | Web console (port 4567) |
| `http://api.myapp.local` | Emulator (API Gateway custom domain) |

```bash
overcast bridge --http-port 8080  # avoid needing root/admin for port 80
```

> [!NOTE]
> Port 80 is often held by a local web server, or needs elevated privileges. If
> the bind fails, `bridge` logs a warning with platform-specific instructions
> and carries on — mDNS still works, you just need the port in the URL
> (`http://overcast.local:4566`). `--http-port 0` skips the proxy entirely.

## Platform setup

| Platform | mDNS needs | Binding port 80 |
| --- | --- | --- |
| macOS | Nothing — built-in `dns-sd` | `sudo overcast bridge`, or `--http-port 8080` |
| Linux | avahi (`apt install avahi-daemon avahi-utils`, `dnf install avahi avahi-tools`) | `sudo setcap cap_net_bind_service+ep $(which overcast)`, then run as a normal user |
| Windows | The DNS-SD service, built into Windows 10 1803+ and Server 2019+ (`Start-Service "DNS Client"`) | A one-off URL reservation in an elevated shell: `netsh http add urlacl url=http://+:80/ user=%USERNAME%` |

## Related

- [Networking and host-based addressing](../networking.md) — wildcard DNS, sibling containers and the rest of the addressing story
- [HTTPS and the trust store](./tls.md) — `https` and `trust`
- [Running the daemon](./daemon.md) — `serve --bridge` runs this inline
