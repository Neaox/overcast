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
- [CLI reference](./cli.md) — every `overcast` subcommand: background instances, introspection, AWS environment helpers, networking and TLS
- [Using AWS CDK](./cdk.md) — `cdk bootstrap`, `cdk deploy`, supported resource types, troubleshooting
- [CDK guides](./cdk/) — focused CDK workflow guides
- [Local VPCs for CDK](./cdk/local-vpc.md) — local resources stack that creates the VPC, environment-agnostic application stacks, CDK context cache behavior
- [Networking and host-based addressing](./networking.md) — path-style vs. Host-routed endpoints (API Gateway, Lambda function URLs, AppSync), wildcard DNS setup
- [The inner loop](./local-dev.md) — edit a file and see it take effect: `cdk watch`, Lambda and ECS hot reload, and a Laravel-on-Fargate walkthrough
- [Testcontainers](./testcontainers.md) — start Overcast from integration tests with the Go module; options, port-mapping caveats
- [Migrating from LocalStack](./migration-from-localstack.md) — drop-in replacement guide

### Reference

- [Service emulation reference](./services/README.md) — per-service endpoint coverage tables
- [Configuration reference](#configuration-reference) — all environment variables
- [Service names](#service-names) — every service name and the CDK module it corresponds to
- [Log levels](#log-levels) — `OVERCAST_LOG_LEVEL` values and what each one shows
- [Persistence](#persistence) — storage backends
- [HTTPS / TLS](#https-tls) — browser-trusted HTTPS and HTTP/2 in two commands; see [HTTPS and HTTP/2](./https.md)
- [Debug endpoints](#debug-endpoints) — health, metrics, state dump, pprof
- [Event pipelines](#event-pipelines) — SNS→SQS, SQS→Lambda, DynamoDB Streams
- [Web management console](#web-management-console) — built-in dashboard
- [Troubleshooting](#troubleshooting) — startup preflight warnings and what they mean

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
endpoint (`/_overcast/health`) and the web dashboard:

| Tier        | Meaning                                                                                                                                                                           |
| ----------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Full**    | P1+P2 operations implemented. Real SDK clients can use it end-to-end.                                                                                                             |
| **Partial** | P1 operations implemented. Basic workflows work.                                                                                                                                  |
| **Inert**   | Full CRUD works — resources are created and stored — but no side-effects or enforcement occur. For example, IAM stores users, roles, and policies but never enforces permissions. |
| **Stub**    | Registered so discovery works: at most a hardcoded, stateless answer to the service's describe call; every other operation returns `501 Not Implemented`.                        |

Endpoints marked **Unsupported** return a well-formed AWS error response so
that SDKs surface a clear error rather than a connection failure:

```
HTTP 501 Not Implemented
x-emulator-unsupported: true

{
  "__type": "NotImplemented",
  "message": "This operation is not yet emulated. See https://github.com/overcast-sh/overcast/docs/services/<service>.md"
}
```

---

## Services

For a shorter overview, start with the [service reference index](./services/README.md).

<!-- BEGIN overcast:service-index -->

| Service          | Doc                                                 | Ops | Coverage tier                 |
| ---------------- | --------------------------------------------------- | --- | ----------------------------- |
| S3               | [s3.md](./services/s3.md)                           | 53  | Comprehensive / broad support |
| SQS              | [sqs.md](./services/sqs.md)                         | 21  | Comprehensive / broad support |
| DynamoDB         | [dynamodb.md](./services/dynamodb.md)               | 28  | Comprehensive / broad support |
| Lambda           | [lambda.md](./services/lambda.md)                   | 59  | Comprehensive / broad support |
| API Gateway      | [apigateway.md](./services/apigateway.md)           | 106 | Comprehensive / broad support |
| AppSync          | [appsync.md](./services/appsync.md)                 | 82  | Comprehensive / broad support |
| CloudFront       | [cloudfront.md](./services/cloudfront.md)           | 89  | Comprehensive / broad support |
| Cognito          | [cognito.md](./services/cognito.md)                 | 70  | Comprehensive / broad support |
| EC2 / VPC        | [ec2.md](./services/ec2.md)                         | 72  | Comprehensive / broad support |
| SNS              | [sns.md](./services/sns.md)                         | 30  | Comprehensive / broad support |
| IAM              | [iam.md](./services/iam.md)                         | 74  | Core CRUD + common workflows  |
| ECS              | [ecs.md](./services/ecs.md)                         | 48  | Core CRUD + common workflows  |
| ECR              | [ecr.md](./services/ecr.md)                         | 22  | Core CRUD + common workflows  |
| KMS              | [kms.md](./services/kms.md)                         | 33  | Core CRUD + common workflows  |
| Kinesis          | [kinesis.md](./services/kinesis.md)                 | 23  | Core CRUD + common workflows  |
| EventBridge      | [eventbridge.md](./services/eventbridge.md)         | 29  | Core CRUD + common workflows  |
| Scheduler        | [scheduler.md](./services/scheduler.md)             | 12  | Core CRUD + common workflows  |
| CloudFormation   | [cloudformation.md](./services/cloudformation.md)   | 52  | Core CRUD + common workflows  |
| RDS              | [rds.md](./services/rds.md)                         | 34  | Core CRUD + common workflows  |
| ElastiCache      | [elasticache.md](./services/elasticache.md)         | 24  | Core CRUD + common workflows  |
| EFS              | [efs.md](./services/efs.md)                         | 31  | Core CRUD + common workflows  |
| AppConfig        | [appconfig.md](./services/appconfig.md)             | 20  | Core CRUD + common workflows  |
| AppConfigData    | [appconfigdata.md](./services/appconfigdata.md)     | 2   | Core CRUD + common workflows  |
| Secrets Manager  | [secretsmanager.md](./services/secretsmanager.md)   | 22  | Core CRUD + common workflows  |
| SSM              | [ssm.md](./services/ssm.md)                         | 18  | Core CRUD + common workflows  |
| CloudWatch Logs  | [cloudwatch-logs.md](./services/cloudwatch-logs.md) | 22  | Core CRUD + common workflows  |
| SES              | [ses.md](./services/ses.md)                         | 45  | Core CRUD + common workflows  |
| STS              | [sts.md](./services/sts.md)                         | 11  | Core CRUD + common workflows  |
| Route 53         | [route53.md](./services/route53.md)                 | 25  | Core CRUD + common workflows  |
| Auto Scaling     | [autoscaling.md](./services/autoscaling.md)         | 25  | Core CRUD + common workflows  |
| Step Functions   | [stepfunctions.md](./services/stepfunctions.md)     | 15  | Minimal / targeted support    |
| Pipes            | [pipes.md](./services/pipes.md)                     | 8   | Minimal / targeted support    |
| WAF v2           | [waf.md](./services/waf.md)                         | 7   | Minimal / targeted support    |
| Shield           | [shield.md](./services/shield.md)                   | 8   | Minimal / targeted support    |
| ACM              | [acm.md](./services/acm.md)                         | 10  | Minimal / targeted support    |
| Athena           | [athena.md](./services/athena.md)                   | 11  | Minimal / targeted support    |
| Bedrock          | [bedrock.md](./services/bedrock.md)                 | 2   | Minimal / targeted support    |
| CloudWatch       | [cloudwatch.md](./services/cloudwatch.md)           | 17  | Minimal / targeted support    |
| DynamoDB Streams | [dynamodbstreams.md](./services/dynamodbstreams.md) | 4   | Minimal / targeted support    |
| Firehose         | [firehose.md](./services/firehose.md)               | 9   | Minimal / targeted support    |
| Glue             | [glue.md](./services/glue.md)                       | 11  | Minimal / targeted support    |
| OpenSearch       | [opensearch.md](./services/opensearch.md)           | 8   | Minimal / targeted support    |
| AppRegistry      | [appregistry.md](./services/appregistry.md)         | 22  | IaC/discovery-oriented stub   |
| Backup           | [backup.md](./services/backup.md)                   | 12  | IaC/discovery-oriented stub   |
| CloudTrail       | [cloudtrail.md](./services/cloudtrail.md)           | 12  | IaC/discovery-oriented stub   |
| EKS              | [eks.md](./services/eks.md)                         | 50  | IaC/discovery-oriented stub   |
| ELBv2            | [elb.md](./services/elb.md)                         | 21  | IaC/discovery-oriented stub   |
| MSK              | [msk.md](./services/msk.md)                         | 30  | IaC/discovery-oriented stub   |
| Organizations    | [organizations.md](./services/organizations.md)     | 9   | IaC/discovery-oriented stub   |
| Transfer Family  | [transfer.md](./services/transfer.md)               | 13  | IaC/discovery-oriented stub   |

<!-- END overcast:service-index -->

Want to add support for a new AWS service? See
[CONTRIBUTING.md § How to add a service](https://github.com/overcast-sh/overcast/blob/main/CONTRIBUTING.md#how-to-add-a-service)
in the repository.

---

## Configuration reference

All configuration is via environment variables. No config file required.

<!--
  This table mirrors the authoritative enumeration in the doc comment on
  Config.Load in internal/config/config.go. When a variable is added or
  removed there, update this table in the same change. Deliberately absent:
  OVERCAST_DATA_DIR_SOURCE (internal provenance marker set by the Docker
  image, not for end users) and removed variables that are no longer read
  (OVERCAST_SERVICES, OVERCAST_MCP_REPLAY_LIMIT, and the per-service
  LAMBDA_NETWORK/ECS_NETWORK/RDS_NETWORK/ELASTICACHE_NETWORK/MSK_NETWORK/
  EKS_NETWORK/EFS_NETWORK, replaced by OVERCAST_NETWORK).
-->
The web console's `OVERCAST_UI_PORT` is documented under
[Web management console](#web-management-console); everything the Go emulator
itself reads is below.

| Variable                         | Default                | Description                                                                          |
| -------------------------------- | ---------------------- | ------------------------------------------------------------------------------------ |
| `OVERCAST_LISTEN`                | `0.0.0.0` containerised, `127.0.0.1` native (see [#761](https://github.com/overcast-sh/overcast/issues/761)) | Hostname or IP to bind the AWS API to (LocalStack's `GATEWAY_LISTEN` idiom — not the same thing as `OVERCAST_HOSTNAME` below). Accepts a comma-separated list to bind several, e.g. `127.0.0.1,172.17.0.1` to be reachable from this machine and from its containers over the Docker bridge without being on any network the machine is attached to. A wildcard cannot be combined with a specific address. The web console binds the first address only. An explicit value always wins over the default, in either direction (e.g. `OVERCAST_LISTEN=0.0.0.0` restores the old native reach from a VM or another machine). Renamed from `OVERCAST_HOST`, which has been removed: a leftover `OVERCAST_HOST` fails at startup naming this variable instead of being silently ignored |
| `OVERCAST_HOSTNAME`              | `localhost`            | Hostname embedded in client-facing URLs (SQS queue URLs, Lambda function URLs, API Gateway `apiEndpoint`, AppSync DNS names, CloudFront domain names). **Set it to `localhost.overcast.sh`** unless you are offline: every `*.localhost.overcast.sh` name resolves to `127.0.0.1` on every OS, which plain `localhost` does not on Windows. See [networking.md](./networking.md). LocalStack's `LOCALSTACK_HOST` is accepted as a compatibility alias (see the row below) |
| `LOCALSTACK_HOST` *(alias)*      | _(none)_               | LocalStack-compatibility alias for `OVERCAST_HOSTNAME` ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) — Overcast is meant to be a drop-in replacement for LocalStack, so its documented settings are honoured directly rather than requiring a rename. Accepts LocalStack's `hostname[:port]` format (e.g. `localhost.localstack.cloud:4566`): the hostname part maps to `OVERCAST_HOSTNAME`, and a port part is accepted only if it matches `OVERCAST_PORT`. Setting both `OVERCAST_HOSTNAME` and `LOCALSTACK_HOST` to the *same* hostname is fine (the natural result of migrating a compose file line by line); setting them to *different* hostnames, or a `LOCALSTACK_HOST` port that disagrees with `OVERCAST_PORT`, fails startup naming both rather than silently preferring one. A startup log line names the alias whenever it was recognised |
| `HOSTNAME_EXTERNAL` *(alias)*    | _(none)_               | Legacy LocalStack name `LOCALSTACK_HOST` replaced; also accepted as a compatibility alias for `OVERCAST_HOSTNAME` ([#1190](https://github.com/overcast-sh/overcast/issues/1190)), chained after `LOCALSTACK_HOST` — all three spellings must agree when more than one is set. Never carried a port suffix, unlike `LOCALSTACK_HOST` |
| `OVERCAST_SPLIT_HORIZON_HOSTS`   | _(none)_               | Extra comma-separated hostnames remapped to Overcast inside containers it starts (ECS tasks), so one URL is dialable from both host and container. Added to the built-in `localhost.overcast.sh`, `localhost.localstack.cloud`, `localhost.floci.io` |
| `OVERCAST_PORT`                  | `4566`                 | TCP port. LocalStack's `EDGE_PORT` is accepted as a direct compatibility alias ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) |
| `EDGE_PORT` *(alias)*            | _(none)_               | LocalStack-compatibility alias for `OVERCAST_PORT` ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) — same format, direct pass-through. Disagreeing with an explicit `OVERCAST_PORT` fails startup naming both |
| `GATEWAY_LISTEN` *(alias)*       | _(none)_               | LocalStack-compatibility alias for `OVERCAST_LISTEN` **and** `OVERCAST_PORT` together ([#1190](https://github.com/overcast-sh/overcast/issues/1190)). Accepts LocalStack's `<ip>:<port>[,<ip>:<port>...]` format: the address(es) map to `OVERCAST_LISTEN`, the port to `OVERCAST_PORT`. Every entry must share the same port — a `GATEWAY_LISTEN` naming more than one port has no single `OVERCAST_PORT` to map to and is a documented non-match (fails startup rather than picking one and dropping the other bind). Counts as an explicit bind-address setting, so it overrides the environment-dependent `OVERCAST_LISTEN` default the same way an explicit `OVERCAST_LISTEN` would |
| `OVERCAST_STATE`                 | `auto`                 | Storage backend: `auto` (default when unset), `memory`, `hybrid`, `persistent`, or `wal`. `auto` resolves to `hybrid` when a volume/bind mount or existing database is found at `OVERCAST_DATA_DIR` (or the dir was explicitly set), otherwise `memory` — see [storage.md § The auto default](./storage.md#the-auto-default). In the `overcast-slim` image and the `overcastd` binaries, `hybrid`/`persistent` are not compiled in and `auto` is always `memory`; use `wal` — see [storage.md § Builds without SQLite](./storage.md#builds-without-sqlite). LocalStack's `PERSISTENCE=1` is accepted as a compatibility alias for `persistent` ([#1190](https://github.com/overcast-sh/overcast/issues/1190)); `PERSISTENCE=0` is a no-op, leaving `auto`'s own detection in place |
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
| `OVERCAST_DATA_DIR`              | `~/.overcast/data`     | Directory for store files and other on-disk state. LocalStack's `DATA_DIR` is accepted as a direct compatibility alias ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) — setting it counts as an explicitly configured data directory for `OVERCAST_STATE=auto`'s detection, the same as `OVERCAST_DATA_DIR` itself would. The Docker images bake `/data` as their default, marked as the image's own (not user intent), so `DATA_DIR` overrides it rather than conflicting — the only `OVERCAST_*` variable the images bake at all |
| `OVERCAST_CA_DIR`                | `$OVERCAST_DATA_DIR/ca` | Where the local overcast CA lives. Separable from the data dir because the two have opposite lifetimes: state is disposable, a CA is a trust anchor you installed into this machine once. Point it at a host-owned directory (`-v ~/.overcast/data/ca:/ca:ro`) so an ephemeral container mints leaves from a root that outlives it — see [HTTPS and HTTP/2](./https.md#docker). May be read-only |
| `OVERCAST_DEFAULT_REGION`        | `us-east-1`            | Fallback region used in ARNs when not present in SigV4 header. LocalStack's `DEFAULT_REGION` is accepted as a direct compatibility alias ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) |
| `OVERCAST_ACCOUNT_ID`            | `000000000000`         | Account ID embedded in ARNs                                                          |
| `OVERCAST_LOG_LEVEL`             | `info`                 | `trace`, `debug`, `info`, `warn`, `error` — see [Log levels](#log-levels) below. LocalStack's `DEBUG=1` is accepted as a compatibility alias for `debug` ([#1190](https://github.com/overcast-sh/overcast/issues/1190)); `DEBUG=0` is a no-op |
| `OVERCAST_DEBUG`                 | `false`                | Enable `/_overcast/debug/*` endpoints                                                         |
| `OVERCAST_DEBUG_TRACE_BUFFER`    | `1000`                 | User-facing request traces always retained — the floor. Only read when `OVERCAST_DEBUG=true` |
| `OVERCAST_DEBUG_TRACE_CEILING`   | `10000`                | How far a burst may grow retention past the floor |
| `OVERCAST_DEBUG_TRACE_WINDOW`    | `1h`                   | How long traces above the floor survive before being reclaimed |
| `OVERCAST_DEBUG_TRACE_PINNED`    | `1000`                 | Traces kept because they went wrong, exempt from the floor and the window |
| `OVERCAST_DEBUG_TRACE_BYTES_MB`  | `512`                  | Retained request/response body budget. Reclaims ordinary overflow first, then the oldest kept failures; never below the floor |
| `OVERCAST_SIGV4_VALIDATE`        | `false`                | Verify SigV4 signatures (header-signed and presigned URLs) and reject invalid or expired ones with `403 InvalidSignatureException`. Signing secrets resolve through IAM user access keys and STS session credentials, falling back to the local-dev default `test`. Unsigned requests still pass through |
| `OVERCAST_ENFORCE_IAM`           | `false`                | Evaluate the calling principal's IAM policies before each request and return AWS-shaped `AccessDenied` when they do not allow it. **Off by default**; with it off nothing is evaluated and no policy is read. See [iam.md § Request-time enforcement](./services/iam.md#request-time-enforcement-opt-in) |
| `OVERCAST_ENFORCE_APIGATEWAY_THROTTLE` | `false`          | Reject API Gateway requests that exceed their usage plan's throttle or quota with AWS's `429`. Off by default: the limits are measured and reported (`GetUsage`, `apigateway:Throttled` events) but never rejected — see [API Gateway](./services/apigateway.md#usage-plan-throttling-and-quotas) |
| `OVERCAST_CFN_SYNC_WAIT_MS`      | `1000`                 | Milliseconds CloudFormation waits for fast stack provisioning before returning (`0` disables) |
| `OVERCAST_STEPFUNCTIONS_EXECUTION_TIMEOUT` | `15m`        | Runaway guard on one Step Functions execution. Executions run off the request path, so this never bounds `StartExecution` itself; a state machine's own `TimeoutSeconds` can lower it but not raise it. Exceeding it ends the execution `TIMED_OUT` with `States.Timeout` |
| `OVERCAST_TLS`                   | —                      | `auto` = serve API **and** web UI over HTTPS with a certificate minted from the local overcast CA (unlocks browser HTTP/2) — see [HTTPS and HTTP/2](./https.md) |
| `OVERCAST_TLS_CERT`              | —                      | Path to your own TLS certificate (enables HTTPS for API and web UI; mutually exclusive with `OVERCAST_TLS=auto`) |
| `OVERCAST_TLS_KEY`               | —                      | Path to the matching TLS private key                                                 |
| `OVERCAST_SHUTDOWN_TIMEOUT`      | `5s`                   | Graceful shutdown wait; also budgets the final store flush — if it can't finish in time the process exits anyway and unflushed writes replay from the pending log on next start |
| `OVERCAST_PROTOCOL_STRICT`       | `false`                | Return `415` when a request arrives in a protocol the target service does not declare, instead of attempting the decode anyway |
| `OVERCAST_DNS`                   | `true`                 | Run the built-in DNS resolver that serves the split-horizon names to the containers Overcast starts. Failing to bind the port is not fatal |
| `OVERCAST_DNS_PORT`              | `53`                   | Port for the built-in DNS resolver. Docker's `--dns` cannot express a port, so anything other than `53` is only useful for tests |
| `OVERCAST_HOT_RELOAD`            | `false`                | Umbrella switch for hot reload across every compute service — see [The inner loop](./local-dev.md) |
| `OVERCAST_LAMBDA_HOT_RELOAD`     | _(`OVERCAST_HOT_RELOAD`)_ | Per-service override: hot reload for Lambda functions                             |
| `OVERCAST_ECS_HOT_RELOAD`        | _(`OVERCAST_HOT_RELOAD`)_ | Per-service override: hot reload for ECS tasks                                    |
| `OVERCAST_EC2_VPC_STRATEGY`      | `shared`               | How VPCs map to Docker networks: `shared`, `strict`, or `remapped` (`strict` and `remapped` currently fall back to `shared` with a startup warning; `netns` is rejected) — see [Local VPCs](./cdk/local-vpc.md) |
| `OVERCAST_MCP_REMOTE_EXPOSURE`   | `false`                | **Security-relevant.** Declares that the MCP endpoint (`/_overcast/mcp`) will be reachable by non-local clients, and turns on bearer-token auth for every MCP request. Setting it `true` makes `OVERCAST_MCP_AUTH_TOKEN` mandatory — Overcast refuses to start without one. Note it does not itself change what Overcast binds: if `OVERCAST_LISTEN` exposes the port, the MCP endpoint is exposed with it, so set this (and a token) before exposing the port beyond localhost. Browser `Origin` checks (localhost origins only) are enforced on MCP regardless |
| `OVERCAST_MCP_AUTH_TOKEN`        | —                      | Bearer token every MCP request must present once set (mandatory when `OVERCAST_MCP_REMOTE_EXPOSURE=true`; setting it alone also enables the auth check). Treat it like any other credential — anyone holding it can drive the emulator through MCP |
| `OVERCAST_NETWORK`               | `overcast`             | Docker network every container Overcast starts is reachable on by name when it belongs to no VPC — the default data plane. A resource that names a VPC joins that VPC's network instead. Overcast derives a second network from this, `<name>_control`, which carries the Lambda Runtime API and the emulator endpoint; see [container networking](./dev/container-networking.md) |
| `LAMBDA_DOCKER_SOCKET`           | `/var/run/docker.sock` | Docker endpoint — Unix path or `tcp://host:port` (for DinD). The per-service socket overrides below must all address the **same** daemon: containers are attached to shared networks across service boundaries |
| `LAMBDA_RUNTIME_API_PORT`        | `9001`                 | Port Overcast exposes the Lambda Runtime API on. The addresses are not configurable and do not follow `OVERCAST_LISTEN`: Overcast binds loopback plus the one address containers on the control plane reach it at — its own address on that network when Overcast is containerised, the network's gateway on a native Linux daemon, the host's routable address on Docker Desktop |
| `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` | _(auto)_               | Max concurrent Docker-backed Lambda container starts. Unset: derived from the Docker host as `clamp(NCPU/2, 2, 8)` (each start bursts ~2 CPUs during INIT); `4` when Docker `/info` is unavailable |
| `LAMBDA_MAX_INSTANCES`           | _(auto)_               | Max Lambda containers across all functions. Unset: derived from the Docker host as `clamp(MemTotal×0.65 / 256 MiB, 4, 32)`; `25` when `/info` is unavailable |
| `LAMBDA_MAX_INSTANCES_PER_FUNCTION` | _(auto)_            | Max concurrent containers for one function. Unset: `clamp(maxInstances/2, 2, maxInstances)`; `10` when `/info` is unavailable |
| `LAMBDA_MAX_MEMORY_MB`           | _(auto)_               | Aggregate memory budget for live Lambda containers (Σ `MemorySize`, in MB). Unset: 65% of the Docker host's `MemTotal`; unlimited when `/info` is unavailable |
| `LAMBDA_MAX_WARM_INSTANCES`      | `10`                   | Idle containers kept warm per function after a burst                                 |
| `LAMBDA_SEED_RUNTIME_IMAGES`     | `false`                | Pre-pull every currently-supported Lambda runtime image at startup                   |
| `LAMBDA_INIT_TIMEOUT_SECONDS`    | `10`                   | Max seconds to wait for a Lambda runtime to finish INIT. LocalStack's `LAMBDA_RUNTIME_ENVIRONMENT_TIMEOUT` is accepted as a direct compatibility alias ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) |
| `LAMBDA_KEEP_CONTAINERS`         | `false`                | Keep stopped Lambda containers after expiry/delete (useful for debugging)            |
| `LAMBDA_TAR_CACHE_MB`            | `256`                  | In-memory cache of pre-built cold-start code and layer tars; `0` disables it         |
| `LAMBDA_PROACTIVE_INIT`          | `true`                 | Pre-initialize one execution environment once a function's configuration settles; set `false` to opt out |
| `LAMBDA_FETCH_REMOTE_LAYERS`     | `false`                | Download layers missing locally from real AWS (needs the `LAMBDA_REMOTE_AWS_*` credentials) |
| `LAMBDA_LAYER_CACHE_DIR`         | `$OVERCAST_DATA_DIR/layers` | Where layer zips are looked up and cached, named `{sha256(arn)}.zip`            |
| `LAMBDA_REMOTE_AWS_ACCESS_KEY_ID` | —                     | AWS access key ID used by `LAMBDA_FETCH_REMOTE_LAYERS`                               |
| `LAMBDA_REMOTE_AWS_SECRET_ACCESS_KEY` | —                 | AWS secret access key used by `LAMBDA_FETCH_REMOTE_LAYERS`                           |
| `LAMBDA_REMOTE_AWS_SESSION_TOKEN` | —                     | Optional AWS session token used by `LAMBDA_FETCH_REMOTE_LAYERS`                      |
| `ECS_DOCKER_SOCKET`              | _(Lambda socket)_      | Docker endpoint for ECS — Unix path or `tcp://host:port`                             |
| `ECS_KEEP_CONTAINERS`            | `false`                | Keep stopped ECS task containers after they exit                                     |
| `OVERCAST_RDS_MODE`              | `live`                 | `live` runs a real engine container per instance; `mock` is metadata-only            |
| `RDS_DOCKER_SOCKET`              | _(Lambda socket)_      | Docker endpoint for RDS — Unix path or `tcp://host:port`                             |
| `RDS_PORT_BASE`                  | `33060`                | Starting host port for RDS containers (each instance gets the next available port)   |
| `RDS_KEEP_CONTAINERS`            | `false`                | Keep stopped RDS containers after instance deletion                                  |
| `ELASTICACHE_DOCKER_SOCKET`      | _(Lambda socket)_      | Docker endpoint for ElastiCache — Unix path or `tcp://host:port`                     |
| `ELASTICACHE_PORT_BASE`          | `63790`                | Starting host port for ElastiCache engine containers                                 |
| `ELASTICACHE_KEEP_CONTAINERS`    | `false`                | Keep stopped ElastiCache containers after deletion                                   |
| `MSK_DOCKER_SOCKET`              | _(Lambda socket)_      | Docker endpoint for MSK — Unix path or `tcp://host:port`                             |
| `MSK_PORT_BASE`                  | `49092`                | Starting host port for MSK broker containers                                         |
| `MSK_KEEP_CONTAINERS`            | `false`                | Keep stopped MSK containers after cluster deletion                                   |
| `OVERCAST_EKS_MODE`              | `mock`                 | `mock` is metadata-only; `live` runs real cluster containers — see [eks.md](./services/eks.md) |
| `EKS_DOCKER_SOCKET`              | _(Lambda socket)_      | Docker endpoint for EKS — Unix path or `tcp://host:port`                             |
| `OVERCAST_EFS_MODE`              | `live`                 | `live` backs file systems with real storage (inert without Docker); `mock` is metadata-only — see [efs.md](./services/efs.md) |
| `EFS_DOCKER_SOCKET`              | _(Lambda socket)_      | Docker endpoint for EFS — Unix path or `tcp://host:port`                             |
| `OVERCAST_EFS_NFS`               | `false`                | Run one NFS-Ganesha export container per mount target (live mode only) — see [efs.md](./services/efs.md) |
| `EFS_NFS_PORT_BASE`              | `22049`                | Starting host port for the NFS export containers                                     |
| `EFS_NFS_IMAGE`                  | `registry.k8s.io/sig-storage/nfs-provisioner@sha256:…` | Digest-pinned image used for the NFS export containers               |
| `OVERCAST_ECR_REGISTRY_PORT`     | `4510`                 | Host port the shared ECR registry container asks for; `0`, or a port already taken, falls back to an ephemeral port |
| `OVERCAST_ECR_REGISTRY_PERSIST`  | `true`                 | Back the fixed-port registry with a named Docker volume, so pushed images survive a restart |
| `OVERCAST_SMTP_MOCK`             | `true`                 | Enable built-in SMTP capture server (auto-disabled when `OVERCAST_SMTP_HOST` is set) |
| `OVERCAST_SMTP_PORT`             | `1025`                 | Port for the mock SMTP server                                                        |
| `OVERCAST_SMTP_HOST`             | —                      | External SMTP relay hostname (disables the mock server)                              |
| `OVERCAST_SMTP_FROM`             | `overcast@localhost`   | Envelope From address for outbound SNS email notifications                           |
| `OVERCAST_SMTP_USERNAME`         | —                      | SMTP AUTH PLAIN username for external relay                                          |
| `OVERCAST_SMTP_PASSWORD`         | —                      | SMTP AUTH PLAIN password for external relay                                          |
| `OVERCAST_SMTP_TLS`              | `false`                | Enable implicit TLS (port 465) for external relay                                    |
| `OVERCAST_SMTP_INBOX_MAX`        | `500`                  | Maximum number of captured messages retained before eviction                         |
| `OVERCAST_INIT_ENABLED`          | `true`                 | Run init-hook scripts found in `OVERCAST_INIT_DIRS` at startup; set `false` to disable |
| `OVERCAST_INIT_DIRS`             | `/etc/localstack/init,/etc/overcast/init` | Comma-separated base directories scanned for init-hook scripts in stage subdirs (`boot.d/`, `start.d/`, `ready.d/`, `shutdown.d/`); LocalStack's layout is honoured for drop-in migration — see [Migrating from LocalStack](./migration-from-localstack.md) |
| `OVERCAST_INIT_TIMEOUT`          | `30s`                  | Per-script timeout for init hooks                                                    |
| `SERVICES` *(ignored)*           | _(none)_               | LocalStack variable recognised but with no effect ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) — Overcast runs every service, always, so there is nothing to select. Not rejected; a startup log line names it as seen |
| `LOCALSTACK_API_KEY` *(ignored)* | _(none)_               | LocalStack variable recognised but with no effect ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) — Overcast has no LocalStack Pro/auth-gated feature set to unlock. Not rejected; a startup log line names it as seen |
| `LOCALSTACK_AUTH_TOKEN` *(ignored)* | _(none)_            | Same as `LOCALSTACK_API_KEY` above ([#1190](https://github.com/overcast-sh/overcast/issues/1190))                                          |

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
| `efs`             | EFS              | `aws-efs`                                          |
| `appconfig`       | AppConfig        | `aws-appconfig`                                    |
| `appconfigdata`   | AppConfigData    | — (runtime data plane; no constructs)              |
| `secretsmanager`  | Secrets Manager  | `aws-secretsmanager`                               |
| `ssm`             | SSM              | `aws-ssm`                                          |
| `logs`            | CloudWatch Logs  | `aws-logs`                                         |
| `ses`             | SES              | `aws-ses`                                          |
| `sts`             | STS              | — (used by the CDK CLI itself)                     |
| `route53`         | Route 53         | `aws-route53`, `aws-route53-targets`               |
| `autoscaling`     | Auto Scaling     | `aws-autoscaling`, `aws-applicationautoscaling`    |
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
| `backup`          | Backup           | `aws-backup`                                       |
| `cloudtrail`      | CloudTrail       | `aws-cloudtrail`                                   |
| `eks`             | EKS              | `aws-eks`                                          |
| `elbv2`           | ELBv2            | `aws-elasticloadbalancingv2`                       |
| `msk`             | MSK              | `aws-msk`                                          |
| `organizations`   | Organizations    | — (no constructs)                                  |
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
  ghcr.io/overcast-sh/overcast:latest
```

This resolves to `hybrid` automatically because a volume is mounted at `/data`. Set
`OVERCAST_STATE` explicitly (e.g. `-e OVERCAST_STATE=persistent`) if you need a different
backend than what `auto` would pick.

> [!IMPORTANT]
> **The `overcast-slim` image and the `overcastd` binaries are built without SQLite**, so
> `hybrid` and `persistent` do not exist in them: `auto` always resolves to `memory` there
> and the mounted volume above would be ignored — state is lost on every restart, with no
> error. Add `-e OVERCAST_STATE=wal` (the one durable backend those artifacts do have), or
> use the full `ghcr.io/overcast-sh/overcast` image. See
> [storage.md § Builds without SQLite](./storage.md#builds-without-sqlite).

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
  ghcr.io/overcast-sh/overcast:latest
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

- **`GET /_overcast/health`** — the `storage` object shows the resolved default backend (`default`), what was actually configured (`configured` — e.g. `auto`, when `default` was resolved rather than explicitly set), per-service overrides, and persistent backend health including pending hybrid writes when available.
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

Running in Docker? Still those two commands — mount the CA the first one
created, read-only, so the container mints certificates from a root this
machine already trusts and recreating it never costs you another approval
prompt:

```bash
overcast https enable            # once per machine
docker run -d -e OVERCAST_TLS=auto \
  -e OVERCAST_CA_DIR=/ca -v ~/.overcast/data/ca:/ca:ro \
  -p 4566:4566 -p 4567:4567 ghcr.io/overcast-sh/overcast:latest
```

No `overcast` on the host? The daemon can mint its own CA and serve the
certificate at `/_overcast/ca.pem` for `overcast https enable --endpoint
https://localhost:4566` to install — keep `OVERCAST_CA_DIR` on a named volume
so it survives recreation. See [HTTPS and HTTP/2](./https.md#docker).

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
  ghcr.io/overcast-sh/overcast:latest
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
    image: ghcr.io/overcast-sh/overcast:latest
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

Set `OVERCAST_DEBUG=true` to enable the `/_overcast/debug` namespace and request tracing.
Every response carries a request ID (`x-amzn-requestid` for most services,
`x-amz-request-id` for S3) which can be used to look up the full trace:

| Endpoint                    | Method | Description                                           |
| --------------------------- | ------ | ----------------------------------------------------- |
| `/_overcast/health`                  | GET    | Basic health check (always enabled)                   |
| `/_overcast/events`                  | GET    | SSE stream of internal events (always enabled)        |
| `/_overcast/metrics`                 | GET    | Go runtime memory/GC/goroutine stats (always enabled) |
| `/_overcast/topology`                | GET    | Full cross-region resource graph (always enabled)     |
| `/_overcast/preflight/region`        | GET    | Whether resources of a `?kind=` exist in some region other than the caller's, and how many (always enabled). Answers with nothing to report when the caller's own region has any — it explains an empty list, it is not a census |
| `/_overcast/reset`                   | POST   | Wipe all state (always enabled — not expensive or leaky like the rest of this namespace) |
| `/_overcast/reset/{service}`         | POST   | Wipe state for one service (always enabled)           |
| `/_overcast/debug/health`            | GET    | Detailed: uptime, services, state backend and health  |
| `/_overcast/debug/config`            | GET    | Effective configuration (secrets redacted)            |
| `/_overcast/debug/state`             | GET    | Every namespace and its keys (no values)              |
| `/_overcast/debug/state/{namespace}` | GET    | Paginated key/value pages for one namespace (`?after=` cursor, `?limit=` ≤ 5000, default 500); `?key=` fetches one raw value |
| `/_overcast/debug/metrics`           | GET    | Storage diagnostics: flush history, seed duration, pending-log size; `?includeRowCounts=true` adds per-namespace row counts |
| `/_overcast/debug/pprof/`            | GET    | Go pprof index (goroutine, heap, CPU profiles, etc.)  |
| `/_overcast/debug/trace/{requestId}` | GET    | Full trace for one request: bodies, headers, log entries, AWS errors |
| `/_overcast/debug/traces`            | GET    | Paginated list of recent traces; filterable by `?service=`, `?method=`, `?path=`, `?status=`, `?search=` |
| `/_overcast/debug/traces/count`      | GET    | Current trace buffer count and capacity               |
| `/_overcast/debug/traces/search`     | GET    | Free-text search over retained traces                 |
| `/_overcast/debug/ec2/vpcs`          | GET    | EC2 VPC-to-Docker-network wiring, for debugging VPC-backed networking. Service-specific debug routes live under `/_overcast/debug/<service>/…`; this is the only one today |

Traces are retained under three rules, so that the request explaining a failure is
still there when you go looking, without your having configured anything first:

1. The newest `OVERCAST_DEBUG_TRACE_BUFFER` traces (default 1000) are always kept.
2. Beyond that, a burst is kept for `OVERCAST_DEBUG_TRACE_WINDOW` (default 1h), up to
   `OVERCAST_DEBUG_TRACE_CEILING` (default 10000). A `cdk deploy` pushes thousands of
   requests through in a couple of minutes, and the floor alone would keep the
   rollback traffic and discard the error that started it.
3. Traces that went wrong — a 4xx/5xx, an AWS error code, or a failed internal hop —
   are exempt from both, up to `OVERCAST_DEBUG_TRACE_PINNED` (default 1000). They are
   not exempt from the memory budget: under real pressure the oldest kept failures are
   surrendered last, after every ordinary trace above the floor.

Internal polling (health checks, the console's own requests) is retained separately
and can never evict a request you made. A trace records each
internal service-to-service hop a request made, and captures a goroutine stack
for the first 20 hops plus the first 20 hops that failed — a CloudFormation or
CDK deploy dispatches hundreds of hops through one trace, and a stack for every
one of them would cost more than it tells you. Hops past that budget show
"Stack trace not captured" in the console.

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

The full image (`ghcr.io/overcast-sh/overcast`) includes a web management console
accessible at **http://localhost:4567** (configurable via `OVERCAST_UI_PORT`
env var / `--ui-port` flag; `0` disables it).

The console provides:

- Dashboard with service cards and real-time status
- Service-specific UI for all implemented services (S3 browser, SQS message inspector, DynamoDB item editor, Lambda test/invoke, etc.)
- **Live activity feed** — a real-time stream of API calls as they happen across all services, showing the operation, resource, status code, and latency. Useful for understanding what your application is actually doing against the emulated APIs.
- **Inbox** — a built-in capture inbox for all outbound email and SMS messages generated by SES, SNS, and Cognito. Instead of messages disappearing into the void (or requiring a real SMTP server), the Inbox collects them and lets you browse, search, and inspect each message's headers and body. This makes it easy to verify that your application sends the right emails during local development and testing — no third-party mail catcher needed.
- Topology map showing cross-service relationships
- Real-time updates via SSE

The web UI is non-critical — the AWS API on port 4566 does not depend on it.
It is served in-process by the same binary (there is no separate console
server to start or supervise), and `OVERCAST_UI_PORT=0` / `--ui-port 0` turns
it off entirely.

> [!TIP]
> If the console feels sluggish or stops responding to clicks while many
> Lambdas run or transfers are in flight, you are hitting the browser's
> 6-connection HTTP/1.1 limit — the live feed and progress streams are
> holding the sockets. Serve the console over HTTPS to unlock HTTP/2 and
> keep it responsive under any load: see [HTTPS and HTTP/2](./https.md).

---

## Troubleshooting

### Startup preflight

A handful of environment mistakes cost real time on this project because
they don't look like environment mistakes — Overcast answers normally, and
the symptom (an empty console list, a container that never starts, data that
isn't where you left it) reads exactly like a bug in the emulator. Where
Overcast can tell, it says so: one actionable `WARN`, the moment the symptom
appears, never a wall of startup output and never on a healthy setup.

| Message names...                                                         | Means                                                                                                                    |
| -------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| `No stacks in <region>. There are N in <region>.`                        | The selected region has nothing, but another region does — check `AWS_REGION`/`AWS_DEFAULT_REGION` against what you expect. Served on demand at [`/_overcast/preflight/region`](#debug-endpoints), not logged at startup. |
| `Docker is not reachable for: ...`                                        | One or more container-backed services (ECS, RDS, Lambda, MSK, etc.) couldn't reach a Docker daemon and will run metadata-only — container creates fail instead of starting anything. Start Docker, or check the socket is readable by this user. |
| `the API is published on a different host port than it listens on`      | The container remapped its port (e.g. `-p 4580:4566`). Overcast already rewrites the common case (queue URLs, split-horizon hostnames); publish 1:1 instead if something still compares the port literally (a Cognito token's `iss`). |
| `a request arrived addressed to "..." — a real AWS hostname`             | A hosts-file entry, DNS override, or proxy is sending `*.amazonaws.com`-bound traffic to Overcast. Point `AWS_ENDPOINT_URL` at Overcast explicitly, or remove the redirect if that wasn't intentional. |
| `OVERCAST_HOSTNAME=... does not resolve` / `virtual-hosted-style addressing will not work` | This host's resolver can't resolve `OVERCAST_HOSTNAME` (or its subdomains) — breaks virtual-hosted S3 and `cdk deploy` asset publishing. The message names the fix for this host. |
| `this run is memory-only, but an existing Overcast database was found`   | `OVERCAST_STATE=memory` (explicitly, or a `-tags nosqlite` build resolving `auto` to memory regardless) is ignoring a database that already has data in it. Set `OVERCAST_STATE=auto` (or `hybrid`/`wal`) to use it instead. |
| `Running in memory-only mode (auto-detected)`                             | No volume is mounted and no `OVERCAST_DATA_DIR` is set — state won't survive a restart. Expected outside of a persistent setup; see [Persistence](#persistence). |

A port Overcast wants to bind that's already taken isn't on this list because
it needs no diagnosis: startup fails immediately with the OS's own `bind:
address already in use`, rather than falling back silently.
