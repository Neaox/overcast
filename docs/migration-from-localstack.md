---
title: "Migrating from LocalStack"
description: "Swap the image line and keep the rest: the LocalStack variables Overcast reads directly, the endpoint and init-hook mapping, which paid-plan services carry over, and the behavioural differences worth knowing."
section: "Getting Started"
tags:
  - docs
  - from
  - localstack
  - migrating
  - migration
---

# Migrating from LocalStack

Overcast is a drop-in replacement for LocalStack: same port, same init-hook
layout, LocalStack's own environment variables honoured directly rather than
renamed, and no auth token or plan. Usually the image is the only line that
changes:

```yaml
# Before
services:
  localstack:
    image: localstack/localstack
    ports: ["4566:4566"]
    environment:
      SERVICES: s3,sqs,dynamodb
      DEBUG: 1

# After — the environment block carries over as-is; both variables are honoured
services:
  overcast:
    image: ghcr.io/overcast-sh/overcast:latest
    ports: ["4566:4566"]
    environment:
      DEBUG: 1
```

`AWS_ENDPOINT_URL` does not change: Overcast listens on `4566` too. For the
item-by-item audit behind that — every port, URL, hostname, container
convention and client tool, with its status — see the
[compatibility matrix](./localstack-compatibility.md).

---

## LocalStack's editions

