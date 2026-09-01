---
title: "Persistence"
description: "Configure OVERCAST_STATE and per-service storage overrides — which backend auto picks, how to mount a volume, and where the active configuration is visible at runtime."
section: "Storage & Performance"
tags:
  - docs
  - persistence
  - storage
  - state
  - configuration
---

# Persistence

Overcast supports four concrete storage backends, set via `OVERCAST_STATE`.
This page covers how to configure them; for the durability comparison and how
to choose one, see [Storage backends](./storage.md).

| Backend      | Description                                                                             |
| ------------ | ----------------------------------------------------------------------------------------- |
| `auto`       | **Default when unset.** Resolves to `hybrid` or `memory` at startup — see below.         |
| `memory`     | All state in-process; lost on restart. Fastest — ideal for CI.                          |
| `hybrid`     | Reads from memory, flushes to SQLite asynchronously. Fast with durability.               |
| `persistent` | Every mutation written synchronously to SQLite. Fully durable, slightly slower.         |
| `wal`        | In-memory reads + append-log durability with replay on startup and periodic compaction. |

**`OVERCAST_STATE` is unset by default, which means `auto`:** Overcast picks a mode based
on whether a durable data location was provided — a volume or bind mount at the data
directory resolves to `hybrid` (persist); nothing mounted resolves to `memory`. In CI,
where containers typically run with no data volume, this means `auto` lands on `memory` —
the fast, ephemeral mode CI wants — with zero configuration. See
[storage.md § The auto default](./storage.md#the-auto-default) for the full decision
rule (it also covers native, non-Docker runs).

For state that persists across restarts, just mount a volume — `auto` does the rest:

```bash
docker run --rm \
  -p 4566:4566 \
  -v $(pwd)/overcast-data:/data \
  ghcr.io/overcast-sh/overcast:latest
```

This resolves to `hybrid` automatically because a volume is mounted at `/data`. Set
`OVERCAST_STATE` explicitly (e.g. `-e OVERCAST_STATE=persistent`) if you need a different
backend than what `auto` would pick.

> [!IMPORTANT]
> **The `overcast-slim` image and the `overcastd` binaries are built without SQLite**, so
> `hybrid` and `persistent` do not exist in them: `auto` always resolves to `memory` there
> and the mounted volume above would be ignored — state is lost on every restart, with no
> error. Add `-e OVERCAST_STATE=wal` (the one durable backend those artifacts do have), or
> use the full `ghcr.io/overcast-sh/overcast` image. See
> [storage.md § Builds without SQLite](./storage.md#builds-without-sqlite).

Persistent/hybrid SQLite data lives at `$OVERCAST_DATA_DIR/overcast.db`. WAL mode uses
`$OVERCAST_DATA_DIR/overcast.wal`.

Hybrid seeds small control-plane namespaces into memory on startup and reads large
data-plane namespaces (messages, log events, metric datapoints) from SQLite on every
access — there is no read-through cache for those, by design — so background schedulers
and dashboards do not continuously poll SQLite for hot resource metadata, while
high-volume data never has to fit in memory. See [storage.md](./storage.md) for the full
backend comparison.

## Per-service storage overrides

Each service can use a different backend. Set `OVERCAST_STATE_<SERVICE>`
where `<SERVICE>` is one of the [service names](./configuration.md#service-names) in
upper case, so CloudWatch Logs is `OVERCAST_STATE_LOGS`:

```bash
docker run --rm -p 4566:4566 \
  -e OVERCAST_STATE=memory \
  -e OVERCAST_STATE_DYNAMODB=persistent \
  -e OVERCAST_STATE_S3=hybrid \
  -v $(pwd)/data:/data \
  ghcr.io/overcast-sh/overcast:latest
```

> **Note:** a few services accept an override that can have no effect, and log a startup
> warning when one is set: `DYNAMODBSTREAMS` (a facade over the `dynamodb` service, which owns
> all stream state), `STS` (its session state lives under IAM's storage), and
> `BEDROCK`/`ORGANIZATIONS` (stateless stubs). Every other service's override works.

In this example DynamoDB writes synchronously to disk, S3 flushes
asynchronously, and every other service uses in-memory (ephemeral)
storage. Each overridden service gets its own SQLite file under
`$OVERCAST_DATA_DIR/<service>/`.

## Where the active configuration is visible

- **`GET /_overcast/health`** — the `storage` object shows the resolved default backend (`default`), what was actually configured (`configured` — e.g. `auto`, when `default` was resolved rather than explicitly set), per-service overrides, and persistent backend health including pending hybrid writes when available.
- **Dashboard footer** — the web management console displays the storage mode with a tooltip listing overrides.
- **Startup log** — when `OVERCAST_STATE` resolves via `auto`, Overcast logs which mode it picked and why (e.g. `storage mode auto-detected: memory (no persistence signal found...) — set OVERCAST_STATE to override`). The web console's Metrics & Health page also surfaces this as an advisory whenever the resolved mode is `memory`.
