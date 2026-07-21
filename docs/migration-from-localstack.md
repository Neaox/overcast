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
    image: ghcr.io/neaox/overcast:latest
    ports: ["4566:4566"]
    environment:
      OVERCAST_SERVICES: s3,sqs,dynamodb
      OVERCAST_LOG_LEVEL: debug
```

---

## Environment variable mapping

| LocalStack        | overcast                                 | Notes                                                             |
| ----------------- | ---------------------------------------- | ----------------------------------------------------------------- |
| `LOCALSTACK_HOST` | `OVERCAST_HOST`                          | Hostname to bind. Default: `0.0.0.0`                              |
| `EDGE_PORT`       | `OVERCAST_PORT`                          | Default: `4566`                                                   |
| `SERVICES`        | `OVERCAST_SERVICES`                      | Comma-separated. Same service names.                              |
| `DATA_DIR`        | `OVERCAST_DATA_DIR`                      | SQLite persistence directory                                      |
| `DEBUG=1`         | `OVERCAST_LOG_LEVEL=debug`               | Verbose logging                                                   |
| `DEFAULT_REGION`  | `OVERCAST_DEFAULT_REGION`                | Default: `us-east-1`                                              |
| `GATEWAY_LISTEN`  | `OVERCAST_HOST:OVERCAST_PORT`            | Split into two variables                                          |
| —                 | `OVERCAST_STATE=persistent`              | Explicit persistence opt-in (LocalStack uses `DATA_DIR` presence) |
| —                 | `OVERCAST_DEBUG=true`                    | Enable `/_debug/*` endpoints                                      |
| —                 | `OVERCAST_TLS_CERT` / `OVERCAST_TLS_KEY` | HTTPS support                                                     |

---

## Endpoint mapping

| LocalStack                       | overcast                  | Notes                          |
| -------------------------------- | ------------------------- | ------------------------------ |
| `/_localstack/health`            | `/_health`                | Always enabled                 |
| `/_localstack/health` (detailed) | `/_debug/health`          | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/init`              | `/_overcast/init`         | Always enabled                 |
| `/_localstack/init/{stage}`      | `/_overcast/init/{stage}` | Always enabled                 |
| `/_localstack/state/reset`       | `/_debug/reset`           | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/info`              | `/_debug/config`          | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/state`             | `/_debug/state`           | Requires `OVERCAST_DEBUG=true` |

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
    image: ghcr.io/neaox/overcast:latest
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

This matches what most local dev setups need. Virtual-hosted style requires DNS
resolution of `*.localhost` which doesn't work without extra configuration.

**Impact:** If your SDK is configured for virtual-hosted style, set:

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

### Lambda: Docker-based execution

Overcast executes Lambda functions inside Docker containers using the official
AWS Lambda base images (`public.ecr.aws/lambda/<runtime>`). This requires Docker
to be available (either via socket mount or TCP). If Docker is not available,
Lambda functions can still be created and managed, but invocations fall back to
a built-in Node.js runtime for simple handlers.

**Impact:** Lambda execution should be compatible with LocalStack Community
Edition. Ensure Docker is accessible to the overcast container (see the
`LAMBDA_DOCKER_SOCKET` and `LAMBDA_NETWORK` configuration variables).

### Persistence: explicit opt-in

LocalStack enables persistence when `DATA_DIR` is set. overcast requires an
explicit `OVERCAST_STATE=persistent` (or `hybrid`, `wal`) in addition to
`OVERCAST_DATA_DIR`.

This makes the intent unambiguous — you can set `OVERCAST_DATA_DIR` for other
purposes without accidentally enabling persistence.

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
curl http://localhost:4566/_health
```

### SDK returns "The specified bucket does not exist" for a bucket I just created

Check you're using path-style addressing (see above). Also confirm the bucket was
created against the same service instance — state is in-memory by default and
does not persist across container restarts.

### Tests pass with LocalStack but fail with overcast

1. Check `docs/services/<service>.md` — the operation may not yet be emulated.
2. Run with `OVERCAST_LOG_LEVEL=debug` to see exactly what request came in and
   what response went out.
3. Use `/_debug/state` to inspect the stored state.
4. If the operation is listed as ✅ Supported, open an issue with a minimal
   reproduction case.
