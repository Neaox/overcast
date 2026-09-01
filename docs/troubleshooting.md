---
title: "Troubleshooting"
description: "Startup preflight warnings, what each one means, and the fix — Overcast logs one actionable WARN the moment a symptom appears, never a wall of output on a healthy setup."
section: "Troubleshooting"
tags:
  - docs
  - troubleshooting
  - preflight
  - warnings
  - debugging
---

# Troubleshooting

## Startup preflight

A handful of environment mistakes cost real time on this project because
they don't look like environment mistakes — Overcast answers normally, and
the symptom (an empty console list, a container that never starts, data that
isn't where you left it) reads exactly like a bug in the emulator. Where
Overcast can tell, it says so: one actionable `WARN`, the moment the symptom
appears, never a wall of startup output and never on a healthy setup.

| Message names...                                                         | Means                                                                                                                    |
| -------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| `No stacks in <region>. There are N in <region>.`                        | The selected region has nothing, but another region does — check `AWS_REGION`/`AWS_DEFAULT_REGION` against what you expect. Served on demand at [`/_overcast/preflight/region`](./debug-endpoints.md), not logged at startup. |
| `Docker is not reachable for: ...`                                        | One or more container-backed services (ECS, RDS, Lambda, MSK, etc.) couldn't reach a Docker daemon and will run metadata-only — container creates fail instead of starting anything. Start Docker, or check the socket is readable by this user. |
| `the API is published on a different host port (N) than it listens on (N)` | The container remapped its port (e.g. `-p 4580:4566`). Overcast already rewrites the common case (queue URLs, split-horizon hostnames); publish 1:1 instead if something still compares the port literally (a Cognito token's `iss`). |
| `a request arrived addressed to "..." — a real AWS hostname`             | A hosts-file entry, DNS override, or proxy is sending `*.amazonaws.com`-bound traffic to Overcast. Point `AWS_ENDPOINT_URL` at Overcast explicitly, or remove the redirect if that wasn't intentional. |
| `OVERCAST_HOSTNAME=... does not resolve` / `virtual-hosted-style addressing will not work` | This host's resolver can't resolve `OVERCAST_HOSTNAME` (or its subdomains) — breaks virtual-hosted S3 and `cdk deploy` asset publishing. The message names the fix for this host. |
| `this run is memory-only, but an existing Overcast database was found`   | `OVERCAST_STATE=memory` (explicitly, or a `-tags nosqlite` build resolving `auto` to memory regardless) is ignoring a database that already has data in it. Set `OVERCAST_STATE=auto` (or `hybrid`/`wal`) to use it instead. |
| `Running in memory-only mode (auto-detected)`                             | No volume is mounted and no `OVERCAST_DATA_DIR` is set — state won't survive a restart. Expected outside of a persistent setup; see [Persistence](./persistence.md). |

A port Overcast wants to bind that's already taken isn't on this list because
it needs no diagnosis: startup fails immediately with the OS's own `bind:
address already in use`, rather than falling back silently.