LocalStack restructured its editions on 23 March 2026: one image for every
plan, an auth token required to start it, and a free "Hobby" plan for
non-commercial use that leaves most services, and local state persistence, to
the paid Base and Ultimate plans. Overcast has no plans. Every service it
emulates is in the one build — ECS, RDS, Cognito, CloudFront and the others
named in the [matrix](./localstack-compatibility.md#not-in-scope) included — so
a setup that leaned on a paid-plan service migrates the same way as one using
S3 and SQS, and persistence needs nothing beyond a mounted volume (see
[Storage and persistence](./storage.md)). What has no equivalent here was never
a plan question: snapshot save/load, `SQS_ENDPOINT_STRATEGY` and the `/_aws/*`
inspection endpoints are each listed in the matrix with the alternative.

A carried-over `LOCALSTACK_AUTH_TOKEN` is recognised and inert: startup logs it
once by name, nothing is gated behind it, and it can stay or go. The matrix
measures Overcast against LocalStack's interface as it stands after that change
— ports, URLs, variables and conventions — not against any edition's service
list.

---

## Environment variables

Every LocalStack variable below is read directly. You can rename them to their
Overcast spelling at your leisure, or never.

| LocalStack                            | Overcast                            | Status                                              |
| ------------------------------------- | ----------------------------------- | ---------------------------------------------------- |
| `LOCALSTACK_HOST`                     | `OVERCAST_HOSTNAME`                 | Alias. Accepts the `hostname[:port]` form            |
| `HOSTNAME_EXTERNAL`                   | `OVERCAST_HOSTNAME`                 | Alias, chained after `LOCALSTACK_HOST`               |
| `EDGE_PORT`                           | `OVERCAST_PORT`                     | Alias                                                |
| `GATEWAY_LISTEN`                      | `OVERCAST_LISTEN` + `OVERCAST_PORT` | Alias. Accepts `<ip>:<port>[,…]`, one port only      |
| `DATA_DIR`                            | `OVERCAST_DATA_DIR`                 | Alias. Counts as an explicit data dir for `auto`     |
| `DEFAULT_REGION`                      | `OVERCAST_DEFAULT_REGION`           | Alias                                                |
| `DEBUG=1`                             | `OVERCAST_LOG_LEVEL=debug`          | Alias. `DEBUG=0` is a no-op                          |
| `LS_LOG`                              | `OVERCAST_LOG_LEVEL`                | Alias. `trace-internal` → `trace`, `warning` → `warn` |
| `PERSISTENCE=1`                       | `OVERCAST_STATE=persistent`         | Alias. `PERSISTENCE=0` is a no-op                    |
| `ENFORCE_IAM`                         | `OVERCAST_ENFORCE_IAM`              | Alias                                                |
| `LAMBDA_RUNTIME_ENVIRONMENT_TIMEOUT`  | `LAMBDA_INIT_TIMEOUT_SECONDS`       | Alias                                                |
| `LAMBDA_REMOVE_CONTAINERS=0`          | `LAMBDA_KEEP_CONTAINERS=true`       | Alias, inverted. Same default either way             |
| `DNS_ADDRESS=0`                       | `OVERCAST_DNS=false`                | Alias. A bind address is a no-op                     |
| `DOCKER_HOST`                         | `LAMBDA_DOCKER_SOCKET`              | Read directly; `LAMBDA_DOCKER_SOCKET` still wins     |

Everything else LocalStack documents is **recognised and inert**: never
rejected, never silently missed, and named in a startup log line with the
reason it does nothing — so you can drop it once you have seen it.

| Recognised, no effect                                     | Why                                             |
| --------------------------------------------------------- | ----------------------------------------------- |
| `SERVICES`, `EAGER_SERVICE_LOADING`                       | Every service is always loaded                  |
| `LOCALSTACK_AUTH_TOKEN`, `LOCALSTACK_API_KEY`, `ACTIVATE_PRO` | Nothing here is auth-gated                  |
| `SQS_ENDPOINT_STRATEGY`                                   | Queue URLs follow the caller — see below        |
| `S3_SKIP_SIGNATURE_VALIDATION`                            | Signature checking is server-wide, not S3-only  |
| `IAM_SOFT_MODE`                                           | Policies are stored, never enforced, by default |
| `DISABLE_CORS_CHECKS` and the other CORS knobs            | CORS is already unconditionally permissive      |
| `LAMBDA_DOCKER_NETWORK`, `MAIN_DOCKER_NETWORK`            | Adjacent concept, opposite default — see below  |
| `LAMBDA_KEEPALIVE_MS`                                     | Idle-container lifetime is a fixed 15 minutes   |
| `LAMBDA_DOCKER_FLAGS`, `ECS_DOCKER_FLAGS`, `EC2_DOCKER_FLAGS`, `BATCH_DOCKER_FLAGS`, `LAMBDA_RUNTIME_EXECUTOR` | No flag pass-through; Docker is the only executor |
| `SNAPSHOT_*`                                              | Persistence here is incremental, not snapshot-based |
| `PROVIDER_OVERRIDE_*`                                     | One implementation per service                  |
| `MAIN_CONTAINER_NAME`, `DISABLE_EVENTS`, `SKIP_SSL_CERT_DOWNLOAD`, `ALLOW_NONSTANDARD_REGIONS`, `ENABLE_CONFIG_UPDATES` | Nothing to name, send, download, allow or update |

The mappings came out of a full compatibility audit tracked in
[#1190](https://github.com/overcast-sh/overcast/issues/1190).

### When an alias and its Overcast name disagree

Setting both spellings to the *same* value is fine — that is the natural result
of migrating a compose file line by line. Setting them to **different** values
fails startup naming both, rather than silently preferring one. The same applies
to a `LOCALSTACK_HOST` or `GATEWAY_LISTEN` port that disagrees with
`OVERCAST_PORT`, and to a `GATEWAY_LISTEN` naming more than one distinct port:
there is no single `OVERCAST_PORT` for it to map to, so Overcast refuses rather
than dropping one of the binds.

The Docker images stay out of the way of that check. They bake exactly one
`OVERCAST_*` default, `OVERCAST_DATA_DIR=/data`, and mark it as the image's own
rather than user intent — so `DATA_DIR` overrides it instead of conflicting with
it, and a LocalStack `environment:` block carried over unchanged never trips a
conflict against something the image itself shipped.

### Why `LAMBDA_DOCKER_NETWORK` is inert rather than aliased

`LAMBDA_DOCKER_NETWORK` is the one worth a sentence. It names a network Lambda
containers join, defaulting to Docker's built-in `bridge`; Overcast's
`OVERCAST_NETWORK` names the single shared network *every* container it starts
joins so they can reach each other by name. Aliasing them would strip Lambda
containers of that network the moment a migrated compose file's
`LAMBDA_DOCKER_NETWORK: bridge` carried over. Set `OVERCAST_NETWORK` directly if
you deliberately want a non-default network.

`MAIN_DOCKER_NETWORK` is the same concept from LocalStack's other side — the
network LocalStack itself is on, which it also falls back to for Lambda — and
is inert here for the same reason.

### Egress works the same, and is now a setting

LocalStack never isolates a Docker network, so a Lambda in a VPC reaches the
internet there whatever its subnets look like. Overcast's default,
`OVERCAST_VPC_EGRESS=open`, is the same — so a migration needs nothing here.

Two notes if you are moving from an Overcast between 0.0.1-alpha.37 and this
release: egress on your machine may have been withheld and is not any more, and
`OVERCAST_CONTROL_PLANE_INTERNAL` is deprecated in favour of the mode. If you
set it to `false` to restore LocalStack's behaviour, you can drop it — that is
now the default.

Overcast can also do two things LocalStack cannot. `OVERCAST_VPC_EGRESS=none`
gives nothing it starts a route out of the machine, for deterministic CI or to
prove a stack has no hidden external dependency. `OVERCAST_VPC_EGRESS=routed`
decides egress per subnet from its route table, so a function in a private
subnet with no NAT gateway fails with `ENETUNREACH` locally rather than in a
deploy.

Run Overcast in a container, or against a native Linux Docker daemon, to get
the whole of either — on Docker Desktop the control plane has to stay routable,
so containers keep a route out and Overcast says so at startup. See
[Egress modes](./networking/egress.md) and
[`routed`](./networking/routed-egress.md).

---

## Endpoint mapping

Six LocalStack paths are **served as-is** — leave those callers alone. The
rest have an Overcast endpoint to point at.

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
[debug endpoints reference](./debug-endpoints.md#compatibility-aliases) for
the parameters and the two SES fields (`Region`, `RawData`) that are omitted
because the capture does not hold them.

Every other path under `/_localstack/` or `/_aws/` answers 404 with the
Overcast endpoint that replaces it, so a missed one says so instead of
returning an S3 error. The remaining `/_aws/*` aliases are tracked in
[#1545](https://github.com/overcast-sh/overcast/issues/1545).

---

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

**Do fix a healthcheck that points at neither.** A 404 there is
indistinguishable from a dead container: the orchestrator restarts Overcast,
and on the default in-memory state backend a restart is a wipe — so a deploy
running at the time loses the resources it had already created, and the client
polling them is told they no longer exist. Set `OVERCAST_STATE=persistent` with
a mounted volume if you need state to survive a restart at all.

---

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

---

## Testcontainers

Point an existing LocalStack Testcontainers module at the Overcast image (Java's
`asCompatibleSubstituteFor`, for example) and it fails in non-obvious ways: those
modules parse the image tag as a LocalStack version to pick legacy behaviours,
and wait for a log line Overcast does not emit. Use
[Overcast's own module](./testcontainers.md) — a Go module ships today, and the
generic-container recipe on that page works from every other language.

---

## Behavioural differences

Deliberate divergences, so you know what to expect.

### S3: path-style by default, virtual-hosted supported

Overcast returns path-style URLs (`http://localhost:4566/bucket/key`). Both
virtual-hosted forms work too — `bucket.s3.<base>` and the bare `bucket.<base>` —
and unlike LocalStack neither needs an `s3.` prefix on your endpoint. What they
do need is for the bucket subdomain to resolve, which `*.localhost` does on Linux
and macOS but not on Windows. `OVERCAST_HOSTNAME=localhost.overcast.sh` makes it
work everywhere; an existing `localhost.localstack.cloud` is recognised and keeps
working.

To force path-style instead of configuring a hostname:

```bash
aws configure set s3.addressing_style path                                  # AWS CLI
# boto3: boto3.client('s3', config=Config(s3={'addressing_style': 'path'}))
```

> [!WARNING]
> CDK's asset publisher always uses virtual-hosted style and ignores
> `forcePathStyle`, so on Windows it needs the hostname, not the setting — see
> [CDK § S3 asset upload fails on Windows](./cdk.md#s3-asset-upload-fails-on-windows).

### SQS: queue URLs follow the caller

There is no `SQS_ENDPOINT_STRATEGY` equivalent. Overcast mints each queue URL on
the origin the caller reached it on, so a host CLI gets `localhost:4566` and a
Lambda container gets an address it can dial. This matters because AWS SDKs
resolve the SQS endpoint from the `QueueUrl` and ignore `AWS_ENDPOINT_URL` when
doing so — see
[SQS § Queue URLs and endpoint resolution](./services/sqs.md#queue-urls-and-endpoint-resolution).
Queue URLs carried over from a LocalStack setup keep resolving on both sides of
the container boundary, because `localhost.localstack.cloud` is remapped to
Overcast inside the containers it starts. Drop `SQS_ENDPOINT_STRATEGY`.

### Lambda: Docker-based execution

Functions run in containers built on the official AWS base images
(`public.ecr.aws/lambda/<runtime>`), so the Docker socket has to be reachable —
see `LAMBDA_DOCKER_SOCKET` and `OVERCAST_NETWORK` in the
[environment variable reference](./configuration/reference.md). Without Docker,
functions can
still be created and managed; invocations degrade to a built-in Node.js runtime
for simple handlers.

### Persistence: auto-detected, like LocalStack's `DATA_DIR` presence

LocalStack enables persistence when `DATA_DIR` is set. Overcast's default
(`OVERCAST_STATE` unset, i.e. `auto`) behaves the same way: `hybrid` when a
volume or bind mount is present at `OVERCAST_DATA_DIR` — or a database is already
there — and `memory` otherwise. Set `OVERCAST_STATE` explicitly for a specific
backend regardless of what is mounted. See
[Storage and persistence § The auto default](./storage.md#the-auto-default).

A volume carried over at LocalStack's own path is read where it is. Overcast
keeps state in `/data`, but a compose file migrated line by line still mounts
`/var/lib/localstack` — so when that is the only volume mounted, it becomes the
state directory and a startup line says so. Mount `/data`, or set
`OVERCAST_DATA_DIR`, to choose otherwise; either wins.

```yaml
volumes:
  - "./volume:/var/lib/localstack" # works unchanged
  - "/var/run/docker.sock:/var/run/docker.sock"
```

> [!WARNING]
> **Not true of the `overcast-slim` image or the `overcastd` binaries.** Both are
> built without SQLite, so `auto` there always resolves to `memory` — a mounted
> volume gives you no persistence at all, and nothing announces it beyond the
> startup log. Replacing a LocalStack container that had `DATA_DIR` set? Use the
> full `ghcr.io/overcast-sh/overcast` image, or add `OVERCAST_STATE=wal`. See
> [Builds without SQLite](./storage.md#builds-without-sqlite).

### Request IDs: always present

Every response, errors included, carries `x-amz-request-id` (or
`x-amzn-requestid`). Some LocalStack error responses omit it.

---

## Coverage

A hand-maintained list of missing services rots quickly, so this guide carries
none. For current coverage use the
[generated service index](./README.md#services) — every emulated service with its
operation count — and the per-service pages behind it for operation-level detail.
CloudFormation coverage is listed separately under
[supported resource types](./cdk.md#supported-resource-types).

Two defaults worth knowing when coming from LocalStack, both off unless you ask:

| Default                                   | Turn on with                          |
| ----------------------------------------- | ------------------------------------- |
| SigV4 signatures accepted, not verified   | `OVERCAST_SIGV4_VALIDATE=true`        |
| IAM policies stored, never enforced       | `OVERCAST_ENFORCE_IAM=true`           |

---

## Troubleshooting

### "Connection refused" on port 4566

```bash
docker compose ps
curl http://localhost:4566/_overcast/health
```

### A bucket I just created does not exist

Two candidates: path-style addressing (above), or the state went with the last
container. Storage defaults to `memory` when no volume is mounted — see
[Storage and persistence](./storage.md).

### Tests pass with LocalStack but fail here

1. Check the service page under [Services](./README.md#services) — the operation
   may not be emulated yet.
2. Re-run with `OVERCAST_LOG_LEVEL=debug` to see the request and the response.
3. Inspect stored state at `/_overcast/debug/state` (needs `OVERCAST_DEBUG=true`).
4. If the operation is listed ✅ Supported,
   [open a compatibility issue](https://github.com/overcast-sh/overcast/issues/new?template=compat_review.md)
   with a minimal reproduction.
