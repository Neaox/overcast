---
title: "CLI reference"
description: "Every overcast subcommand — starting and managing daemons, introspection, AWS environment helpers, and networking/TLS setup — with flags, defaults, and examples."
section: "Getting Started"
tags:
  - cli
  - docs
  - overcast
  - reference
  - commands
---

# CLI reference

The `overcast` binary is both the emulator daemon and the host-side tooling
around it — starting and stopping background instances, checking on a running
daemon, and configuring your shell to talk to it. `overcastd` (the slim
binary) exposes the same subcommands, minus `mcp` and the web console.

Every command accepts `--endpoint` (default `http://localhost:4566`); run
`overcast --help` for the exhaustive flag list. For AWS SDK/CLI configuration
instead of `overcast` itself, see [Using AWS SDKs and CLI](./sdk-cli.md).

---

## The daemon

### `overcast serve`

Starts the emulator in the foreground. All emulator configuration is via
environment variables (see the [configuration reference](./README.md#configuration-reference)) —
`serve` itself takes only three flags, for the web console and the optional
mDNS bridge:

| Flag                 | Default     | Description                                                                  |
| --------------------- | ----------- | ------------------------------------------------------------------------------ |
| `--ui-port`           | `4567`      | Port for the web console (env: `OVERCAST_UI_PORT`). `0` disables it. Falls back to an ephemeral port if 4567 is taken. |
| `--bridge`            | off         | Also run the mDNS bridge and port-80 reverse proxy — see `overcast bridge` below. |
| `--bridge-bind-ip`    | `127.0.0.1` | IP advertised in mDNS when `--bridge` is set.                                |

```bash
overcast serve
OVERCAST_STATE=memory overcast serve
overcast serve --bridge
```

---

## Background instances

`overcast start` spawns `overcast serve` as a detached background process (or
container, with `--docker`) and remembers it in a local registry at
`~/.overcast/instances/<name>/`, so `stop`, `restart`, `logs`, and `status`
can find it again by name. Everything below this section operates on that
registry, not on a foreground `overcast serve`.

### `overcast start`

| Flag                    | Default   | Description                                                                                     |
| ------------------------ | --------- | --------------------------------------------------------------------------------------------------- |
| `--name`                | `default` | Instance name. Reused by `stop`/`restart`/`logs`/`status` to identify it.                        |
| `--port`                | `4566`    | API port (passed to the daemon as `OVERCAST_PORT`).                                              |
| `--ui-port`              | `4567`    | Web console port (`0` disables it).                                                               |
| `--state`                | _(unset)_ | Passthrough for `OVERCAST_STATE`; empty leaves the daemon's own default (`auto`).                |
| `--data-dir`             | _(unset)_ | Passthrough for `OVERCAST_DATA_DIR`.                                                              |
| `--env KEY=VALUE`        | —         | Extra environment variable for the daemon. Repeatable. Only `OVERCAST_*`/`AWS_*` names are allowed. |
| `--no-wait`              | `false`   | Return immediately instead of waiting for the instance to report healthy.                        |
| `--timeout`              | `60s`     | How long to wait for the instance to become healthy.                                             |
| `--docker`               | `false`   | Run the instance as a Docker container instead of a native process.                              |
| `--image`                | _(unset)_ | Full image override (`--docker` only).                                                            |
| `--channel`              | _(unset)_ | Docker image channel: `alpha`, `beta`, or `latest` (`--docker` only). See below for the default. |
| `--data-volume`          | _(unset)_ | Docker named volume to mount at `/data` (`--docker` only).                                        |
| `--mount-docker-socket`  | `false`   | Bind-mount the host Docker socket into the container, for Lambda/ECS sibling containers (`--docker` only). |

```bash
overcast start                              # default instance on 4566/4567
overcast start --name ci --port 4570 --ui-port 0 --no-wait
overcast start --docker --channel latest --mount-docker-socket
```

Without `--image`/`--channel`, a `--docker` instance defaults to this CLI's
own version tag (e.g. `ghcr.io/overcast-sh/overcast:0.0.1-alpha.25`) rather
than a floating tag, so it always matches the binary that launched it. An
unreleased (`dev`) build falls back to `:alpha`.

Starting an instance whose name is already running fails with a pointer to
`overcast stop`; a dead record left behind by a crashed process is replaced
silently. On success it prints the endpoint, the web console URL (if
enabled), where the log lives, and the `overcast env` line to point AWS tools
at it.

### `overcast stop [name]`

Stops the named instance (`default` if omitted) — SIGTERM then SIGKILL after
a 10s grace period for a native process, `docker stop && docker rm` for a
container — and removes its registry record. Stopping an already-stopped
instance is not an error: it just cleans up the stale record.

```bash
overcast stop
overcast stop ci
```

### `overcast restart [name]`

Stops (if running) and starts the named instance again using its **saved**
configuration — the flags the original `overcast start` was given, replayed
without needing to remember them. Takes the same `--no-wait`/`--timeout`
flags as `start`.

```bash
overcast restart
overcast restart ci --no-wait
```

### `overcast status`

Pings `/_overcast/health` at `--endpoint` and prints a one-line summary
(reachability, version, storage backend). When the local instance registry is
non-empty it also prints a table of every instance `start` knows about —
name, backend, endpoint, and lifecycle state — regardless of which one
`--endpoint` pointed at.

```bash
overcast status
overcast status --endpoint http://localhost:4570
```

### `overcast wait`

Blocks until the daemon at `--endpoint` reports healthy, then exits 0 — the
CI-friendly counterpart to `status`, which checks once. Useful right after
starting `overcast serve` in the background, before the first AWS call.

| Flag         | Default | Description                                  |
| ------------ | ------- | --------------------------------------------- |
| `--timeout`  | `60s`   | Give up waiting after this long.             |
| `--interval` | `500ms` | How often to poll the health endpoint.       |
| `--quiet`    | `false` | Print nothing on success.                    |

```bash
overcast wait --timeout 30s
```

### `overcast logs [name]`

Shows a background instance's output: the `daemon.log` file for a native
instance, `docker logs` for a container one. Same flags and behaviour either
way.

| Flag             | Default | Description                                           |
| ----------------- | ------- | -------------------------------------------------------- |
| `-f`, `--follow`  | `false` | Keep streaming new output until interrupted (Ctrl+C).   |
| `-n`, `--tail`    | `100`   | Number of lines to show from the end of the log.        |

```bash
overcast logs
overcast logs ci --follow --tail 500
```

---

## Introspection and maintenance

### `overcast services`

Lists the AWS services the daemon at `--endpoint` has enabled and each one's
emulation tier (full/partial/inert/stub), read from `/_overcast/health`.

| Flag       | Default | Description                     |
| ---------- | ------- | ---------------------------------- |
| `--output` | `text`  | `text` (aligned table) or `json`. |

```bash
overcast services
overcast services --output json | jq '.services[] | select(.tier=="stub")'
```

### `overcast config`

Prints the running daemon's effective configuration, fetched from
`/_overcast/debug/config`. **Requires the daemon to have been started with
`OVERCAST_DEBUG=true`** — otherwise this returns a clear error naming the
env var, rather than a bare 404.

| Flag       | Default  | Description                  |
| ---------- | -------- | -------------------------------- |
| `--output` | `pretty` | `pretty` (indented) or `json`.  |

```bash
OVERCAST_DEBUG=true overcast serve &
overcast config
```

### `overcast reset [service]`

Wipes emulated state — all of it, or one service's — via
`POST /_overcast/reset[/{service}]`. This is always available (not gated by
`OVERCAST_DEBUG`) since it grants no more power than deleting every resource
by hand through the ordinary AWS API already would. It is still destructive:
in an interactive terminal you're asked to confirm unless `--yes`/`-y` is
given; a non-interactive caller (CI, a pipe) proceeds without prompting.

```bash
overcast reset                # wipe everything, with a confirmation prompt
overcast reset s3             # wipe only S3 state
overcast reset dynamodb --yes # skip the prompt
```

---

## AWS environment helpers

Full walkthrough with SDK examples: [Using AWS SDKs and CLI](./sdk-cli.md).
Brief reference here:

### `overcast env`

Prints `AWS_*` exports for your shell to `eval`, pointing any AWS tool at the
daemon and clearing every other `AWS_*` variable already in the environment
first, so nothing left over can redirect a call to real AWS.

| Flag       | Default | Description                                                |
| ---------- | ------- | -------------------------------------------------------------- |
| `--region` | _(auto)_ | Region to export: `$OVERCAST_REGION`, else `us-east-1`.        |
| `--shell`  | `auto`  | `auto` (detected from OS), `sh`, `powershell`, or `fish`.       |

```bash
eval "$(overcast env)"                 # sh / bash / zsh
overcast env --shell powershell | iex  # PowerShell
overcast env --shell fish | source
```

### `overcast aws [args...]`

Runs the host `aws` CLI against the daemon with every `AWS_*` variable
scrubbed first and dummy credentials/region/endpoint substituted — a leftover
`AWS_PROFILE` or `AWS_ENDPOINT_URL` can never silently redirect the call.
Every argument after `aws` (including `aws`-native flags like `--debug`)
passes straight through; only a *leading* `--endpoint` is intercepted for
overcast itself.

```bash
overcast aws s3 mb s3://my-bucket
overcast aws --endpoint http://localhost:4570 sqs list-queues
alias awslocal='overcast aws'   # LocalStack muscle memory
```

### `overcast import cognito-users`

Copies users from a real AWS Cognito user pool into an Overcast user pool
(imported users land in `FORCE_CHANGE_PASSWORD` status, since passwords
cannot be extracted from AWS). Sent to the daemon in batches.

| Flag              | Default | Description                                        |
| ------------------- | ------- | ------------------------------------------------------ |
| `--from-profile`   | —       | AWS profile for the source account.                   |
| `--from-region`    | _(auto)_ | Region for the source pool.                           |
| `--from-pool-id`   | —       | **Required.** Source user pool ID in real AWS.        |
| `--to-pool-id`     | —       | **Required.** Target user pool ID in Overcast.        |
| `--user`           | —       | Import a single user by sub (UUID) instead of all users. |
| `--max-users`      | `0`     | Cap on users imported (`0` = unlimited).              |
| `--batch-size`     | `100`   | Users per batch sent to the daemon.                   |

```bash
overcast import cognito-users \
  --from-pool-id us-east-1_abc123 --to-pool-id us-east-1_def456
```

---

## Networking and TLS

These three have their own full guides — [HTTPS and HTTP/2](./https.md) for
`https`/`trust`, and the [`overcast bridge` section of the README](../README.md#overcast-bridge)
for platform-specific mDNS/port-80 setup. Summarized here:

### `overcast bridge`

Publishes `overcast.local` (API) and `overcast-app.local` (web console) via
mDNS, watches the daemon for registered API Gateway custom domains and
advertises those too, and runs an HTTP reverse proxy on port 80 that routes
by `Host` header. Runs in the foreground; Ctrl+C withdraws everything.

| Flag           | Default                  | Description                                    |
| -------------- | ------------------------- | --------------------------------------------------- |
| `--bind-ip`    | `127.0.0.1`               | IP advertised for every registered hostname.        |
| `--http-port`  | `80`                      | Reverse proxy port (`0` disables it — mDNS only).   |
| `--api-addr`   | `http://localhost:4566`   | Emulator backend for the proxy.                    |
| `--ui-addr`    | `http://localhost:4567`   | Web console backend for the proxy.                 |

```bash
overcast bridge                   # in a second terminal, alongside overcast serve
overcast bridge --http-port 8080  # avoid needing root/admin for port 80
overcast serve --bridge           # or run it inline with the daemon
```

### `overcast https enable|disable|status`

One-shot browser-trusted HTTPS setup: creates (or fetches, with `--endpoint`)
the local overcast CA, installs it into the system trust store, and mints the
server certificate.

```bash
overcast https enable            # once per machine
OVERCAST_TLS=auto overcast serve # HTTPS + HTTP/2 on both listeners
overcast https status
overcast https disable
```

`enable`'s only extra flag is `--trust-remote`, required to install a CA
fetched from a non-loopback `--endpoint` (an explicit acknowledgement that
the remote host could then impersonate any TLS site to this machine).

### `overcast trust install|uninstall|status`

Lower-level management of the overcast CA in the system trust store —
`https enable`/`disable` build on these. Useful when scripting the pieces
separately.

```bash
overcast trust install
overcast trust status
overcast trust uninstall
```

---

## Development tooling

### `overcast mcp`

Runs the **workspace** MCP server: repo-aware tools for agents and editors
(service files, doc/test coverage, symbols, conventions), backed by the
files on disk — not by a running daemon. This is a different server from the
one a running `overcast serve` exposes at `/_overcast/mcp`, which answers
questions about the live emulator instead. Not included in slim builds
(`overcastd`), where it errors explaining why.

| Flag          | Default              | Description                                  |
| ------------- | ---------------------- | ------------------------------------------------- |
| `--workspace` | `.`                    | Workspace root path.                              |
| `--listen`    | `127.0.0.1:7778`       | Listen address for the HTTP transport.            |
| `--stdio`     | `false`                | Serve over stdio instead of HTTP (editor-launched mode). |

```bash
overcast mcp --stdio
overcast mcp --listen 127.0.0.1:7778
```
