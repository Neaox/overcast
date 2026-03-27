# Overcast

**A fast, free, open-source local cloud service emulator.**

Overcast emulates the APIs of popular cloud services so you can develop and test
locally without an internet connection, a cloud account, or a bill.

[![Tests](https://github.com/your-org/overcast/actions/workflows/test.yml/badge.svg)](https://github.com/your-org/overcast/actions)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go 1.23+](https://img.shields.io/badge/Go-1.23+-blue.svg)](https://go.dev)

---

## Project goals

1. **Works with the official AWS CLI** — `aws s3 mb s3://my-bucket --endpoint-url http://localhost:4566` just works.
2. **Works with all official AWS SDK clients** — Go, JavaScript/TypeScript, Python, Java, .NET without code changes.
3. **Drop-in replacement for LocalStack** — same port (4566), same env vars mapped, same path conventions. Switching requires changing one line.
4. **Zero configuration** — `docker run -p 4566:4566 ghcr.io/your-org/overcast` is the full getting-started guide.
5. **Fast** — sub-200ms startup, <15 MiB idle memory, tiny Docker image. CI pipelines should not wait for the emulator.
6. **Honest about gaps** — unimplemented endpoints return `501 Not Implemented` with a clear message and a link to the support matrix. Silent failures are worse than loud ones.
7. **Fully open** — MIT licensed, no auth tokens, no telemetry, no usage limits, no feature gates. Free forever for every use case including CI/CD.
8. **Production-quality internals** — race-safe, well-tested, well-documented, easy to contribute to.

---

---

## What Overcast is NOT

Overcast is a development and testing tool. Please don't use it for:

| Not for | Why |
|---------|-----|
| **Staging environments** | API parity is not 100%. Differences are documented but exist. |
| **Production traffic** | Overcast is not hardened, not monitored, not replicated. |
| **Self-hosted AWS replacement** | This is not a platform you host for others. It has no security model, no IAM, and no durability guarantees. Running it as a persistent internal service is building on quicksand. |
| **Security testing** | Credentials are accepted but not validated in v1. |
| **Performance / load testing** | AWS throttling, quotas, and latency are not emulated. |
| **IAM policy testing** | IAM is out of scope. All operations are permitted. |
| **CloudFormation / CDK deploys** | CloudFormation is out of scope for v1. |

## Contents

- [Quick start](#quick-start)
- [Running with Docker](#running-with-docker)
- [Running locally (Go)](#running-locally-go)
- [AWS CLI compatibility](#aws-cli-compatibility)
- [Migrating from LocalStack](#migrating-from-localstack)
- [Supported services](#supported-services)
- [Configuration reference](#configuration-reference)
- [Persistence](#persistence)
- [HTTPS / TLS](#https--tls)
- [Debug endpoints](#debug-endpoints)
- [CDK compatibility](#cdk-compatibility)
- [Event pipelines](#event-pipelines)
- [Contributing](#contributing)

---

## Quick start

```bash
docker run --rm -p 4566:4566 ghcr.io/your-org/overcast:latest
```

Point any AWS SDK or the AWS CLI at it:

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

# AWS CLI
aws s3 mb s3://my-bucket
aws sqs create-queue --queue-name my-queue
aws dynamodb list-tables

# No other changes needed — use the SDK exactly as you would against real AWS.
```

---

## Running with Docker

### docker run

```bash
docker run --rm \
  -p 4566:4566 \
  -e OVERCAST_SERVICES=s3,sqs,dynamodb \
  -e OVERCAST_LOG_LEVEL=debug \
  ghcr.io/your-org/overcast:latest
```

### docker compose (recommended for local dev)

```yaml
# docker-compose.yml
services:
  overcast:
    image: ghcr.io/your-org/overcast:latest
    ports:
      - "4566:4566"
    environment:
      OVERCAST_STATE: memory          # or 'sqlite' for persistence across restarts
      OVERCAST_LOG_LEVEL: debug
      OVERCAST_SERVICES: s3,sqs,dynamodb,sns,lambda
    volumes:
      - overcast-data:/data           # only needed when STATE=sqlite
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:4566/_health"]
      interval: 5s
      timeout: 3s
      retries: 5

volumes:
  overcast-data:
```

```bash
docker compose up
```

---

## Running locally (Go)

```bash
# Prerequisites: Go 1.23+ (https://go.dev/dl/)

git clone https://github.com/your-org/overcast
cd overcast
go mod tidy
make run          # starts on :4566 with in-memory state
```

Run the tests:

```bash
make test              # all tests with race detector
make test-unit         # fast unit tests only
make test-integration  # integration tests only
make test-coverage     # HTML coverage report → coverage.html
```

---

## AWS CLI compatibility

Overcast is tested against the AWS CLI v2. Configure it with:

```bash
# Option 1: environment variables (recommended for CI)
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

# Option 2: AWS CLI profile (~/.aws/config)
[profile overcast]
aws_access_key_id = test
aws_secret_access_key = test
region = us-east-1
endpoint_url = http://localhost:4566

# Then: aws --profile overcast s3 ls
```

For S3, use path-style addressing (default in Overcast):

```bash
aws --endpoint-url http://localhost:4566 \
    --no-verify-ssl \
    s3 mb s3://my-bucket
```

---

## Migrating from LocalStack

Overcast is designed as a drop-in replacement. In most cases changing one env var is all you need.

| LocalStack | Overcast | Notes |
|------------|----------|-------|
| `LOCALSTACK_HOST` | `OVERCAST_HOST` | Bind address, default `0.0.0.0` |
| `EDGE_PORT` | `OVERCAST_PORT` | Default `4566` |
| `DATA_DIR` | `OVERCAST_DATA_DIR` | Persistence directory |
| `SERVICES` | `OVERCAST_SERVICES` | Same service names |
| `DEBUG=1` | `OVERCAST_LOG_LEVEL=debug` | Verbose logging |
| `/_localstack/health` | `/_health` | Always enabled |
| `/_localstack/state/reset` | `/_debug/reset` | Requires `OVERCAST_DEBUG=true` |

See [docs/migration-from-localstack.md](./docs/migration-from-localstack.md) for the full guide.

---

## Supported services

| Service | Coverage | Docs |
|---------|----------|------|
| S3 | P1 + P2 complete | [s3.md](./docs/services/s3.md) |
| SQS | P1 + P2 complete | [sqs.md](./docs/services/sqs.md) |
| DynamoDB | In progress | [dynamodb.md](./docs/services/dynamodb.md) |
| SNS | Stub (P2 next) | [sns.md](./docs/services/sns.md) |
| Lambda | Stub (Node.js, P2 next) | [lambda.md](./docs/services/lambda.md) |

Status key: ✅ Supported · ⚠️ Partial · 🚧 WIP · ❌ Unsupported

---

## Configuration reference

All configuration is via environment variables. No config file required.

| Variable | Default | Description |
|----------|---------|-------------|
| `OVERCAST_HOST` | `0.0.0.0` | Hostname or IP to bind to |
| `OVERCAST_PORT` | `4566` | TCP port |
| `OVERCAST_SERVICES` | all | Comma-separated: `s3,sqs,dynamodb,sns,lambda` |
| `OVERCAST_STATE` | `memory` | `memory` (default) or `sqlite` |
| `OVERCAST_DATA_DIR` | `~/.overcast/data` | SQLite file and persistence directory |
| `OVERCAST_REGION` | `us-east-1` | Region in ARNs and responses |
| `OVERCAST_ACCOUNT_ID` | `000000000000` | Account ID in ARNs |
| `OVERCAST_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `OVERCAST_DEBUG` | `false` | Enable `/_debug/*` endpoints |
| `OVERCAST_SIGV4_VALIDATE` | `false` | SigV4 verification *(not yet implemented)* |
| `OVERCAST_TLS_CERT` | — | Path to TLS certificate (enables HTTPS) |
| `OVERCAST_TLS_KEY` | — | Path to TLS private key |
| `OVERCAST_SHUTDOWN_TIMEOUT` | `30s` | Graceful shutdown wait |
| `OVERCAST_LAMBDA_NODE_BIN` | `node` | Node.js binary for Lambda execution |

---

## Persistence

By default all state is in-memory and lost on restart — ideal for CI.

For state that persists across restarts:

```bash
docker run --rm \
  -p 4566:4566 \
  -e OVERCAST_STATE=sqlite \
  -e OVERCAST_DATA_DIR=/data \
  -v $(pwd)/overcast-data:/data \
  ghcr.io/your-org/overcast:latest
```

The database is a standard SQLite file at `$OVERCAST_DATA_DIR/overcast.db`.

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
  ghcr.io/your-org/overcast:latest
```

```bash
export AWS_CA_BUNDLE=/path/to/cert.pem       # AWS CLI + boto3
export NODE_EXTRA_CA_CERTS=/path/to/cert.pem # Node.js SDK
```

---

## Debug endpoints

Set `OVERCAST_DEBUG=true` to enable the `/_debug` namespace:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/_health` | GET | Basic health check (always enabled) |
| `/_debug/health` | GET | Detailed: uptime, services, state backend |
| `/_debug/config` | GET | Effective configuration (secrets redacted) |
| `/_debug/state` | GET | Full state dump across all namespaces |
| `/_debug/state/{namespace}` | GET | State for one namespace, e.g. `s3:buckets` |
| `/_debug/reset` | POST | Wipe all state |
| `/_debug/reset/{service}` | POST | Wipe state for one service |
| `/_debug/metrics` | GET | Request counts and latencies |

> A web UI for these endpoints is planned — contributions welcome.

---

## CDK compatibility

| CDK operation | Status | Notes |
|---------------|--------|-------|
| S3 operations (bootstrap) | ✅ | Standard S3 emulation |
| SSM parameter reads | ❌ | Not yet emulated |
| CloudFormation | ❌ | Out of scope for v1 |

---

## Event pipelines

| Pipeline | Status |
|----------|--------|
| SNS → SQS subscription | 🚧 In progress |
| SQS → Lambda event source mapping | 🚧 In progress |
| DynamoDB Streams → SQS | 🚧 Planned |
| DynamoDB Streams → Lambda | 🚧 Planned |

---

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for setup, workflow, and conventions.

AI agents: read [AGENTS.md](./AGENTS.md) first.
