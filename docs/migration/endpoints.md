---
title: "LocalStack endpoints and init hooks"
description: "Which /_localstack and /_aws paths Overcast serves as-is, what the rest map to, and how LocalStack health checks, init-hook directories and Testcontainers modules carry over."
section: "Getting Started"
tags:
  - docs
  - endpoints
  - localstack
  - migration
---

# LocalStack endpoints and init hooks

The paths a migrated test suite already calls, behind
[Migrating from LocalStack](../migration-from-localstack.md). Six are served
as-is; the rest answer 404 naming the Overcast endpoint that replaces them.

## Endpoint mapping

| LocalStack                       | Overcast                  | Availability                   |
| -------------------------------- | ------------------------- | ------------------------------ |
| `/_localstack/health`            | **served as-is**, or `/_overcast/health` | Always          |
| `/_localstack/init`              | **served as-is**, or `/_overcast/init` | Always            |
| `/_localstack/init/{stage}`      | **served as-is**, or `/_overcast/init/{stage}` | Always    |
| `POST /_localstack/state/reset`  | **served as-is**, or `/_overcast/reset` | Always           |
| `GET`/`DELETE /_aws/ses`         | **served as-is**, or `/_overcast/ses/inbox/messages` | Always |
| `/_aws/sqs/messages`             | **served as-is**, or `GET /{account}/{queue}` | Always |
| `/_localstack/health` (detailed) | `/_overcast/debug/health` | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/info`              | `/_overcast/debug/config` | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/state`             | `/_overcast/debug/state`  | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/diagnose`          | `/_overcast/debug/state` and `/_overcast/debug/config` | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/config`            | `/_overcast/debug/config` | Read-only: configuration is fixed for the process |
| `/_localstack/usage`             | `/_overcast/metrics`      | Always                         |
| `/_localstack/state/save`, `/load` | —                       | Persistence is incremental, not snapshot-based |
| `/_aws/sns/sms-messages`         | `/_overcast/ses/inbox/messages`, entries with `"kind": "sms"` | Always |
| `/_aws/sns/platform-endpoint-messages` | `/_overcast/ses/inbox/messages`, entries with `"kind": "push"` | Always |
| `/_aws/lambda/runtimes`          | `/_overcast/lambda/runtimes` | Always; Overcast's shape carries more per runtime |
| `/_aws/execute-api/{id}/{stage}/…` | `/restapis/{id}/{stage}/_user_request_/…`, or the host form | Always |
| `/_aws/sns/subscription-tokens/{arn}`, `DELETE /_aws/dynamodb/expired` | — | The token is not exposed; TTL expiry has no manual trigger |

The init pair needs no translation. Overcast's own status endpoint already
answers in LocalStack's shape — the same `BOOT`/`START`/`READY`/`SHUTDOWN`
stages, the same `UNKNOWN`/`RUNNING`/`SUCCESSFUL`/`ERROR` script states — so a
`localstack wait` or an init-script poll reads what it expects. Reset answers
`{"status":"reset"}` where LocalStack returns an empty body; nothing checking
the status code notices.

