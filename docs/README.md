# Documentation

This directory contains the full Overcast documentation. For a quick overview,
see the [root README](../README.md).

## Contents

### Getting started

- [Using AWS SDKs and CLI](./sdk-cli.md) — configure the AWS CLI (`--endpoint-url`), Node.js, Python, Go, Java, .NET, Rust, Terraform
- [Using AWS CDK](./cdk.md) — `cdk bootstrap`, `cdk deploy`, supported resource types, troubleshooting
- [Migrating from LocalStack](./migration-from-localstack.md) — drop-in replacement guide

### Reference

- [Service emulation reference](./services/) — per-service endpoint coverage tables
- [Configuration reference](#configuration-reference) — all environment variables
- [Persistence](#persistence) — storage backends
- [HTTPS / TLS](#https--tls) — self-signed certs for local HTTPS
- [Debug endpoints](#debug-endpoints) — health, metrics, state dump, pprof
- [Event pipelines](#event-pipelines) — SNS→SQS, SQS→Lambda, DynamoDB Streams
- [Web management console](#web-management-console) — built-in dashboard

### Development

- [Development setup](./development-setup.md) — building from source
- [Debugging](./debugging.md) — debug endpoints, logging, profiling
- [Performance](./performance.md) — benchmarks and tuning

---

## Support level legend

Every endpoint in the service docs carries one of these statuses:

| Status         | Meaning                                                        |
| -------------- | -------------------------------------------------------------- |
| ✅ Supported   | Fully implemented. AWS SDK calls work as expected.             |
| ⚠️ Partial     | Implemented but with caveats. See the notes column for detail. |
| 🚧 WIP         | Under active development. May be broken or incomplete.         |
| ❌ Unsupported | Not implemented. Returns `501 Not Implemented`.                |

### Service emulation tiers

Each service also has an overall emulation tier, visible on the health
endpoint (`/_health`) and the web dashboard:

| Tier        | Meaning                                                                                                                                                                           |
| ----------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Full**    | P1+P2 operations implemented. Real SDK clients can use it end-to-end.                                                                                                             |
| **Partial** | P1 operations implemented. Basic workflows work.                                                                                                                                  |
| **Inert**   | Full CRUD works — resources are created and stored — but no side-effects or enforcement occur. For example, IAM stores users, roles, and policies but never enforces permissions. |
| **Stub**    | Service is registered but all operations return `501 Not Implemented`.                                                                                                            |

Endpoints marked **Unsupported** return a well-formed AWS error response so
that SDKs surface a clear error rather than a connection failure:

```
HTTP 501 Not Implemented
x-emulator-unsupported: true

{
  "__type": "NotImplemented",
  "message": "This operation is not yet emulated. See https://github.com/Neaox/overcast/docs/services/<service>.md"
}
```

---

## Services

| Service           | Doc                                                 | Overall coverage  |
| ----------------- | --------------------------------------------------- | ----------------- |
| API Gateway       | [apigateway.md](./services/apigateway.md)           | See summary table |
| AppRegistry       | [appregistry.md](./services/appregistry.md)         | See summary table |
| AppSync           | [appsync.md](./services/appsync.md)                 | Config            |
| CloudFormation    | [cloudformation.md](./services/cloudformation.md)   | See summary table |
| CloudFront        | [cloudfront.md](./services/cloudfront.md)           | Stub              |
| CloudWatch Logs   | [cloudwatch-logs.md](./services/cloudwatch-logs.md) | See summary table |
| Cognito           | [cognito.md](./services/cognito.md)                 | Stub              |
| DynamoDB          | [dynamodb.md](./services/dynamodb.md)               | See summary table |
| EC2               | [ec2.md](./services/ec2.md)                         | See summary table |
| ECS               | [ecs.md](./services/ecs.md)                         | See summary table |
| EventBridge       | [eventbridge.md](./services/eventbridge.md)         | Inert             |
| EventBridge Pipes | [pipes.md](./services/pipes.md)                     | See summary table |
| IAM               | [iam.md](./services/iam.md)                         | Inert             |
| Kinesis           | [kinesis.md](./services/kinesis.md)                 | See summary table |
| KMS               | [kms.md](./services/kms.md)                         | See summary table |
| Lambda            | [lambda.md](./services/lambda.md)                   | See summary table |
| RDS               | [rds.md](./services/rds.md)                         | See summary table |
| S3                | [s3.md](./services/s3.md)                           | See summary table |
| Secrets Manager   | [secretsmanager.md](./services/secretsmanager.md)   | See summary table |
| SES               | [ses.md](./services/ses.md)                         | See summary table |
| Shield            | [shield.md](./services/shield.md)                   | Stub              |
| SNS               | [sns.md](./services/sns.md)                         | See summary table |
| SQS               | [sqs.md](./services/sqs.md)                         | See summary table |
| SSM               | [ssm.md](./services/ssm.md)                         | See summary table |
| Step Functions    | [stepfunctions.md](./services/stepfunctions.md)     | Inert             |
| STS               | [sts.md](./services/sts.md)                         | See summary table |
| WAF               | [waf.md](./services/waf.md)                         | Stub              |

---

## Adding a new service

1. Create `docs/services/<service>.md` using the template below.
2. Add the service to the table above.
3. Create `internal/services/<service>/` and register it in the router.
4. Add integration tests under `tests/integration/<service>/`.

### Service doc template

```markdown
# <Service Name>

> AWS docs: https://docs.aws.amazon.com/...

## Summary

| Category | Supported | Partial | WIP | Unsupported |
| -------- | --------- | ------- | --- | ----------- |
| ...      | N         | N       | N   | N           |

## Endpoints

### <Category name>

| Operation | Status      | Notes | AWS Docs    |
| --------- | ----------- | ----- | ----------- |
| ...       | ✅/⚠️/🚧/❌ |       | [link](...) |
```

---

## Configuration reference

All configuration is via environment variables. No config file required.

| Variable                         | Default                | Description                                                                          |
| -------------------------------- | ---------------------- | ------------------------------------------------------------------------------------ |
| `OVERCAST_HOST`                  | `0.0.0.0`              | Hostname or IP to bind to                                                            |
| `OVERCAST_HOSTNAME`              | `localhost`            | Hostname used in client-facing URLs (e.g. SQS queue URLs, SNS unsubscribe links)     |
| `OVERCAST_PORT`                  | `4566`                 | TCP port                                                                             |
| `OVERCAST_SERVICES`              | all                    | Comma-separated list of services to enable, e.g. `s3,sqs,dynamodb`                   |
| `OVERCAST_STATE`                 | `hybrid`               | Storage backend: `memory`, `hybrid` (default), `persistent`, or `wal`                |
| `OVERCAST_STATE_<SERVICE>`       | _(global)_             | Per-service backend override, e.g. `OVERCAST_STATE_S3=memory`                        |
| `OVERCAST_HYBRID_FLUSH_INTERVAL` | `5s`                   | How often the hybrid backend flushes in-memory state to disk                         |
| `OVERCAST_WAL_FSYNC`             | `interval`             | WAL fsync policy: `always`, `interval`, or `never`                                   |
| `OVERCAST_WAL_FSYNC_INTERVAL`    | `100ms`                | Periodic fsync interval used when `OVERCAST_WAL_FSYNC=interval`                      |
| `OVERCAST_WAL_MAX_LOG_BYTES`     | `67108864`             | WAL log compaction threshold in bytes (default 64 MiB)                               |
| `OVERCAST_DATA_DIR`              | `~/.overcast/data`     | Directory for store files and other on-disk state                                    |
| `OVERCAST_DEFAULT_REGION`        | `us-east-1`            | Fallback region used in ARNs when not present in SigV4 header                        |
| `OVERCAST_ACCOUNT_ID`            | `000000000000`         | Account ID embedded in ARNs                                                          |
| `OVERCAST_LOG_LEVEL`             | `info`                 | `debug`, `info`, `warn`, `error`                                                     |
| `OVERCAST_DEBUG`                 | `false`                | Enable `/_debug/*` endpoints                                                         |
| `OVERCAST_SIGV4_VALIDATE`        | `false`                | SigV4 verification _(not yet implemented)_                                           |
| `OVERCAST_TLS_CERT`              | —                      | Path to TLS certificate (enables HTTPS)                                              |
| `OVERCAST_TLS_KEY`               | —                      | Path to TLS private key                                                              |
| `OVERCAST_SHUTDOWN_TIMEOUT`      | `5s`                   | Graceful shutdown wait                                                               |
| `LAMBDA_DOCKER_SOCKET`           | `/var/run/docker.sock` | Docker endpoint — Unix path or `tcp://host:port` (for DinD)                          |
| `LAMBDA_NETWORK`                 | `overcast_lambda`      | Docker network for Lambda containers                                                 |
| `LAMBDA_RUNTIME_API_PORT`        | `9001`                 | Port Overcast exposes the Lambda Runtime API on                                      |
| `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` | `4`                    | Max concurrent Docker-backed Lambda container starts                                 |
| `LAMBDA_INIT_TIMEOUT_SECONDS`    | `10`                   | Max seconds to wait for a Lambda runtime to finish INIT                              |
| `LAMBDA_KEEP_CONTAINERS`         | `false`                | Keep stopped Lambda containers after expiry/delete (useful for debugging)            |
| `ECS_DOCKER_SOCKET`              | _(Lambda socket)_      | Docker endpoint for ECS — Unix path or `tcp://host:port`                             |
| `ECS_NETWORK`                    | `overcast_ecs`         | Docker network for ECS task containers                                               |
| `ECS_KEEP_CONTAINERS`            | `false`                | Keep stopped ECS task containers after they exit                                     |
| `RDS_DOCKER_SOCKET`              | _(Lambda socket)_      | Docker endpoint for RDS — Unix path or `tcp://host:port`                             |
| `RDS_NETWORK`                    | `overcast_rds`         | Docker network for RDS database containers                                           |
| `RDS_PORT_BASE`                  | `33060`                | Starting host port for RDS containers (each instance gets the next available port)   |
| `RDS_KEEP_CONTAINERS`            | `false`                | Keep stopped RDS containers after instance deletion                                  |
| `OVERCAST_SMTP_MOCK`             | `true`                 | Enable built-in SMTP capture server (auto-disabled when `OVERCAST_SMTP_HOST` is set) |
| `OVERCAST_SMTP_PORT`             | `1025`                 | Port for the mock SMTP server                                                        |
| `OVERCAST_SMTP_HOST`             | —                      | External SMTP relay hostname (disables the mock server)                              |
| `OVERCAST_SMTP_FROM`             | `overcast@localhost`   | Envelope From address for outbound SNS email notifications                           |
| `OVERCAST_SMTP_USERNAME`         | —                      | SMTP AUTH PLAIN username for external relay                                          |
| `OVERCAST_SMTP_PASSWORD`         | —                      | SMTP AUTH PLAIN password for external relay                                          |
| `OVERCAST_SMTP_TLS`              | `false`                | Enable implicit TLS (port 465) for external relay                                    |
| `OVERCAST_SMTP_INBOX_MAX`        | `500`                  | Maximum number of captured messages retained before eviction                         |

---

## Persistence

Overcast supports four storage backends, set via `OVERCAST_STATE`:

| Backend      | Description                                                                             |
| ------------ | --------------------------------------------------------------------------------------- |
| `memory`     | All state in-process; lost on restart. Fastest — ideal for CI.                          |
| `hybrid`     | **Default.** Reads from memory, flushes to SQLite asynchronously. Fast with durability. |
| `persistent` | Every mutation written synchronously to SQLite. Fully durable, slightly slower.         |
| `wal`        | In-memory reads + append-log durability with replay on startup and periodic compaction. |

For state that persists across restarts (recommended: `hybrid`):

```bash
docker run --rm \
  -p 4566:4566 \
  -e OVERCAST_STATE=hybrid \
  -e OVERCAST_DATA_DIR=/data \
  -v $(pwd)/overcast-data:/data \
  ghcr.io/neaox/overcast:latest
```

Persistent/hybrid SQLite data lives at `$OVERCAST_DATA_DIR/overcast.db`. WAL mode uses `$OVERCAST_DATA_DIR/overcast.wal`. You can also override the backend per-service:

```bash
-e OVERCAST_STATE=memory -e OVERCAST_STATE_S3=hybrid
```

### Per-service storage overrides

Each service can use a different backend. Set `OVERCAST_STATE_<SERVICE>`
where `<SERVICE>` is the uppercase service name (hyphens become underscores):

```bash
docker run --rm -p 4566:4566 \
  -e OVERCAST_STATE=memory \
  -e OVERCAST_STATE_DYNAMODB=persistent \
  -e OVERCAST_STATE_S3=hybrid \
  -v $(pwd)/data:/data \
  ghcr.io/neaox/overcast:latest
```

In this example DynamoDB writes synchronously to disk, S3 flushes
asynchronously, and every other service uses in-memory (ephemeral)
storage. Each overridden service gets its own SQLite file under
`$OVERCAST_DATA_DIR/<service>/`.

The active storage configuration is visible in two places:

- **`GET /_health`** — the `storage` object shows the default backend and any per-service overrides.
- **Dashboard footer** — the web management console displays the storage mode with a tooltip listing overrides.

---

## HTTPS / TLS

```bash
# Generate a self-signed cert for local development
openssl req -x509 -newkey rsa:4096 \
  -keyout key.pem -out cert.pem \
  -days 365 -nodes -subj '/CN=localhost'

docker run --rm \
  -p 4566:4566 \
  -e OVERCAST_TLS_CERT=/certs/cert.pem \
  -e OVERCAST_TLS_KEY=/certs/key.pem \
  -v $(pwd):/certs \
  ghcr.io/neaox/overcast:latest
```

```bash
export AWS_CA_BUNDLE=/path/to/cert.pem       # AWS CLI + boto3
export NODE_EXTRA_CA_CERTS=/path/to/cert.pem # Node.js SDK
```

---

## Multi-container networking

When running Overcast inside Docker Compose alongside application containers,
client-facing URLs (e.g. SQS queue URLs, SNS unsubscribe links, RDS endpoints)
default to `localhost` — which won't resolve from a sibling container.

Set `OVERCAST_HOSTNAME` to the Docker Compose service name so returned URLs
are reachable across the network:

```yaml
services:
  overcast:
    image: ghcr.io/neaox/overcast:latest
    environment:
      OVERCAST_HOSTNAME: overcast # SQS QueueUrl → http://overcast:4566/...
    ports:
      - "4566:4566"

  app:
    build: .
    environment:
      AWS_ENDPOINT_URL: http://overcast:4566
    depends_on:
      - overcast
```

---

## Debug endpoints

Set `OVERCAST_DEBUG=true` to enable the `/_debug` namespace:

| Endpoint                    | Method | Description                                           |
| --------------------------- | ------ | ----------------------------------------------------- |
| `/_health`                  | GET    | Basic health check (always enabled)                   |
| `/_events`                  | GET    | SSE stream of internal events (always enabled)        |
| `/_metrics`                 | GET    | Go runtime memory/GC/goroutine stats (always enabled) |
| `/_topology`                | GET    | Full cross-region resource graph (always enabled)     |
| `/_debug/health`            | GET    | Detailed: uptime, services, state backend             |
| `/_debug/config`            | GET    | Effective configuration (secrets redacted)            |
| `/_debug/state`             | GET    | Full state dump across all namespaces                 |
| `/_debug/state/{namespace}` | GET    | State for one namespace, e.g. `s3:buckets`            |
| `/_debug/reset`             | POST   | Wipe all state                                        |
| `/_debug/reset/{service}`   | POST   | Wipe state for one service                            |
| `/_debug/metrics`           | GET    | Request counts and latencies                          |
| `/_debug/pprof/`            | GET    | Go pprof index (goroutine, heap, CPU profiles, etc.)  |

---

## Event pipelines

| Pipeline                          | Status       |
| --------------------------------- | ------------ |
| SNS → SQS subscription            | ✅ Supported |
| SQS → Lambda event source mapping | ✅ Supported |
| DynamoDB Streams → SQS (Pipes)    | ✅ Supported |
| DynamoDB Streams → Lambda (ESM)   | ✅ Supported |

---

## Web management console

The full image (`ghcr.io/neaox/overcast`) includes a web management console
accessible at **http://localhost:8080** (configurable via `WEB_PORT` env var
inside the container).

The console provides:

- Dashboard with service cards and real-time status
- Service-specific UI for all implemented services (S3 browser, SQS message inspector, DynamoDB item editor, Lambda test/invoke, etc.)
- **Live activity feed** — a real-time stream of API calls as they happen across all services, showing the operation, resource, status code, and latency. Useful for understanding what your application is actually doing against the emulated APIs.
- **Inbox** — a built-in capture inbox for all outbound email and SMS messages generated by SES, SNS, and Cognito. Instead of messages disappearing into the void (or requiring a real SMTP server), the Inbox collects them and lets you browse, search, and inspect each message's headers and body. This makes it easy to verify that your application sends the right emails during local development and testing — no third-party mail catcher needed.
- Topology map showing cross-service relationships
- Real-time updates via SSE

The web UI is non-critical — if the BFF server fails to start, the Go backend
runs normally without it.
