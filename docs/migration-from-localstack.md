---
title: "Migrating from LocalStack"
description: "overcast is designed as a drop-in replacement for LocalStack Community Edition. In most cases, changing AWS_ENDPOINT_URL is the only change needed."
section: "Getting Started"
tags:
  - docs
  - from
  - localstack
  - migrating
  - migration
---

# Migrating from LocalStack

overcast is designed as a drop-in replacement for LocalStack Community Edition.
In most cases, changing `AWS_ENDPOINT_URL` is the only change needed.

This guide covers every known difference so you can migrate with confidence.

---

## Quick migration

```bash
# Before (LocalStack)
export AWS_ENDPOINT_URL=http://localhost:4566

# After (overcast) — same URL, different container
export AWS_ENDPOINT_URL=http://localhost:4566
```

Replace the container in your `docker-compose.yml`:

```yaml
# Before
services:
  localstack:
    image: localstack/localstack
    ports: ["4566:4566"]
    environment:
      SERVICES: s3,sqs,dynamodb
      DEBUG: 1

# After
services:
  overcast:
    image: ghcr.io/neaox/overcast:alpha
    ports: ["4566:4566"]
    environment:
      OVERCAST_LOG_LEVEL: debug
```

---

## Environment variable mapping

| LocalStack        | overcast                                 | Notes                                                             |
| ----------------- | ---------------------------------------- | ----------------------------------------------------------------- |
| `LOCALSTACK_HOST` | `OVERCAST_HOSTNAME`                      | **Honoured directly as a compatibility alias** ([#1190](https://github.com/Neaox/overcast/issues/1190)) — no rename needed. The *advertised* name embedded in returned URLs — the true analogue. (`OVERCAST_HOST`, despite the similar name, used to be the bind address instead; it has since been renamed and removed — see the next row). Accepts LocalStack's `hostname[:port]` format (e.g. `localhost.localstack.cloud:4566`): the hostname part maps to `OVERCAST_HOSTNAME`, and a port part is accepted only if it matches `OVERCAST_PORT`. `LOCALSTACK_HOST` and `OVERCAST_HOSTNAME` may both be set to the *same* value; setting them to different values, or to a conflicting port, fails startup naming both rather than silently preferring one |
| `HOSTNAME_EXTERNAL` | `OVERCAST_HOSTNAME`                    | **Honoured directly as a compatibility alias** ([#1190](https://github.com/Neaox/overcast/issues/1190)) — the legacy LocalStack name `LOCALSTACK_HOST` replaced. Chained after `LOCALSTACK_HOST`: if you set both (and/or `OVERCAST_HOSTNAME`), all set values must agree, or startup fails naming every one that disagrees. Unlike `LOCALSTACK_HOST` it never carried a port suffix |
| `EDGE_PORT`       | `OVERCAST_PORT`                          | **Honoured directly as a compatibility alias** ([#1190](https://github.com/Neaox/overcast/issues/1190)). Default: `4566`. Disagreeing with an explicit `OVERCAST_PORT` fails startup naming both |
| `SERVICES`        | — *(recognised, no effect)*              | Overcast runs every service, always, so there is nothing to select. The variable is read and logged once at startup as seen, but never rejected and never given any effect ([#1190](https://github.com/Neaox/overcast/issues/1190)) — drop it once you've migrated, there's nothing it can still be doing |
| `DATA_DIR`        | `OVERCAST_DATA_DIR`                      | **Honoured directly as a compatibility alias** ([#1190](https://github.com/Neaox/overcast/issues/1190)) — SQLite persistence directory. Setting it counts as an explicitly configured data directory for `OVERCAST_STATE=auto`'s detection, the same as `OVERCAST_DATA_DIR` itself would. In the Docker images it also overrides the image's own baked-in `OVERCAST_DATA_DIR=/data` default rather than conflicting with it — that baked value is marked as the image's default, not user intent |
| `DEBUG=1`         | `OVERCAST_LOG_LEVEL=debug`               | **Honoured directly as a compatibility alias** ([#1190](https://github.com/Neaox/overcast/issues/1190)) — verbose logging. `DEBUG=0` is a no-op, leaving `OVERCAST_LOG_LEVEL`'s own default (`info`) or explicit value in place; disagreeing with an explicit non-debug `OVERCAST_LOG_LEVEL` fails startup naming both |
| `DEFAULT_REGION`  | `OVERCAST_DEFAULT_REGION`                | **Honoured directly as a compatibility alias** ([#1190](https://github.com/Neaox/overcast/issues/1190)). Default: `us-east-1` |
| `GATEWAY_LISTEN`  | `OVERCAST_LISTEN` + `OVERCAST_PORT`      | **Honoured directly as a compatibility alias** ([#1190](https://github.com/Neaox/overcast/issues/1190)) — bind address, split into two variables the same way Overcast already does. Accepts LocalStack's `<ip>:<port>[,<ip>:<port>...]` format: addresses map to `OVERCAST_LISTEN`, the (single, agreeing) port maps to `OVERCAST_PORT` — a `GATEWAY_LISTEN` naming more than one port across its entries has no single `OVERCAST_PORT` to map to and is a documented non-match (fails startup rather than picking one and silently dropping the other bind). Counts as an explicit bind-address setting, overriding the environment-dependent default (`0.0.0.0` in a container, `127.0.0.1` natively) the same way an explicit `OVERCAST_LISTEN` would. (Renamed from `OVERCAST_HOST`, which has been removed — a leftover `OVERCAST_HOST` fails at startup naming `OVERCAST_LISTEN` as the replacement, rather than being silently ignored) |
| `PERSISTENCE=1`   | `OVERCAST_STATE=persistent`              | **Honoured as a compatibility alias** ([#1190](https://github.com/Neaox/overcast/issues/1190)) for the closest named Overcast equivalent to LocalStack's persistence toggle. `PERSISTENCE=0` is a no-op, leaving `OVERCAST_STATE`'s own default/auto-detection in place — which, like LocalStack's `DATA_DIR` presence, already resolves to `hybrid` when a volume/data dir is present, `memory` otherwise, so this alias mainly matters when `PERSISTENCE=1` is set *without* also pointing a data directory at something durable. **Not in the `overcast-slim` image or the `overcastd` binaries:** they exclude SQLite, so `persistent`/`auto`→`hybrid` are unavailable there and durability needs `OVERCAST_STATE=wal` — see [storage.md § Builds without SQLite](./storage.md#builds-without-sqlite) |
| `LAMBDA_RUNTIME_ENVIRONMENT_TIMEOUT` | `LAMBDA_INIT_TIMEOUT_SECONDS` | **Honoured directly as a compatibility alias** ([#1190](https://github.com/Neaox/overcast/issues/1190)) — the same concept (seconds to wait for the Lambda runtime environment to start up) under a different name. Default: `10` |
| `LOCALSTACK_API_KEY` / `LOCALSTACK_AUTH_TOKEN` | — *(recognised, no effect)* | No LocalStack Pro/auth-gated feature set to unlock. Read and logged once at startup as seen, but never rejected ([#1190](https://github.com/Neaox/overcast/issues/1190)) |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | *(same names, standard AWS SDK vars)* | Not an Overcast-specific setting — both emulators read these directly via the AWS SDK's normal credential chain. Like LocalStack, Overcast accepts any non-empty value by default: SigV4 signature validation is off unless you opt in with `OVERCAST_SIGV4_VALIDATE=true` |
| —                 | `OVERCAST_STATE`                         | Explicit backend override; unset defaults to `auto`, which — like LocalStack's `DATA_DIR` presence — resolves to persistent (`hybrid`) when a volume/data dir is present, `memory` otherwise. **Not in the `overcast-slim` image or the `overcastd` binaries:** they exclude SQLite, so `auto` there is always `memory` and durability needs `OVERCAST_STATE=wal` — see [storage.md § Builds without SQLite](./storage.md#builds-without-sqlite) |
| —                 | `OVERCAST_DEBUG=true`                    | Enable `/_overcast/debug/*` endpoints                                      |
| —                 | `OVERCAST_TLS_CERT` / `OVERCAST_TLS_KEY` | HTTPS support                                                     |

The "fails startup naming both" rules above describe two settings *you* set
disagreeing. The Docker images get out of the way of that check: they bake no
`OVERCAST_*` environment defaults beyond `OVERCAST_DATA_DIR=/data` (which is
marked as the image's own default, so `DATA_DIR` overrides it), which means a
LocalStack `environment:` block carried over unchanged — `DEFAULT_REGION`,
`EDGE_PORT`, `GATEWAY_LISTEN`, `DEBUG`, `DATA_DIR` and all — configures a
fresh `docker run` without tripping a conflict against anything the image
itself shipped.

### Not aliased

Overcast's LocalStack-compatibility audit ([#1190](https://github.com/Neaox/overcast/issues/1190))
also checked every other variable [LocalStack documents](https://docs.localstack.cloud/aws/capabilities/config/configuration/).
These are deliberately **not** aliased — half-mapping them would be a false-friend trap, not a convenience:

| LocalStack | Why it is not aliased |
| --- | --- |
| `LAMBDA_DOCKER_NETWORK` | Names a Docker network Lambda containers join, defaulting to Docker's built-in `bridge` network. Overcast's `OVERCAST_NETWORK` names the single shared, user-defined network every container Overcast starts joins for sibling-by-name reachability (see [container networking](./dev/container-networking.md)) — adjacent concepts, not equivalent defaults. Aliasing them would silently strip Lambda containers of Overcast's own network the moment a migrated compose file's baked-in `LAMBDA_DOCKER_NETWORK: bridge` (LocalStack's own default) carries over unchanged. Set `OVERCAST_NETWORK` directly if you deliberately want a non-default network |
| `LAMBDA_KEEPALIVE_MS` | Overcast's idle-Lambda-container lifetime is a fixed 15-minute constant, not user-configurable — there is no variable for this alias to point at |
| `DISABLE_CORS_CHECKS`, `EXTRA_CORS_ALLOWED_ORIGINS`, `DISABLE_CORS_HEADERS`, `EXTRA_CORS_ALLOWED_HEADERS`, `EXTRA_CORS_EXPOSE_HEADERS` | Overcast's CORS middleware is already maximally permissive by design for local dev — every origin, method, and header is allowed unconditionally. There is no restrictive posture to disable or extend, so these variables have nothing to toggle |
| `EAGER_SERVICE_LOADING` | Overcast has no lazy-service-loading concept: every service is always fully loaded. There is no "eager" to toggle on |
| `SNAPSHOT_SAVE_STRATEGY`, `SNAPSHOT_LOAD_STRATEGY`, `SNAPSHOT_FLUSH_INTERVAL` | LocalStack's persistence is snapshot-based (periodic save/load of full state). Overcast's is structurally different — `memory`/`hybrid`/`persistent`/`wal` backends that write incrementally, not in snapshots — so there is no equivalent strategy or interval to alias |

The remaining LocalStack-documented variables (roughly 130 more, covering per-service Docker
container tuning, JVM heap sizes, k3s/EKS cluster internals, and LocalStack Pro/Cloud Pods
features Overcast has no analogue for) were reviewed and found to have no genuine Overcast
equivalent. The handful that could plausibly grow one are tracked in
[#1338](https://github.com/Neaox/overcast/issues/1338) rather than half-implemented here.

---

## Endpoint mapping

| LocalStack                       | overcast                  | Notes                          |
| -------------------------------- | ------------------------- | ------------------------------ |
| `/_localstack/health`            | `/_overcast/health`                | Always enabled                 |
| `/_localstack/health` (detailed) | `/_overcast/debug/health`          | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/init`              | `/_overcast/init`         | Always enabled                 |
| `/_localstack/init/{stage}`      | `/_overcast/init/{stage}` | Always enabled                 |
| `/_localstack/state/reset`       | `/_overcast/debug/reset`           | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/info`              | `/_overcast/debug/config`          | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/state`             | `/_overcast/debug/state`           | Requires `OVERCAST_DEBUG=true` |

---

## Init hooks

overcast supports LocalStack-compatible initialization hooks. Shell scripts
placed in `/etc/localstack/init/<stage>.d/` are executed at the corresponding
lifecycle stage — no configuration needed.

An Overcast-native path `/etc/overcast/init/<stage>.d/` is also supported.
Both paths are scanned in order (LocalStack first, then Overcast).

| Stage      | Directory                          | When it runs                      |
| ---------- | ---------------------------------- | --------------------------------- |
| `BOOT`     | `/etc/localstack/init/boot.d/`     | Before overcastd starts (as root) |
| `START`    | `/etc/localstack/init/start.d/`    | After config loaded, before HTTP  |
| `READY`    | `/etc/localstack/init/ready.d/`    | After server is listening         |
| `SHUTDOWN` | `/etc/localstack/init/shutdown.d/` | On graceful shutdown              |

Scripts must have the `.sh` extension and be executable (`chmod +x`). They are
run in alphabetical order; subdirectories are traversed depth-first. A failing
script does not block subsequent scripts.

### Status endpoint

```bash
# All stages
curl -s localhost:4566/_overcast/init | jq .

# Single stage
curl -s localhost:4566/_overcast/init/ready | jq .completed
```

The status endpoint is always available (no debug flag required).

### `awslocal` wrapper

The container image includes `awslocal`, a thin wrapper around `aws` CLI that
automatically sets `--endpoint-url` to the local Overcast instance. Use it in
init scripts:

```bash
#!/bin/bash
awslocal s3 mb s3://my-bucket
awslocal sqs create-queue --queue-name my-queue
```

Note: `awslocal` requires `aws` CLI to be installed in the container. Install it
in a `boot.d` hook or use a custom Dockerfile layer.

### Example docker-compose.yml

```yaml
services:
  overcast:
    image: ghcr.io/neaox/overcast:alpha
    ports: ["4566:4566"]
    volumes:
      - "./init-aws.sh:/etc/localstack/init/ready.d/init-aws.sh"
```

### Configuration

| Variable                | Default                                   | Description              |
| ----------------------- | ----------------------------------------- | ------------------------ |
| `OVERCAST_INIT_ENABLED` | `true`                                    | Disable init hooks       |
| `OVERCAST_INIT_DIRS`    | `/etc/localstack/init,/etc/overcast/init` | Base directories to scan |
| `OVERCAST_INIT_TIMEOUT` | `30s`                                     | Per-script timeout       |

---

## Testcontainers

Tests using LocalStack's Testcontainers modules should switch to
[Overcast's own module](./testcontainers.md) rather than pointing the
LocalStack module at the Overcast image (Java's
`asCompatibleSubstituteFor`, for example): those modules parse the image tag
as a LocalStack version to select legacy behaviours and wait for a log line
Overcast does not emit, so the substitution fails in non-obvious ways. A Go
module ships today; other languages are planned
([#1495](https://github.com/Neaox/overcast/issues/1495)).

---

## Behavioural differences

These are deliberate choices where overcast behaves differently from LocalStack.
Each is documented here so you know what to expect.

### S3: path-style addressing by default

overcast defaults to path-style S3 URLs (`http://localhost:4566/bucket/key`) rather
than virtual-hosted style (`http://bucket.localhost:4566/key`).

Virtual-hosted style **is** supported — both `bucket.s3.<base>` and the bare
`bucket.<base>` — and unlike LocalStack it does not require an `s3.` prefix on
your endpoint. What it does need is for the bucket subdomain to resolve, which
`*.localhost` does on Linux and macOS but not on Windows. Setting
`OVERCAST_HOSTNAME=localhost.overcast.sh` makes it work on every OS; your
existing `localhost.localstack.cloud` setting is also recognised and keeps
working unchanged.

**Impact:** you only need the setting below if you would rather force
path-style than configure a hostname:

```bash
# AWS CLI
aws configure set s3.addressing_style path

# Python boto3
s3 = boto3.client('s3', config=Config(s3={'addressing_style': 'path'}))
```

> **CDK asset publisher on Windows:** CDK's internal Node.js asset publisher
> always uses virtual-hosted style and ignores `forcePathStyle`. On Windows,
> `*.localhost` subdomains don't resolve by default — see the
> [CDK S3 asset upload troubleshooting](./cdk.md#s3-asset-upload-fails-on-windows)
> section for the `OVERCAST_HOSTNAME` workaround.

### SQS: queue URLs follow the caller

LocalStack has `SQS_ENDPOINT_STRATEGY` for choosing the host in returned queue
URLs. overcast has no equivalent setting: it mints each queue URL on the origin
the caller reached it on, so a host CLI gets `localhost:4566` and a Lambda
container gets an address it can dial. This matters because AWS SDKs resolve the
SQS endpoint from the `QueueUrl` and ignore `AWS_ENDPOINT_URL` when doing so —
see [SQS: queue URLs and endpoint resolution](./services/sqs.md#queue-urls-and-endpoint-resolution).

`localhost.localstack.cloud` keeps working: it is recognised for S3
virtual-hosted addressing, and it is mapped to overcast's address inside Lambda
containers, so queue URLs carried over from a LocalStack setup resolve on both
sides of the container boundary.

**Impact:** none for most setups. If you pinned queue URLs to a specific host,
drop `SQS_ENDPOINT_STRATEGY` and let the default apply.

### Lambda: Docker-based execution

Overcast executes Lambda functions inside Docker containers using the official
AWS Lambda base images (`public.ecr.aws/lambda/<runtime>`). This requires Docker
to be available (either via socket mount or TCP). If Docker is not available,
Lambda functions can still be created and managed, but invocations fall back to
a built-in Node.js runtime for simple handlers.

**Impact:** Lambda execution should be compatible with LocalStack Community
Edition. Ensure Docker is accessible to the overcast container (see the
`LAMBDA_DOCKER_SOCKET` and `OVERCAST_NETWORK` configuration variables).

### Persistence: auto-detected, same as LocalStack's `DATA_DIR` presence

LocalStack enables persistence when `DATA_DIR` is set. overcast's default
(`OVERCAST_STATE` unset, i.e. `auto`) works the same way in practice: it resolves to
`hybrid` when a volume or bind mount is present at `OVERCAST_DATA_DIR` (or an existing
database is already there), and `memory` otherwise. See
[docs/storage.md § The auto default](./storage.md#the-auto-default) for the exact rule.

Set `OVERCAST_STATE` explicitly (`persistent`, `hybrid`, `wal`, or `memory`) if you want a
specific backend regardless of what's mounted — for example, to use `OVERCAST_DATA_DIR`
for something else without triggering persistence, or to force `persistent` durability
semantics that `auto` wouldn't pick on its own.

> [!WARNING]
> **This does not hold for the `overcast-slim` image or the `overcastd` binaries.** Both
> are built without SQLite, which `hybrid` and `persistent` require, so `auto` there always
> resolves to `memory` — a mounted volume gives you no persistence at all, and nothing
> announces that beyond the startup log. If you are replacing a LocalStack container
> that had `DATA_DIR` set, either use the full `ghcr.io/neaox/overcast` image or add
> `OVERCAST_STATE=wal`, the one durable backend the slim artifacts do have. See
> [storage.md § Builds without SQLite](./storage.md#builds-without-sqlite).

### Request IDs: always present

overcast always includes `x-amz-request-id` (or `x-amzn-requestid`) on every
response including errors. Some LocalStack error responses omit this header.

---

## Coverage differences

A hand-maintained list of missing services rots quickly, so this guide no
longer carries one. For current per-service coverage, use the
[generated service index](./README.md#services) — it lists every emulated
service with its operation count — and the per-service pages under
`docs/services/` for operation-level detail.

Two behavioural defaults worth knowing when coming from LocalStack:

- **SigV4 validation is off by default.** Requests are accepted without
  signature verification unless you opt in with `OVERCAST_SIGV4_VALIDATE=true`.
- **IAM enforcement is off by default.** Policies are stored and can be
  simulated, but requests are not denied unless you opt in with
  `OVERCAST_ENFORCE_IAM=true`.

The following features that were previously missing are now implemented:

- **Lambda execution** — full Docker-based container execution
- **DynamoDB Streams** — ListStreams, DescribeStream, GetShardIterator, GetRecords
- **DynamoDB transactions** — TransactWriteItems, TransactGetItems
- **DynamoDB GSI** — Global Secondary Indexes supported
- **S3 multipart upload** — CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload, ListParts
- **S3 versioning** — PutBucketVersioning, GetBucketVersioning, ListObjectVersions
- **SNS → SQS fan-out** — working
- **SQS → Lambda ESM** — event source mapping with CRUD and polling delivery
- **CloudFormation** — CreateStack, UpdateStack, DeleteStack, DescribeStacks, ListStacks with 130+ resource types (see [supported resource types](./cdk.md#supported-resource-types))
- **IAM** — users, roles, groups, policies, instance profiles (credentials accepted; enforcement is opt-in via `OVERCAST_ENFORCE_IAM`)
- **SigV4 validation** — opt-in via `OVERCAST_SIGV4_VALIDATE=true`
- **CloudWatch** — metrics (`PutMetricData` and friends) and automatic alarm evaluation, alongside Logs
- **Kinesis Data Firehose** — delivery stream CRUD and record ingestion (records are acknowledged but not delivered to destinations)
- **Route 53** — hosted zones, record sets, health checks (metadata with AWS-faithful validation; no DNS queries answered)
- **ElastiCache** — Docker-backed Redis/Valkey/Memcached cache clusters, replication groups, serverless caches

If a feature you need is missing, check `docs/services/<service>.md` for the
detailed support matrix, then open an issue or PR.

---

## Troubleshooting

### "Connection refused" on port 4566

Confirm the container is running and healthy:

```bash
docker compose ps
curl http://localhost:4566/_overcast/health
```

### SDK returns "The specified bucket does not exist" for a bucket I just created

Check you're using path-style addressing (see above). Also confirm the bucket was
created against the same service instance — state is in-memory by default and
does not persist across container restarts.

### Tests pass with LocalStack but fail with overcast

1. Check `docs/services/<service>.md` — the operation may not yet be emulated.
2. Run with `OVERCAST_LOG_LEVEL=debug` to see exactly what request came in and
   what response went out.
3. Use `/_overcast/debug/state` to inspect the stored state.
4. If the operation is listed as ✅ Supported, open an issue with a minimal
   reproduction case.