The `/_aws/` pair is the one a test suite hits: `curl localhost:4566/_aws/ses`
after a send, `/_aws/sqs/messages?QueueUrl=…` to peek a queue without
consuming it. Both answer in LocalStack's shape, over the same store Overcast's
own endpoint reads — see the
[debug endpoints reference](../debug-endpoints.md#compatibility-aliases) for
the parameters and the two SES fields (`Region`, `RawData`) that are omitted
because the capture does not hold them.

Every other path under `/_localstack/` or `/_aws/` answers 404 with the
Overcast endpoint that replaces it, so a missed one says so instead of
returning an S3 error. The remaining `/_aws/*` aliases are tracked in
[#1545](https://github.com/overcast-sh/overcast/issues/1545).

## Health checks

`/_localstack/health` is the one endpoint in the table you do not have to
change. Overcast serves it, in LocalStack's response shape — a `services` map
plus `edition` and `version` — so a compose healthcheck, a `localstack wait`,
or a Testcontainers HTTP wait strategy carried over unedited keeps working:

```yaml
services:
  overcast:
    image: ghcr.io/overcast-sh/overcast:latest
    ports: ["4566:4566"]
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:4566/_localstack/health"]
      interval: 5s
      timeout: 3s
      retries: 5
```

```typescript
// Testcontainers, any language: the LocalStack path works unchanged.
new GenericContainer("ghcr.io/overcast-sh/overcast-slim:latest")
  .withExposedPorts(4566)
  .withWaitStrategy(Wait.forHttp("/_localstack/health", 4566));
```

Prefer `/_overcast/health` for anything you are writing fresh — it reports
per-service emulation tiers, the resolved storage backend and Docker
connectivity, none of which LocalStack's shape has a field for.

> [!WARNING]
> **Do fix a healthcheck that points at neither.** A 404 there is
> indistinguishable from a dead container: the orchestrator restarts Overcast,
> and on the default in-memory state backend a restart is a wipe — so a deploy
> running at the time loses the resources it had already created, and the client
> polling them is told they no longer exist. Set `OVERCAST_STATE=persistent`
> with a mounted volume if you need state to survive a restart at all.

## Init hooks

Shell scripts in `/etc/localstack/init/<stage>.d/` run at the matching lifecycle
stage, with no configuration. An Overcast-native `/etc/overcast/init/<stage>.d/`
works the same way; both trees are scanned, LocalStack's first.

```yaml
services:
  overcast:
    image: ghcr.io/overcast-sh/overcast:latest
    ports: ["4566:4566"]
    volumes:
      - "./init-aws.sh:/etc/localstack/init/ready.d/init-aws.sh"
```

| Stage      | Directory        | When it runs                      |
| ---------- | ---------------- | --------------------------------- |
| `BOOT`     | `boot.d/`        | Before the emulator starts (as root) |
| `START`    | `start.d/`       | After config is loaded, before HTTP |
| `READY`    | `ready.d/`       | After the server is listening     |
| `SHUTDOWN` | `shutdown.d/`    | On graceful shutdown              |

Scripts need the `.sh` extension and the executable bit. They run in
alphabetical order, subdirectories depth-first, and a failing script does not
block the ones after it. Check what ran at `/_overcast/init` (or
`/_overcast/init/ready` for one stage) — always available, no debug flag needed.

The image ships `awslocal`, the same thin `aws` wrapper, so init scripts carry
over unchanged:

```bash
#!/bin/bash
awslocal s3 mb s3://my-bucket
awslocal sqs create-queue --queue-name my-queue
```

> [!NOTE]
> `awslocal` needs the `aws` CLI present in the container. Install it in a
> `boot.d` hook or a custom image layer.
>
> On a **native Windows** daemon, hooks run through `cmd.exe /c`, so a `.sh`
> script needs WSL or Git Bash on the PATH.

Tune with `OVERCAST_INIT_ENABLED` (default `true`), `OVERCAST_INIT_DIRS`
(default `/etc/localstack/init,/etc/overcast/init`) and `OVERCAST_INIT_TIMEOUT`
(default `30s`).

## Testcontainers

Point an existing LocalStack Testcontainers module at the Overcast image (Java's
`asCompatibleSubstituteFor`, for example) and it fails in non-obvious ways: those
modules parse the image tag as a LocalStack version to pick legacy behaviours,
and wait for a log line Overcast does not emit. Use
[Overcast's own module](../testcontainers.md) — a Go module ships today, and the
generic-container recipe on that page works from every other language.

## Related

- [Migrating from LocalStack](../migration-from-localstack.md) — the image swap and the rest of the move
- [LocalStack environment variables](./environment-variables.md) — the alias and ignored-variable tables
- [Behavioural differences](./differences.md) — what changes once it is running
- [Debug endpoints](../debug-endpoints.md) — the full `/_overcast/*` reference
- [Testcontainers](../testcontainers.md) — starting Overcast from integration tests
