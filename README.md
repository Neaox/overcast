# Overcast

**A fast, free, open-source local cloud service emulator.**

Overcast emulates the APIs of popular cloud services so you can develop and test
locally without an internet connection, a cloud account, or a bill.

[![CI](https://github.com/Neaox/overcast/actions/workflows/test.yml/badge.svg)](https://github.com/Neaox/overcast/actions)
[![GitHub release](https://img.shields.io/github/v/release/Neaox/overcast?include_prereleases)](https://github.com/Neaox/overcast/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Container image](https://img.shields.io/badge/ghcr.io-neaox%2Fovercast-blue?logo=docker&logoColor=white)](https://github.com/Neaox/overcast/pkgs/container/overcast)

Every change is tested against **eight official AWS clients** — the AWS CLI,
the CDK, and the Go, JavaScript, Python, Java, .NET, and Rust SDKs — via the
[compatibility suite](./docs/dev/compatibility/).

---

## Project goals

1. **Works with the official AWS CLI** — `aws s3 mb s3://my-bucket --endpoint-url http://localhost:4566` just works.
2. **Works with all official AWS SDK clients** — Go, JavaScript/TypeScript, Python, Java, .NET without code changes.
3. **Drop-in replacement for LocalStack** — same port (4566), same env vars mapped, same path conventions. Switching requires changing one line.
4. **Zero configuration** — `docker run -p 4566:4566 ghcr.io/neaox/overcast:alpha` is the full getting-started guide.
5. **Fast** — sub-200ms startup, <15 MiB idle memory, tiny Docker image. CI pipelines should not wait for the emulator.
6. **Honest about gaps** — unimplemented endpoints return `501 Not Implemented` with a clear message and a link to the support matrix. Silent failures are worse than loud ones.
7. **Fully open** — MIT licensed, no auth tokens, no telemetry, no usage limits, no feature gates. Free forever for every use case including CI/CD.
8. **Production-quality internals** — race-safe, well-tested, well-documented, easy to contribute to.

---

> [!CAUTION]
> Overcast is a local development and CI tool only. Never expose it on a public network,
> use it as a staging environment, or make production go/no-go decisions based on its behavior.
> Details: [What Overcast is NOT](#what-overcast-is-not).

## Contents

- [Overcast](#overcast)
  - [Project goals](#project-goals)
  - [Contents](#contents)
  - [Quick start](#quick-start)
  - [What Overcast is NOT](#what-overcast-is-not)
  - [Running with Docker](#running-with-docker)
    - [docker run](#docker-run)
    - [docker compose (recommended for local dev)](#docker-compose-recommended-for-local-dev)
  - [Native binaries](#native-binaries)
    - [Binary variants](#binary-variants)
    - [Installation](#installation)
    - [Commands](#commands)
    - [overcast serve](#overcast-serve)
    - [overcast bridge](#overcast-bridge)
    - [overcast status](#overcast-status)
    - [overcast trust](#overcast-trust)
    - [Platform notes](#platform-notes)
      - [macOS](#macos)
      - [Linux](#linux)
      - [Windows](#windows)
  - [Supported services](#supported-services)
  - [Documentation](#documentation)
  - [Contributing](#contributing)

---

## Quick start

Two images are published to GHCR:

| Image                         | Description                                                | Size   |
| ----------------------------- | ---------------------------------------------------------- | ------ |
| `ghcr.io/neaox/overcast`      | Full image with web management console (ports 4566 + 4567) | ~50 MB |
| `ghcr.io/neaox/overcast-slim` | Headless — Go binary only, no UI (port 4566)               | ~20 MB |

Overcast is pre-1.0, so every build publishes to the `:alpha` channel tag and to
an exact version tag such as `:0.0.1-alpha.25`. There is no `:latest` tag yet —
it starts publishing with the first stable release. Pin the exact version in CI;
use `:alpha` to track the newest build.

```bash
# Full image (with web UI on :4567)
docker run --rm -p 4566:4566 -p 4567:4567 ghcr.io/neaox/overcast:alpha

# Slim image (CI pipelines, no UI)
docker run --rm -p 4566:4566 ghcr.io/neaox/overcast-slim:alpha
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

## What Overcast is NOT

| Not for                          | Why                                                                                                                                                                               |
| -------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Staging environments**         | API parity is not 100%. Differences are documented but exist.                                                                                                                     |
| **Production traffic**           | Overcast is not hardened, not monitored, not replicated.                                                                                                                          |
| **Self-hosted AWS replacement**  | This is not a platform you host for others. IAM resources are emulated, but Overcast is not a security boundary and has no durability guarantees. Running it as a persistent internal service is building on quicksand. |
| **Security testing**             | Credentials are accepted. SigV4 validation is optional, and IAM policies are not enforced as an authorization layer.                                                               |
| **Performance / load testing**   | AWS throttling, quotas, and latency are not emulated.                                                                                                                             |
| **IAM policy testing**           | IAM resource APIs exist for local development and IaC compatibility, but policy enforcement is out of scope. All operations are permitted.                                         |
| **CloudFormation / CDK deploys** | CloudFormation emulation supports ~50 resource types. `cdk deploy` works for stacks using [supported types](./docs/cdk.md#supported-resource-types). Coverage is not exhaustive.  |

## Running with Docker

### docker run

```bash
# Full image with web console
docker run --rm \
  -p 4566:4566 \
  -p 4567:4567 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e OVERCAST_LOG_LEVEL=debug \
  ghcr.io/neaox/overcast:alpha

# With persistent data (survives container restarts) — mounting a volume at
# /data is enough; OVERCAST_STATE defaults to "auto", which resolves to
# hybrid automatically whenever a volume or bind mount is present there.
docker run --rm \
  -p 4566:4566 \
  -p 4567:4567 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v ~/.overcast:/data \
  ghcr.io/neaox/overcast:alpha

# Slim image (no web UI) — no Docker socket needed when only using
# non-container services (S3, SQS, DynamoDB, SNS, etc.)
docker run --rm \
  -p 4566:4566 \
  ghcr.io/neaox/overcast-slim:alpha
```

### docker compose (recommended for local dev)

```yaml
# docker-compose.yml
services:
  overcast:
    image: ghcr.io/neaox/overcast:alpha
    ports:
      - "4566:4566"
      - "4567:4567"
    environment:
      # OVERCAST_STATE is left unset: mounting overcast-data below at /data
      # makes auto resolve to hybrid automatically. Set OVERCAST_STATE
      # explicitly (memory | hybrid | persistent | wal) to override.
      OVERCAST_LOG_LEVEL: debug
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock # required for Lambda, ECS, RDS, EC2
      - overcast-data:/data # mounting this is what makes auto resolve to hybrid
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

> [!NOTE]
> **Docker socket and container-based services**
>
> Lambda, ECS, RDS, and EC2 launch sibling containers on the host's Docker
> daemon. This requires bind-mounting the Docker socket (`/var/run/docker.sock`).
> If the socket is not mounted, these services degrade gracefully — metadata
> operations (create, describe, list, delete) still work, but Lambda invocations
> return mock responses and ECS/RDS containers won't start.
>
> Services that **don't** need the Docker socket (S3, SQS, DynamoDB, SNS,
> CloudWatch Logs, SES, Secrets Manager, KMS, SSM, STS, IAM, etc.) work without it.
>
> **CI environments** where socket mounting is restricted can use a
> [Docker-in-Docker (DinD) sidecar](https://hub.docker.com/_/docker) instead.
> Set `LAMBDA_DOCKER_SOCKET` (and optionally `ECS_DOCKER_SOCKET` / `RDS_DOCKER_SOCKET`)
> to a `tcp://` endpoint:
>
> ```yaml
> services:
>   dind:
>     image: docker:dind
>     privileged: true
>     environment:
>       DOCKER_TLS_CERTDIR: "" # disable TLS for simplicity
>   overcast:
>     image: ghcr.io/neaox/overcast:alpha
>     ports:
>       - "4566:4566"
>     environment:
>       LAMBDA_DOCKER_SOCKET: tcp://dind:2375
>     depends_on:
>       - dind
> ```

---

## Native binaries

Download pre-built binaries from the [GitHub releases page](https://github.com/Neaox/overcast/releases).
No runtime dependencies — a single static binary is all you need.

### Binary variants

Two binaries are published for every release:

| Binary      | Platforms                                           | Description                                                                        |
| ----------- | --------------------------------------------------- | ---------------------------------------------------------------------------------- |
| `overcast`  | Linux amd64/arm64, macOS amd64/arm64, Windows amd64 | Full binary — emulator + embedded web console + Go BFF. All subcommands available. |
| `overcastd` | Linux amd64/arm64, macOS amd64/arm64, Windows amd64 | Slim binary — emulator only, no web UI. Smaller footprint for CI and servers.      |

Both binaries share the same `overcast serve` entrypoint and respond identically to AWS SDK clients. The only difference is that `overcastd` returns `404` for web console requests.

### Installation

**macOS / Linux — manual:**

```bash
# Replace VERSION and PLATFORM (linux-amd64, linux-arm64, darwin-amd64, darwin-arm64)
curl -L https://github.com/Neaox/overcast/releases/latest/download/overcast-linux-amd64 \
  -o /usr/local/bin/overcast
chmod +x /usr/local/bin/overcast
```

**Windows — manual:**

Download `overcast-windows-amd64.exe` from the releases page and place it anywhere on your `PATH`.

**Build from source:**

```bash
git clone https://github.com/Neaox/overcast.git && cd overcast
# Full binary (builds web UI first)
cd web && pnpm install --frozen-lockfile && pnpm run build && cd ..
go build -trimpath -o overcast ./cmd/overcast

# Slim binary (no Node.js needed)
go build -trimpath -tags slim -o overcastd ./cmd/overcast
```

### Commands

All subcommands are available in both `overcast` and `overcastd` (the web UI is simply absent in the slim binary). Run `overcast --help` or `overcast <command> --help` for the full flag reference.

| Command           | Description                                                             |
| ----------------- | ----------------------------------------------------------------------- |
| `overcast serve`  | Start the AWS service emulator                                          |
| `overcast bridge` | Publish `.local` domains via mDNS and start a port-80 reverse proxy     |
| `overcast status` | Inspect a running daemon (version, uptime, state backend, service list) |
| `overcast trust`  | Manage the local trust store for self-signed TLS certificates           |

### overcast serve

Starts the emulator on port 4566 (configurable). All configuration is via environment variables.

```bash
overcast serve

# Common overrides
OVERCAST_PORT=4566 \
OVERCAST_STATE=hybrid \
OVERCAST_LOG_LEVEL=debug \
  overcast serve
```

**Key flags / env vars:**

| Flag / Env var                   | Default     | Description                                                                                  |
| -------------------------------- | ----------- | -------------------------------------------------------------------------------------------- |
| `--ui-port` / `OVERCAST_UI_PORT` | `4567`      | Web console port. `0` disables the UI. Falls back to a free ephemeral port if 4567 is taken. |
| `--bridge` / —                   | off         | Also run the mDNS bridge and port-80 proxy (see `overcast bridge`).                          |
| `--bridge-bind-ip`               | `127.0.0.1` | IP advertised in mDNS when `--bridge` is set.                                                |
| `OVERCAST_PORT`                  | `4566`      | AWS API port.                                                                                |
| `OVERCAST_HOST`                  | `0.0.0.0`   | Interface to bind. Comma-separate to bind several, e.g. `127.0.0.1,172.17.0.1`.               |
| `OVERCAST_STATE`                 | `auto`      | State backend: `auto` (default — resolves to `hybrid` or `memory`, see [storage.md](./docs/storage.md#the-auto-default)), `memory`, `hybrid`, `persistent`, `wal`. |

See the [configuration reference](./docs/README.md#configuration-reference) for the full list.

The web console (`overcast` full binary only) is served on port 4567 and loads lazily on first request — no warm-up needed. Point a browser at `http://localhost:4567` after starting the server.

### overcast bridge

Connects to a running `overcast serve` instance and:

- Publishes `overcast.local` (emulator API) and `overcast-app.local` (web console) on the host mDNS responder so you can reach them from any browser or tool without editing `/etc/hosts`.
- Watches the emulator's domain registry and advertises every registered API Gateway custom domain on the same responder.
- Starts an HTTP reverse proxy on port 80 that routes requests by `Host` header — no port number needed when accessing via `.local` names.

> [!NOTE]
> **Port 80 conflicts.** Port 80 is commonly held by local web servers (nginx, Apache, IIS) or
> requires elevated privileges to bind. If the port is busy or the bind fails, `overcast bridge`
> logs a warning with platform-specific instructions and continues — mDNS still works, you just
> need the port number in the URL (e.g. `http://overcast.local:4566`).
>
> To avoid the conflict entirely, use `--http-port 0` (mDNS-only, no proxy) or pick a free
> high port with `--http-port 8080`. See [Platform notes](#platform-notes) for privilege setup.

```bash
# In a second terminal, while overcast serve is running
overcast bridge

# Point to a non-default instance
overcast bridge --endpoint http://localhost:4566

# Custom bind IP (if your machine has multiple interfaces)
overcast bridge --bind-ip 192.168.1.100

# mDNS only — no port-80 proxy (.local names resolve but need a port in the URL)
overcast bridge --http-port 0

# Use a non-privileged port (http://overcast.local:8080 etc.)
overcast bridge --http-port 8080

# Run bridge inline with the server (--bridge flag on serve)
overcast serve --bridge
```

After `overcast bridge` is running:

| URL                         | Routed to                            |
| --------------------------- | ------------------------------------ |
| `http://overcast.local`     | Emulator API (port 4566)             |
| `http://overcast-app.local` | Web console (port 4567)              |
| `http://api.myapp.local`    | Emulator (API Gateway custom domain) |

### overcast status

Prints the current state of a running daemon — version, uptime, active state backend, enabled services, and listener address.

```bash
overcast status
overcast status --endpoint http://localhost:4566
```

### overcast https

One-shot HTTPS setup: creates the local overcast CA if missing, installs it
into the system trust store (approve the OS prompt — that's the only manual
step), and mints the server certificate. Serving both the API and the web UI
over TLS unlocks browser HTTP/2, which keeps the web console responsive under
load. See [docs/https.md](./docs/https.md).

```bash
overcast https enable            # once per machine
OVERCAST_TLS=auto overcast serve # HTTPS + HTTP/2 on both listeners
# → https://localhost.overcast.sh:4567

overcast https status            # report the setup state
overcast https disable           # remove the CA from the trust store
```

Daemon running in Docker? Trust it without a shared volume — the daemon
serves its CA certificate at `/_overcast/ca.pem` and the CLI fetches it:

```bash
overcast https enable --endpoint http://localhost:4566
```

(Loopback endpoints only, unless you acknowledge the trust decision with
`--trust-remote`. The same `--endpoint` works on `status`/`disable` and on
the `trust` subcommands.)

### overcast trust

Lower-level management of the overcast CA in the system trust store (the
`https` subcommands build on it). Useful with `OVERCAST_TLS=auto`, or when
scripting the pieces separately.

```bash
# Install the CA certificate into the system trust store
overcast trust install

# Report whether it is installed
overcast trust status

# Remove it (the CA key material on disk is kept)
overcast trust uninstall
```

> [!NOTE]
> On Windows the CA goes into the current user's certificate store (a
> confirmation dialog appears); on macOS into the login keychain (an
> authorisation prompt appears); on Linux into the system CA bundle, which
> requires root (`sudo overcast trust install`). Firefox/Chromium on Linux
> read their own NSS store — see [docs/https.md](./docs/https.md).

### Platform notes

#### macOS

- All four subcommands work out of the box.
- `overcast bridge` uses the built-in `dns-sd` tool (part of Bonjour). No additional software needed.
- Binding the port-80 proxy requires `sudo`:
  ```bash
  sudo overcast bridge
  # or run on a high port and use a local redirect:
  overcast bridge --http-port 8080
  ```

#### Linux

- All four subcommands work out of the box.
- `overcast bridge` requires **avahi** for mDNS. Install it with your package manager:
  ```bash
  # Debian / Ubuntu
  sudo apt install avahi-daemon avahi-utils
  # Fedora / RHEL
  sudo dnf install avahi avahi-tools
  ```
- Binding port 80 without running as root requires the `cap_net_bind_service` capability:
  ```bash
  sudo setcap cap_net_bind_service+ep $(which overcast)
  overcast bridge          # now binds :80 as a normal user
  ```
  Alternatively, run `sudo overcast bridge` or use `--http-port` to pick a high port.
- **ARM64 (Raspberry Pi, AWS Graviton):** pre-built `linux-arm64` binaries are published for every release.

#### Windows

- All four subcommands are supported. Binaries are console `.exe` files — no installer, no service.
- `overcast bridge` uses the Windows DNS-SD service (built into Windows 10 1803+ and Windows Server 2019+). If the service is not running, start it:
  ```powershell
  Start-Service "DNS Client"
  ```
- Binding port 80 requires a URL reservation (run once in an elevated shell):
  ```powershell
  netsh http add urlacl url=http://+:80/ user=%USERNAME%
  overcast bridge          # now binds :80 as a normal user
  ```
  Or use `--http-port` to pick a port above 1024.
- Init hooks (`OVERCAST_INIT_DIRS`) run via `cmd.exe /c` on Windows; `.sh` scripts require WSL or Git Bash.
- `overcast trust` modifies the Windows Certificate Store and will prompt for UAC elevation.

---

## Supported services

<!-- BEGIN overcast:root-service-list -->

Overcast currently registers **50 AWS services**. Coverage ranges from broad
service emulation to minimal discovery/IaC stubs; check the per-service docs for
exact endpoint support.

[ACM](./docs/services/acm.md), [API Gateway](./docs/services/apigateway.md), [AppConfig](./docs/services/appconfig.md), [AppConfigData](./docs/services/appconfigdata.md),
[AppRegistry](./docs/services/appregistry.md), [AppSync](./docs/services/appsync.md), [Athena](./docs/services/athena.md), [Auto Scaling](./docs/services/autoscaling.md),
[Backup](./docs/services/backup.md), [Bedrock](./docs/services/bedrock.md), [CloudFormation](./docs/services/cloudformation.md), [CloudFront](./docs/services/cloudfront.md),
[CloudTrail](./docs/services/cloudtrail.md), [CloudWatch](./docs/services/cloudwatch.md), [CloudWatch Logs](./docs/services/cloudwatch-logs.md), [Cognito](./docs/services/cognito.md),
[DynamoDB](./docs/services/dynamodb.md), [DynamoDB Streams](./docs/services/dynamodbstreams.md), [EC2 / VPC](./docs/services/ec2.md), [ECR](./docs/services/ecr.md),
[ECS](./docs/services/ecs.md), [EFS](./docs/services/efs.md), [EKS](./docs/services/eks.md), [ElastiCache](./docs/services/elasticache.md),
[ELBv2](./docs/services/elb.md), [EventBridge](./docs/services/eventbridge.md), [Firehose](./docs/services/firehose.md), [Glue](./docs/services/glue.md),
[IAM](./docs/services/iam.md), [Kinesis](./docs/services/kinesis.md), [KMS](./docs/services/kms.md), [Lambda](./docs/services/lambda.md),
[MSK](./docs/services/msk.md), [OpenSearch](./docs/services/opensearch.md), [Organizations](./docs/services/organizations.md), [Pipes](./docs/services/pipes.md),
[RDS](./docs/services/rds.md), [Route 53](./docs/services/route53.md), [S3](./docs/services/s3.md), [Scheduler](./docs/services/scheduler.md),
[Secrets Manager](./docs/services/secretsmanager.md), [SES](./docs/services/ses.md), [Shield](./docs/services/shield.md), [SNS](./docs/services/sns.md),
[SQS](./docs/services/sqs.md), [SSM](./docs/services/ssm.md), [Step Functions](./docs/services/stepfunctions.md), [STS](./docs/services/sts.md),
[Transfer Family](./docs/services/transfer.md), [WAF v2](./docs/services/waf.md).

Some services require Docker socket access for full runtime behavior:

- Lambda, ECS, RDS, EC2/VPC, and ElastiCache can launch sibling containers.
- Without Docker, their metadata/control-plane APIs still work where possible,
  but runtime execution falls back to metadata-only or stub behavior.

IAM is implemented for local development and CloudFormation/CDK compatibility,
but IAM policies are not enforced as an authorization layer.

See the [service emulation reference](./docs/services/) for per-endpoint
coverage tables, or browse the generated summary in [STATUS.md](./STATUS.md#service-coverage).

<!-- END overcast:root-service-list -->

---

## Documentation

Full documentation lives in [`docs/`](./docs/README.md):

| Guide                                                               | Description                                                              |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| [Using AWS SDKs and CLI](./docs/sdk-cli.md)                         | Configure the AWS CLI, Node.js, Python, Go, Java, .NET, Rust, Terraform  |
| [Using AWS CDK](./docs/cdk.md)                                      | `cdk bootstrap`, `cdk deploy`, supported resource types, troubleshooting |
| [Networking and host-based addressing](./docs/networking.md)        | Host-routed endpoints (API Gateway, Lambda function URLs, AppSync), wildcard DNS |
| [Service reference](./docs/services/)                               | Per-service endpoint coverage matrices                                   |
| [Configuration reference](./docs/README.md#configuration-reference) | All environment variables                                                |
| [Persistence](./docs/README.md#persistence)                         | Storage backends: memory, hybrid, persistent, WAL                        |
| [HTTPS / TLS](./docs/README.md#https--tls)                          | Self-signed certs for local HTTPS                                        |
| [Event pipelines](./docs/README.md#event-pipelines)                 | SNS→SQS, SQS→Lambda, DynamoDB Streams                                    |
| [Web management console](./docs/README.md#web-management-console)   | Built-in dashboard on port 4567                                          |
| [Debug endpoints](./docs/README.md#debug-endpoints)                 | Health, metrics, state dump, pprof                                       |
| [Migrating from LocalStack](./docs/migration-from-localstack.md)    | Drop-in replacement guide                                                |
| [Development setup](./docs/dev/development-setup.md)                | Building from source                                                     |

---

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) for coding standards and workflow, and
[docs/dev/development-setup.md](./docs/dev/development-setup.md) for building from source.

## Disclaimer

Overcast is an independent open-source project. It is **not affiliated with,
endorsed by, or sponsored by Amazon Web Services**. "AWS" and all AWS service
names are trademarks of Amazon.com, Inc. or its affiliates, used here solely to
describe compatibility.

Overcast is a **work in progress**, provided **as-is** and on a
**best-effort basis**, without warranty of any kind, under the
[MIT License](LICENSE). It aims for high fidelity on the most-used AWS API
surface, but it is not a perfect replica: there are compatibility gaps we know
about (documented in the per-service [support matrices](docs/services/)) and
inevitably some we haven't found yet. Fidelity improves all the time — and
discrepancy reports are what drive that work. If you find behavior that
differs from real AWS, please
[open a compatibility issue](https://github.com/Neaox/overcast/issues/new?template=compat_review.md).
