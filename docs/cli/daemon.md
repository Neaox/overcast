---
title: "Running the daemon"
description: "overcast serve runs the emulator in the foreground; overcast start runs it detached under a name that stop, restart and logs can find again."
section: "Reference"
tags:
  - cli
  - daemon
  - docs
  - overcast
  - serve
  - start
---

# Running the daemon

`overcast serve` runs the emulator in the foreground. `overcast start` runs the
same daemon detached and records it under a name, so `stop`, `restart` and
`logs` can find it again.

```bash
overcast serve                      # foreground; Ctrl+C stops it
overcast start --name ci --no-wait  # detached, recorded as "ci"
```

Part of the [CLI reference](../cli.md).

## `overcast serve`

Emulator configuration is by environment variable — see the
[environment variable reference](../configuration/reference.md). `serve` itself takes three
flags, for the web console and the optional mDNS bridge.

| Flag | Default | Description |
| --- | --- | --- |
| `--ui-port` | `4567` | Web console port (env: `OVERCAST_UI_PORT`). `0` disables it; if the default port is taken, an ephemeral one is used instead. |
| `--bridge` | off | Also run the mDNS bridge and port-80 reverse proxy — see [Reaching Overcast by name](./bridge.md). |
| `--bridge-bind-ip` | `127.0.0.1` | IP advertised in mDNS when `--bridge` is set. |

```bash
overcast serve
OVERCAST_STATE=memory overcast serve
overcast serve --bridge
```

## Background instances

`overcast start` spawns `overcast serve` as a detached process — or a
container, with `--docker` — and records it at `~/.overcast/instances/<name>/`.
`stop`, `restart` and `logs` look the instance up there by name, and
[`overcast status`](./inspect.md#overcast-status) lists every record. A
foreground `overcast serve` is in no registry and none of them can see it.

### `overcast start`

| Flag | Default | Description |
| --- | --- | --- |
| `--name` | `default` | Instance name, reused by `stop`, `restart` and `logs`. |
| `--port` | `4566` | API port (passed to the daemon as `OVERCAST_PORT`). |
| `--ui-port` | `4567` | Web console port (`0` disables it). |
| `--state` | _(unset)_ | Passthrough for `OVERCAST_STATE`; empty leaves the daemon's own default. |
| `--data-dir` | _(unset)_ | Passthrough for `OVERCAST_DATA_DIR`. |
| `--env KEY=VALUE` | — | One extra environment variable for the daemon. Repeatable. `OVERCAST_*` and `AWS_*` names only. |
| `--no-wait` | `false` | Return immediately instead of waiting for the instance to report healthy. |
| `--timeout` | `60s` | How long to wait for it to become healthy. |
| `--docker` | `false` | Run the instance as a Docker container instead of a native process. |
| `--image` | _(unset)_ | Full image override (`--docker` only). |
| `--channel` | _(unset)_ | Image channel: `alpha`, `beta` or `latest` (`--docker` only). |
| `--data-volume` | _(unset)_ | Docker named volume to mount at `/data` (`--docker` only). |
| `--mount-docker-socket` | `false` | Bind-mount the host Docker socket into the container, for Lambda/ECS sibling containers (`--docker` only). |

```bash
overcast start                              # default instance on 4566/4567
overcast start --name ci --port 4570 --ui-port 0 --no-wait
overcast start --docker --channel latest --mount-docker-socket
```

Given neither `--image` nor `--channel`, a `--docker` instance runs
`ghcr.io/overcast-sh/overcast` at this CLI's own version tag rather than a
floating one, so the container always matches the binary that launched it. An
unreleased (`dev`) build has no matching tag and falls back to `:alpha`, saying
so as it starts.

Starting an instance whose name is already running fails with a pointer to
`overcast stop`; a dead record left by a crashed process is replaced silently.
On success `start` prints the endpoint, the web console URL, where the log
lives, and the `overcast env` line that points AWS tools at it.

### `overcast stop [name]`

Stops the named instance (`default` if omitted) and removes its registry
record. A native process is asked to exit and killed if it has not gone after
10 seconds; a container gets `docker stop` then `docker rm`. Stopping an
already-stopped instance is not an error — it clears the stale record.

```bash
overcast stop
overcast stop ci
```

### `overcast restart [name]`

Stops the instance if it is running, then starts it again from its **saved**
configuration — the flags the original `overcast start` was given, replayed
without your having to remember them. Takes the same `--no-wait` and
`--timeout` flags as `start`.

```bash
overcast restart
overcast restart ci --no-wait
```

### `overcast logs [name]`

Shows a background instance's output: the `daemon.log` file for a native
instance, `docker logs` for a container. Same flags either way.

| Flag | Default | Description |
| --- | --- | --- |
| `-f`, `--follow` | `false` | Keep streaming new output until interrupted (Ctrl+C). |
| `-n`, `--tail` | `100` | Lines to show from the end of the log. |

```bash
overcast logs
overcast logs ci --follow --tail 500
```

## Related

- [Checking a running daemon](./inspect.md) — `status`, `wait`, `services`, `config`
- [Environment variable reference](../configuration/reference.md) — every environment variable `serve` reads
- [Storage and persistence](../storage.md) — what `--state` and `--data-dir` choose between
