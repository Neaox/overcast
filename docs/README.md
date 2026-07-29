---
title: "Documentation"
description: "This directory contains the full Overcast documentation. For a quick overview, see the root README."
section: "Getting Started"
tags:
  - docs
  - documentation
---

# Documentation

This directory contains the full Overcast documentation. For a quick overview,
see the [root README](../README.md).

## Contents

### Getting started

- [Using AWS SDKs and CLI](./sdk-cli.md) — configure the AWS CLI (`--endpoint-url`), Node.js, Python, Go, Java, .NET, Rust, Terraform
- [Using AWS CDK](./cdk.md) — `cdk bootstrap`, `cdk deploy`, supported resource types, troubleshooting
- [CDK guides](./cdk/) — focused CDK workflow guides
- [Local VPCs for CDK](./cdk/local-vpc.md) — stable local VPC bootstrap, CDK context cache behavior, VPC provider pattern
- [Networking and host-based addressing](./networking.md) — path-style vs. Host-routed endpoints (API Gateway, Lambda function URLs, AppSync), wildcard DNS setup
- [Migrating from LocalStack](./migration-from-localstack.md) — drop-in replacement guide

### Reference

- [Service emulation reference](./services/README.md) — per-service endpoint coverage tables
- [Configuration reference](#configuration-reference) — all environment variables
- [Service names](#service-names) — every service name and the CDK module it corresponds to
- [Log levels](#log-levels) — `OVERCAST_LOG_LEVEL` values and what each one shows
- [Persistence](#persistence) — storage backends
- [HTTPS / TLS](#https--tls) — browser-trusted HTTPS and HTTP/2 in two commands; see [HTTPS and HTTP/2](./https.md)
- [Debug endpoints](#debug-endpoints) — health, metrics, state dump, pprof
- [Event pipelines](#event-pipelines) — SNS→SQS, SQS→Lambda, DynamoDB Streams
- [Web management console](#web-management-console) — built-in dashboard

### Storage and performance

- [Performance](./performance.md) — startup expectations, storage tuning, and where "feels slow" time actually goes
- [Storage backends](./storage.md) — durability comparison and what survives a restart, per backend

Internal working plans (storage stabilization, storage access patterns, pagination fidelity,
the storage regression test plan, and others) live in `docs/plans/` in the repository, and
contributor-facing developer docs (building from source, debugging, wire-protocol design,
storage internals, AWS compatibility review tracking) live in `docs/dev/` — both are
deliberately excluded from this published documentation set. See
[CONTRIBUTING.md](../CONTRIBUTING.md) and [AGENTS.md](../AGENTS.md) if you're contributing
to Overcast itself.

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

For a shorter overview, start with the [service reference index](./services/README.md).

<!-- BEGIN overcast:service-index -->

| Service          | Doc                                                 | Ops | Coverage tier                 |
| ---------------- | --------------------------------------------------- | --- | ----------------------------- |
| S3               | [s3.md](./services/s3.md)                           | 47  | Comprehensive / broad support |
| SQS              | [sqs.md](./services/sqs.md)                         | 21  | Comprehensive / broad support |
| DynamoDB         | [dynamodb.md](./services/dynamodb.md)               | 19  | Comprehensive / broad support |
| Lambda           | [lambda.md](./services/lambda.md)                   | 48  | Comprehensive / broad support |
| API Gateway      | [apigateway.md](./services/apigateway.md)           | 105 | Comprehensive / broad support |
| AppSync          | [appsync.md](./services/appsync.md)                 | 82  | Comprehensive / broad support |
| CloudFront       | [cloudfront.md](./services/cloudfront.md)           | 89  | Comprehensive / broad support |
| Cognito          | [cognito.md](./services/cognito.md)                 | 67  | Comprehensive / broad support |
| EC2 / VPC        | [ec2.md](./services/ec2.md)                         | 72  | Comprehensive / broad support |
| SNS              | [sns.md](./services/sns.md)                         | 24  | Comprehensive / broad support |
| IAM              | [iam.md](./services/iam.md)                         | 61  | Core CRUD + common workflows  |
| ECS              | [ecs.md](./services/ecs.md)                         | 48  | Core CRUD + common workflows  |
| ECR              | [ecr.md](./services/ecr.md)                         | 20  | Core CRUD + common workflows  |
| KMS              | [kms.md](./services/kms.md)                         | 32  | Core CRUD + common workflows  |
| Kinesis          | [kinesis.md](./services/kinesis.md)                 | 17  | Core CRUD + common workflows  |
| EventBridge      | [eventbridge.md](./services/eventbridge.md)         | 28  | Core CRUD + common workflows  |
| Scheduler        | [scheduler.md](./services/scheduler.md)             | 12  | Core CRUD + common workflows  |
| CloudFormation   | [cloudformation.md](./services/cloudformation.md)   | 48  | Core CRUD + common workflows  |
| RDS              | [rds.md](./services/rds.md)                         | 33  | Core CRUD + common workflows  |
| ElastiCache      | [elasticache.md](./services/elasticache.md)         | 24  | Core CRUD + common workflows  |
| AppConfig        | [appconfig.md](./services/appconfig.md)             | 12  | Core CRUD + common workflows  |
| AppConfigData    | [appconfigdata.md](./services/appconfigdata.md)     | 3   | Core CRUD + common workflows  |
| Secrets Manager  | [secretsmanager.md](./services/secretsmanager.md)   | 21  | Core CRUD + common workflows  |
| SSM              | [ssm.md](./services/ssm.md)                         | 18  | Core CRUD + common workflows  |
| CloudWatch Logs  | [cloudwatch-logs.md](./services/cloudwatch-logs.md) | 19  | Core CRUD + common workflows  |
| SES              | [ses.md](./services/ses.md)                         | 42  | Core CRUD + common workflows  |
| STS              | [sts.md](./services/sts.md)                         | 11  | Core CRUD + common workflows  |
| Step Functions   | [stepfunctions.md](./services/stepfunctions.md)     | 5   | Minimal / targeted support    |
| Pipes            | [pipes.md](./services/pipes.md)                     | 5   | Minimal / targeted support    |
| WAF v2           | [waf.md](./services/waf.md)                         | 4   | Minimal / targeted support    |
| Shield           | [shield.md](./services/shield.md)                   | 5   | Minimal / targeted support    |
| ACM              | [acm.md](./services/acm.md)                         | 7   | Minimal / targeted support    |
| Athena           | [athena.md](./services/athena.md)                   | 8   | Minimal / targeted support    |
| Bedrock          | [bedrock.md](./services/bedrock.md)                 | 2   | Minimal / targeted support    |
| CloudWatch       | [cloudwatch.md](./services/cloudwatch.md)           | 12  | Minimal / targeted support    |
| DynamoDB Streams | [dynamodbstreams.md](./services/dynamodbstreams.md) | 4   | Minimal / targeted support    |
| Firehose         | [firehose.md](./services/firehose.md)               | 6   | Minimal / targeted support    |
| Glue             | [glue.md](./services/glue.md)                       | 8   | Minimal / targeted support    |
| OpenSearch       | [opensearch.md](./services/opensearch.md)           | 8   | Minimal / targeted support    |
| AppRegistry      | [appregistry.md](./services/appregistry.md)         | 21  | IaC/discovery-oriented stub   |
| Auto Scaling     | [autoscaling.md](./services/autoscaling.md)         | 19  | IaC/discovery-oriented stub   |
| Backup           | [backup.md](./services/backup.md)                   | 9   | IaC/discovery-oriented stub   |
| CloudTrail       | [cloudtrail.md](./services/cloudtrail.md)           | 9   | IaC/discovery-oriented stub   |
| EKS              | [eks.md](./services/eks.md)                         | 52  | IaC/discovery-oriented stub   |
| ELBv2            | [elb.md](./services/elb.md)                         | 15  | IaC/discovery-oriented stub   |
| MSK              | [msk.md](./services/msk.md)                         | 29  | IaC/discovery-oriented stub   |
| Organizations    | [organizations.md](./services/organizations.md)     | 1   | IaC/discovery-oriented stub   |
| Route 53         | [route53.md](./services/route53.md)                 | 10  | IaC/discovery-oriented stub   |
| Transfer Family  | [transfer.md](./services/transfer.md)               | 10  | IaC/discovery-oriented stub   |

<!-- END overcast:service-index -->

Want to add support for a new AWS service? See
[CONTRIBUTING.md § How to add a service](https://github.com/Neaox/overcast/blob/main/CONTRIBUTING.md#how-to-add-a-service)
in the repository.

---

## Configuration reference

All configuration is via environment variables. No config file required.

| Variable                         | Default                | Description                                                                          |
| -------------------------------- | ---------------------- | ------------------------------------------------------------------------------------ |
| `OVERCAST_HOST`                  | `0.0.0.0`              | Hostname or IP to bind to                                                            |
| `OVERCAST_HOSTNAME`              | `localhost`            | Hostname embedded in client-facing URLs (SQS queue URLs, Lambda function URLs, API Gateway `apiEndpoint`, AppSync DNS names, CloudFront domain names). **Set it to `localhost.overcast.sh`** unless you are offline: every `*.localhost.overcast.sh` name resolves to `127.0.0.1` on every OS, which plain `localhost` does not on Windows. See [networking.md](./networking.md) |
| `OVERCAST_SPLIT_HORIZON_HOSTS`   | _(none)_               | Extra comma-separated hostnames remapped to Overcast inside containers it starts (ECS tasks), so one URL is dialable from both host and container. Added to the built-in `localhost.overcast.sh`, `localhost.localstack.cloud`, `localhost.floci.io` |
| `OVERCAST_PORT`                  | `4566`                 | TCP port                                                                             |
| `OVERCAST_STATE`                 | `auto`                 | Storage backend: `auto` (default when unset), `memory`, `hybrid`, `persistent`, or `wal`. `auto` resolves to `hybrid` when a volume/bind mount or existing database is found at `OVERCAST_DATA_DIR` (or the dir was explicitly set), otherwise `memory` — see [storage.md § The auto default](./storage.md#the-auto-default) |
| `OVERCAST_STATE_<SERVICE>`       | _(global)_             | Per-service backend override, e.g. `OVERCAST_STATE_S3=memory`                        |
| `OVERCAST_HYBRID_FLUSH_INTERVAL` | `5s`                   | How often the hybrid backend flushes in-memory state to disk                         |
| `OVERCAST_HYBRID_SYNC`           | `interval`             | Hybrid pending-log fsync policy: `always`, `interval`, or `never`                    |
| `OVERCAST_HYBRID_SYNC_INTERVAL`  | `100ms`                | Periodic fsync interval used when `OVERCAST_HYBRID_SYNC=interval`                    |
| `OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD` | `10000`         | Unflushed-write count that triggers an early hybrid flush ahead of the timer (`<= 0` disables) |
| `OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD`  | `8388608`       | Approximate unflushed-write bytes that trigger an early hybrid flush (default 8 MiB; `<= 0` disables) |
| `OVERCAST_HYBRID_MAINTENANCE_INTERVAL`  | `5m`            | How often the hybrid backend runs background SQLite housekeeping (passive WAL checkpoint + conditional incremental vacuum) |
| `OVERCAST_WAL_FSYNC`             | `interval`             | WAL fsync policy: `always`, `interval`, or `never`                                   |
| `OVERCAST_WAL_FSYNC_INTERVAL`    | `100ms`                | Periodic fsync interval used when `OVERCAST_WAL_FSYNC=interval`                      |
| `OVERCAST_WAL_MAX_LOG_BYTES`     | `67108864`             | WAL log compaction threshold in bytes (default 64 MiB)                               |
| `OVERCAST_DATA_DIR`              | `~/.overcast/data`     | Directory for store files and other on-disk state                                    |
| `OVERCAST_DEFAULT_REGION`        | `us-east-1`            | Fallback region used in ARNs when not present in SigV4 header                        |
| `OVERCAST_ACCOUNT_ID`            | `000000000000`         | Account ID embedded in ARNs                                                          |
| `OVERCAST_LOG_LEVEL`             | `info`                 | `trace`, `debug`, `info`, `warn`, `error` — see [Log levels](#log-levels) below      |
| `OVERCAST_DEBUG`                 | `false`                | Enable `/_debug/*` endpoints                                                         |
| `OVERCAST_SIGV4_VALIDATE`        | `false`                | SigV4 verification _(not yet implemented)_                                           |
| `OVERCAST_CFN_SYNC_WAIT_MS`      | `1000`                 | Milliseconds CloudFormation waits for fast stack provisioning before returning (`0` disables) |
| `OVERCAST_TLS`                   | —                      | `auto` = serve API **and** web UI over HTTPS with a certificate minted from the local overcast CA (unlocks browser HTTP/2) — see [HTTPS and HTTP/2](./https.md) |
| `OVERCAST_TLS_CERT`              | —                      | Path to your own TLS certificate (enables HTTPS for API and web UI; mutually exclusive with `OVERCAST_TLS=auto`) |
| `OVERCAST_TLS_KEY`               | —                      | Path to the matching TLS private key                                                 |
| `OVERCAST_SHUTDOWN_TIMEOUT`      | `5s`                   | Graceful shutdown wait; also budgets the final store flush — if it can't finish in time the process exits anyway and unflushed writes replay from the pending log on next start |
| `LAMBDA_DOCKER_SOCKET`           | `/var/run/docker.sock` | Docker endpoint — Unix path or `tcp://host:port` (for DinD)                          |
| `LAMBDA_NETWORK`                 | `overcast_lambda`      | Docker network for Lambda containers                                                 |
| `LAMBDA_RUNTIME_API_PORT`        | `9001`                 | Port Overcast exposes the Lambda Runtime API on                                      |
| `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` | _(auto)_               | Max concurrent Docker-backed Lambda container starts. Unset: derived from the Docker host as `clamp(NCPU/2, 2, 8)` (each start bursts ~2 CPUs during INIT); `4` when Docker `/info` is unavailable |
| `LAMBDA_MAX_INSTANCES`           | _(auto)_               | Max Lambda containers across all functions. Unset: derived from the Docker host as `clamp(MemTotal×0.65 / 256 MiB, 4, 32)`; `25` when `/info` is unavailable |
| `LAMBDA_MAX_INSTANCES_PER_FUNCTION` | _(auto)_            | Max concurrent containers for one function. Unset: `clamp(maxInstances/2, 2, maxInstances)`; `10` when `/info` is unavailable |
| `LAMBDA_MAX_MEMORY_MB`           | _(auto)_               | Aggregate memory budget for live Lambda containers (Σ `MemorySize`, in MB). Unset: 65% of the Docker host's `MemTotal`; unlimited when `/info` is unavailable |
| `LAMBDA_MAX_WARM_INSTANCES`      | `10`                   | Idle containers kept warm per function after a burst                                 |
| `LAMBDA_SEED_RUNTIME_IMAGES`     | `false`                | Pre-pull every managed Lambda runtime image at startup                               |
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

### Service names

Every service listed below always runs — there is no way to switch one off, and
nothing to configure to get one. The names matter for one thing: they are what
the per-service storage override `OVERCAST_STATE_<SERVICE>` is keyed by, in
upper case. CloudWatch Logs is `logs`, so its override is `OVERCAST_STATE_LOGS`
— not `OVERCAST_STATE_CLOUDWATCH_LOGS`, which names nothing and is rejected at
startup.

Each name is the service's AWS CLI name, which for several services matches
neither the display name nor the `aws-cdk-lib` module you would import. The CDK
column is there because that is the mapping people most often need to make.

For per-service endpoint coverage, follow the doc links in [Services](#services)
above.

<!-- BEGIN overcast:service-names -->

| Name              | Service          | CDK module (`aws-cdk-lib/…`)                       |
| ----------------- | ---------------- | -------------------------------------------------- |
| `s3`              | S3               | `aws-s3`                                           |
| `sqs`             | SQS              | `aws-sqs`                                          |
| `dynamodb`        | DynamoDB         | `aws-dynamodb`                                     |
| `lambda`          | Lambda           | `aws-lambda`                                       |
| `apigateway`      | API Gateway      | `aws-apigateway`, `aws-apigatewayv2`               |
| `appsync`         | AppSync          | `aws-appsync`                                      |
| `cloudfront`      | CloudFront       | `aws-cloudfront`, `aws-cloudfront-origins`         |
| `cognito`         | Cognito          | `aws-cognito`                                      |
| `ec2`             | EC2 / VPC        | `aws-ec2`                                          |
| `sns`             | SNS              | `aws-sns`                                          |
| `iam`             | IAM              | `aws-iam`                                          |
| `ecs`             | ECS              | `aws-ecs`                                          |
| `ecr`             | ECR              | `aws-ecr`, `aws-ecr-assets`                        |
| `kms`             | KMS              | `aws-kms`                                          |
| `kinesis`         | Kinesis          | `aws-kinesis`                                      |
| `eventbridge`     | EventBridge      | `aws-events`, `aws-events-targets`                 |
| `scheduler`       | Scheduler        | `aws-scheduler`                                    |
| `cloudformation`  | CloudFormation   | `aws-cloudformation`                               |
| `rds`             | RDS              | `aws-rds`                                          |
| `elasticache`     | ElastiCache      | `aws-elasticache`                                  |
| `appconfig`       | AppConfig        | `aws-appconfig`                                    |
| `appconfigdata`   | AppConfigData    | — (runtime data plane; no constructs)              |
| `secretsmanager`  | Secrets Manager  | `aws-secretsmanager`                               |
| `ssm`             | SSM              | `aws-ssm`                                          |
| `logs`            | CloudWatch Logs  | `aws-logs`                                         |
| `ses`             | SES              | `aws-ses`                                          |
| `sts`             | STS              | — (used by the CDK CLI itself)                     |
| `stepfunctions`   | Step Functions   | `aws-stepfunctions`, `aws-stepfunctions-tasks`     |
| `pipes`           | Pipes            | `aws-pipes`                                        |
| `waf`             | WAF v2           | `aws-wafv2`                                        |
| `shield`          | Shield           | `aws-shield`                                       |
| `acm`             | ACM              | `aws-certificatemanager`                           |
| `athena`          | Athena           | `aws-athena`                                       |
| `bedrock`         | Bedrock          | `aws-bedrock`                                      |
| `cloudwatch`      | CloudWatch       | `aws-cloudwatch`, `aws-cloudwatch-actions`         |
| `dynamodbstreams` | DynamoDB Streams | — (enabled by the `stream` prop on `aws-dynamodb`) |
| `firehose`        | Firehose         | `aws-kinesisfirehose`                              |
| `glue`            | Glue             | `aws-glue`                                         |
| `opensearch`      | OpenSearch       | `aws-opensearchservice`                            |
| `appregistry`     | AppRegistry      | `aws-servicecatalogappregistry`                    |
| `autoscaling`     | Auto Scaling     | `aws-autoscaling`, `aws-applicationautoscaling`    |
| `backup`          | Backup           | `aws-backup`                                       |
| `cloudtrail`      | CloudTrail       | `aws-cloudtrail`                                   |
| `eks`             | EKS              | `aws-eks`                                          |
| `elbv2`           | ELBv2            | `aws-elasticloadbalancingv2`                       |
| `msk`             | MSK              | `aws-msk`                                          |
| `organizations`   | Organizations    | — (no constructs)                                  |
| `route53`         | Route 53         | `aws-route53`, `aws-route53-targets`               |
| `transfer`        | Transfer Family  | `aws-transfer`                                     |

<!-- END overcast:service-names -->

### Log levels

`OVERCAST_LOG_LEVEL` controls how much Overcast logs, from quietest to noisiest:

| Level   | What you'll see                                                                                          |
| ------- | --------------------------------------------------------------------------------------------------------- |
| `info`  | **Default.** Lifecycle events (start, shutdown, migrations) and one line per AWS API call your app makes. |
| `debug` | Everything in `info`, plus the reasoning behind each response — what to attach to a bug report.           |
| `trace` | Everything in `debug`, plus emulator machinery: health-check probes, web UI polling, background flush/sweep ticks. Very high volume — use for a short capture window, not always-on. |
| `warn`  | One-liners for handled-but-unexpected conditions (a malformed record was skipped, a slow filesystem was detected). |
| `error` | One-liners for failures that need attention (storage degraded, a migration failed).                       |

For contributors: the full call-site policy (what belongs at `debug` vs
`trace`) is documented in [CONTRIBUTING.md § Log levels](../CONTRIBUTING.md#log-levels).

---

## Persistence

Overcast supports four concrete storage backends, set via `OVERCAST_STATE`:

| Backend      | Description                                                                             |
| ------------ | --------------------------------------------------------------------------------------- |
| `auto`       | **Default when unset.** Resolves to `hybrid` or `memory` at startup — see below.         |
| `memory`     | All state in-process; lost on restart. Fastest — ideal for CI.                          |
| `hybrid`     | Reads from memory, flushes to SQLite asynchronously. Fast with durability.               |
| `persistent` | Every mutation written synchronously to SQLite. Fully durable, slightly slower.         |
| `wal`        | In-memory reads + append-log durability with replay on startup and periodic compaction. |

**`OVERCAST_STATE` is unset by default, which means `auto`:** Overcast picks a mode based
on whether a durable data location was provided — a volume or bind mount at the data
directory resolves to `hybrid` (persist); nothing mounted resolves to `memory`. In CI,
where containers typically run with no data volume, this means `auto` lands on `memory` —
the fast, ephemeral mode CI wants — with zero configuration. See
[storage.md § The auto default](./storage.md#the-auto-default) for the full decision
rule (it also covers native, non-Docker runs).

For state that persists across restarts, just mount a volume — `auto` does the rest:

```bash
docker run --rm \
  -p 4566:4566 \
  -v $(pwd)/overcast-data:/data \
  ghcr.io/neaox/overcast:alpha
```

This resolves to `hybrid` automatically because a volume is mounted at `/data`. Set
`OVERCAST_STATE` explicitly (e.g. `-e OVERCAST_STATE=persistent`) if you need a different
backend than what `auto` would pick.

Persistent/hybrid SQLite data lives at `$OVERCAST_DATA_DIR/overcast.db`. WAL mode uses `$OVERCAST_DATA_DIR/overcast.wal`. You can also override the backend per-service:

Hybrid seeds small control-plane namespaces into memory on startup and reads large data-plane namespaces (messages, log events, metric datapoints) from SQLite on every access — there is no read-through cache for those, by design — so background schedulers and dashboards do not continuously poll SQLite for hot resource metadata, while high-volume data never has to fit in memory. See [storage.md](./storage.md) for the full backend comparison, or [dev/storage-backends.md](./dev/storage-backends.md) for the implementation internals.

```bash
-e OVERCAST_STATE=memory -e OVERCAST_STATE_S3=hybrid
```

### Per-service storage overrides

Each service can use a different backend. Set `OVERCAST_STATE_<SERVICE>`
where `<SERVICE>` is one of the [service names](#service-names) in upper case,
so CloudWatch Logs is `OVERCAST_STATE_LOGS`:

```bash
docker run --rm -p 4566:4566 \
  -e OVERCAST_STATE=memory \
  -e OVERCAST_STATE_DYNAMODB=persistent \
  -e OVERCAST_STATE_S3=hybrid \
  -v $(pwd)/data:/data \
  ghcr.io/neaox/overcast:alpha
```

> **Note:** a few services accept an override that can have no effect, and log a startup
> warning when one is set: `DYNAMODBSTREAMS` (a facade over the `dynamodb` service, which owns
> all stream state), `STS` (its session state lives under IAM's storage), and
> `BEDROCK`/`ORGANIZATIONS` (stateless stubs). Every other service's override works.

In this example DynamoDB writes synchronously to disk, S3 flushes
asynchronously, and every other service uses in-memory (ephemeral)
storage. Each overridden service gets its own SQLite file under
`$OVERCAST_DATA_DIR/<service>/`.

The active storage configuration is visible in three places:

- **`GET /_health`** — the `storage` object shows the resolved default backend (`default`), what was actually configured (`configured` — e.g. `auto`, when `default` was resolved rather than explicitly set), per-service overrides, and persistent backend health including pending hybrid writes when available.
- **Dashboard footer** — the web management console displays the storage mode with a tooltip listing overrides.
- **Startup log** — when `OVERCAST_STATE` resolves via `auto`, Overcast logs which mode it picked and why (e.g. `storage mode auto-detected: memory (no persistence signal found...) — set OVERCAST_STATE to override`). The web console's Metrics & Health page also surfaces this as an advisory whenever the resolved mode is `memory`.

---

## HTTPS / TLS

Full guide: **[HTTPS and HTTP/2](./https.md)** — why the web console needs it
(browsers cap HTTP/1.1 at 6 connections per origin, localhost included, and
never speak cleartext HTTP/2, so the console's SSE + progress streams starve
navigation under load), the trust model, offline behaviour, and the manual
setup path.

The two-command version:

```bash
overcast https enable            # once per machine: local CA → system trust store
OVERCAST_TLS=auto overcast serve # both listeners now serve HTTPS + HTTP/2
```

Running in Docker? Also two commands — the daemon serves its CA certificate
at `/_overcast/ca.pem`, so no shared volume or manual cert wrangling:

```bash
docker run -d -e OVERCAST_TLS=auto -v overcast-data:/data \
  -p 4566:4566 -p 4567:4567 ghcr.io/neaox/overcast:alpha
overcast https enable --endpoint http://localhost:4566
```

Then open <https://localhost.overcast.sh:4567> (public DNS resolves
`*.localhost.overcast.sh` to `127.0.0.1` — no hosts-file edits). Both the API
(4566) and the web UI (4567) are served over TLS; browsers negotiate HTTP/2
via ALPN and multiplex everything over one connection.

Prefer your own certificate? `OVERCAST_TLS_CERT`/`OVERCAST_TLS_KEY` still
work and now also apply to the web UI:

```bash
docker run --rm \
  -p 4566:4566 -p 4567:4567 \
  -e OVERCAST_TLS_CERT=/certs/cert.pem \
  -e OVERCAST_TLS_KEY=/certs/key.pem \
  -v $(pwd):/certs \
  ghcr.io/neaox/overcast:alpha
```

```bash
export AWS_CA_BUNDLE=~/.overcast/data/ca/rootCA.pem  # AWS CLI + boto3 (auto mode)
export NODE_EXTRA_CA_CERTS=~/.overcast/data/ca/rootCA.pem # Node.js SDK
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
    image: ghcr.io/neaox/overcast:alpha
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
| `/_debug/health`            | GET    | Detailed: uptime, services, state backend and health  |
| `/_debug/config`            | GET    | Effective configuration (secrets redacted)            |
| `/_debug/state`             | GET    | Every namespace and its keys (no values)              |
| `/_debug/state/{namespace}` | GET    | Paginated key/value pages for one namespace (`?after=` cursor, `?limit=` ≤ 5000, default 500); `?key=` fetches one raw value |
| `/_debug/reset`             | POST   | Wipe all state                                        |
| `/_debug/reset/{service}`   | POST   | Wipe state for one service                            |
| `/_debug/metrics`           | GET    | Storage diagnostics: flush history, seed duration, pending-log size; `?includeRowCounts=true` adds per-namespace row counts |
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

> [!TIP]
> If the console feels sluggish or stops responding to clicks while many
> Lambdas run or transfers are in flight, you are hitting the browser's
> 6-connection HTTP/1.1 limit — the live feed and progress streams are
> holding the sockets. Serve the console over HTTPS to unlock HTTP/2 and
> keep it responsive under any load: see [HTTPS and HTTP/2](./https.md).
