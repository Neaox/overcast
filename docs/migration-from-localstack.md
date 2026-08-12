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
| `LOCALSTACK_HOST` | `OVERCAST_HOST`                          | Hostname to bind, or a comma-separated list. Default: `0.0.0.0`   |
| `EDGE_PORT`       | `OVERCAST_PORT`                          | Default: `4566`                                                   |
| `SERVICES`        | — *(no equivalent)*                      | Overcast runs every service, always. Drop the variable; a leftover value is ignored rather than rejected |
| `DATA_DIR`        | `OVERCAST_DATA_DIR`                      | SQLite persistence directory                                      |
| `DEBUG=1`         | `OVERCAST_LOG_LEVEL=debug`               | Verbose logging                                                   |
| `DEFAULT_REGION`  | `OVERCAST_DEFAULT_REGION`                | Default: `us-east-1`                                              |
| `GATEWAY_LISTEN`  | `OVERCAST_HOST:OVERCAST_PORT`            | Split into two variables                                          |
| —                 | `OVERCAST_STATE`                         | Explicit backend override; unset defaults to `auto`, which — like LocalStack's `DATA_DIR` presence — resolves to persistent (`hybrid`) when a volume/data dir is present, `memory` otherwise. **Not in the `overcast-slim` image or the `overcastd` binaries:** they exclude SQLite, so `auto` there is always `memory` and durability needs `OVERCAST_STATE=wal` — see [storage.md § Builds without SQLite](./storage.md#builds-without-sqlite) |
| —                 | `OVERCAST_DEBUG=true`                    | Enable `/_overcast/debug/*` endpoints                                      |
| —                 | `OVERCAST_TLS_CERT` / `OVERCAST_TLS_KEY` | HTTPS support                                                     |

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

## Known gaps (features LocalStack has that overcast doesn't yet)

| Feature               | overcast status | Notes                               |
| --------------------- | --------------- | ----------------------------------- |
| SigV4 validation      | TODO            | Accepted but not validated          |
| CloudWatch Metrics    | Not implemented | Logs are supported; metrics are not |
| Kinesis Data Firehose | Not implemented |                                     |
| Route 53              | Not implemented |                                     |
| ElastiCache           | Not implemented |                                     |

The following features that were previously missing are now implemented:

- **Lambda execution** — full Docker-based container execution
- **DynamoDB Streams** — ListStreams, DescribeStream, GetShardIterator, GetRecords
- **DynamoDB transactions** — TransactWriteItems, TransactGetItems
- **DynamoDB GSI** — Global Secondary Indexes supported
- **S3 multipart upload** — CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload, ListParts
- **S3 versioning** — PutBucketVersioning, GetBucketVersioning, ListObjectVersions
- **SNS → SQS fan-out** — working
- **SQS → Lambda ESM** — event source mapping with CRUD and polling delivery
- **CloudFormation** — CreateStack, UpdateStack, DeleteStack, DescribeStacks, ListStacks with ~50 resource types
- **IAM** — users, roles, groups, policies, instance profiles (credentials accepted but not enforced)

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
