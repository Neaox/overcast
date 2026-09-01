---
title: "Configuration reference"
description: "Every OVERCAST_* and LAMBDA_*/ECS_*/RDS_*/... environment variable Overcast reads, with defaults, LocalStack compatibility aliases, and service names for per-service overrides."
section: "Reference"
tags:
  - docs
  - configuration
  - environment
  - reference
  - variables
---

# Configuration reference

All configuration is via environment variables — no config file required. The
table below is authoritative; find a variable with Ctrl+F rather than reading
top to bottom.

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
[Web management console](./README.md#web-management-console); everything the
Go emulator itself reads is below.

| Variable                         | Default                | Description                                                                          |
| --------------------------------- | ---------------------- | ------------------------------------------------------------------------------------ |
| `OVERCAST_LISTEN`                | `0.0.0.0` containerised, `127.0.0.1` native (see [#761](https://github.com/overcast-sh/overcast/issues/761)) | Hostname or IP to bind the AWS API to (LocalStack's `GATEWAY_LISTEN` idiom — not the same thing as `OVERCAST_HOSTNAME` below). Accepts a comma-separated list to bind several, e.g. `127.0.0.1,172.17.0.1` to be reachable from this machine and from its containers over the Docker bridge without being on any network the machine is attached to. A wildcard cannot be combined with a specific address. The web console binds the first address only. An explicit value always wins over the default, in either direction (e.g. `OVERCAST_LISTEN=0.0.0.0` restores the old native reach from a VM or another machine). Renamed from `OVERCAST_HOST`, which has been removed: a leftover `OVERCAST_HOST` fails at startup naming this variable instead of being silently ignored |
| `OVERCAST_HOSTNAME`              | `localhost`            | Hostname embedded in client-facing URLs (SQS queue URLs, Lambda function URLs, API Gateway `apiEndpoint`, AppSync DNS names, CloudFront domain names). **Set it to `localhost.overcast.sh`** unless you are offline: every `*.localhost.overcast.sh` name resolves to `127.0.0.1` on every OS, which plain `localhost` does not on Windows. See [networking.md](./networking.md). LocalStack's `LOCALSTACK_HOST` is accepted as a compatibility alias (see the row below) |
| `LOCALSTACK_HOST` *(alias)*      | _(none)_               | LocalStack-compatibility alias for `OVERCAST_HOSTNAME` ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) — Overcast is meant to be a drop-in replacement for LocalStack, so its documented settings are honoured directly rather than requiring a rename. Accepts LocalStack's `hostname[:port]` format (e.g. `localhost.localstack.cloud:4566`): the hostname part maps to `OVERCAST_HOSTNAME`, and a port part is accepted only if it matches `OVERCAST_PORT`. Setting both `OVERCAST_HOSTNAME` and `LOCALSTACK_HOST` to the *same* hostname is fine (the natural result of migrating a compose file line by line); setting them to *different* hostnames, or a `LOCALSTACK_HOST` port that disagrees with `OVERCAST_PORT`, fails startup naming both rather than silently preferring one. A startup log line names the alias whenever it was recognised |
| `HOSTNAME_EXTERNAL` *(alias)*    | _(none)_               | Legacy LocalStack name `LOCALSTACK_HOST` replaced; also accepted as a compatibility alias for `OVERCAST_HOSTNAME` ([#1190](https://github.com/overcast-sh/overcast/issues/1190)), chained after `LOCALSTACK_HOST` — all three spellings must agree when more than one is set. Never carried a port suffix, unlike `LOCALSTACK_HOST` |
| `OVERCAST_SPLIT_HORIZON_HOSTS`   | _(none)_               | Extra comma-separated hostnames remapped to Overcast inside containers it starts (ECS tasks), so one URL is dialable from both host and container. Added to the built-in `localhost.overcast.sh`, `localhost.localstack.cloud`, `localhost.floci.io` |
| `OVERCAST_PORT`                  | `4566`                 | TCP port. LocalStack's `EDGE_PORT` is accepted as a direct compatibility alias ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) |
| `EDGE_PORT` *(alias)*            | _(none)_               | LocalStack-compatibility alias for `OVERCAST_PORT` ([#1190](https://github.com/overcast-sh/overcast/issues/1190)) — same format, direct pass-through. Disagreeing with an explicit `OVERCAST_PORT` fails startup naming both |
| `GATEWAY_LISTEN` *(alias)*       | _(none)_               | LocalStack-compatibility alias for `OVERCAST_LISTEN` **and** `OVERCAST_PORT` together ([#1190](https://github.com/overcast-sh/overcast/issues/1190)). Accepts LocalStack's `<ip>:<port>[,<ip>:<port>...]` format: the address(es) map to `OVERCAST_LISTEN`, the port to `OVERCAST_PORT`. Every entry must share the same port — a `GATEWAY_LISTEN` naming more than one port has no single `OVERCAST_PORT` to map to and is a documented non-match (fails startup rather than picking one and dropping the other bind). Counts as an explicit bind-address setting, so it overrides the environment-dependent `OVERCAST_LISTEN` default the same way an explicit `OVERCAST_LISTEN` would |
| `OVERCAST_STATE`                 | `auto`                 | Storage backend: `auto` (default when unset), `memory`, `hybrid`, `persistent`, or `wal`. See [Persistence](./persistence.md) for how `auto` picks and the durability tradeoffs |
| `OVERCAST_STATE_<SERVICE>`       | _(global)_             | Per-service backend override, e.g. `OVERCAST_STATE_S3=memory` — see [Persistence § Per-service storage overrides](./persistence.md#per-service-storage-overrides) |
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
| `OVERCAST_DEBUG`                 | `false`                | Enable `/_overcast/debug/*` endpoints — see [Debug endpoints](./debug-endpoints.md)  |
| `OVERCAST_DEBUG_TRACE_BUFFER`    | `1000`                 | User-facing request traces always retained — the floor. Only read when `OVERCAST_DEBUG=true`. See [Debug endpoints § Trace retention](./debug-endpoints.md#trace-retention) |
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
| `OVERCAST_EC2_VPC_STRATEGY`      | `shared`               | How VPCs map to Docker networks when their CIDRs overlap: `shared`, `strict`, or `remapped` are all implemented; `netns` fails startup naming the strategies that exist — see [ec2.md § Advanced: VPC networking strategies](./services/ec2.md#advanced-vpc-networking-strategies) |
| `OVERCAST_MCP_REMOTE_EXPOSURE`   | `false`                | **Security-relevant.** Declares that the MCP endpoint (`/_overcast/mcp`) will be reachable by non-local clients, and turns on bearer-token auth for every MCP request. Setting it `true` makes `OVERCAST_MCP_AUTH_TOKEN` mandatory — Overcast refuses to start without one. Note it does not itself change what Overcast binds: if `OVERCAST_LISTEN` exposes the port, the MCP endpoint is exposed with it, so set this (and a token) before exposing the port beyond localhost. Browser `Origin` checks (localhost origins only) are enforced on MCP regardless |
| `OVERCAST_MCP_AUTH_TOKEN`        | —                      | Bearer token every MCP request must present once set (mandatory when `OVERCAST_MCP_REMOTE_EXPOSURE=true`; setting it alone also enables the auth check). Treat it like any other credential — anyone holding it can drive the emulator through MCP |
| `OVERCAST_NETWORK`               | `overcast`             | Docker network every container Overcast starts is reachable on by name when it belongs to no VPC — the default data plane. A resource that names a VPC joins that VPC's network instead. Overcast derives a second network from this, `<name>_control`, which carries the Lambda Runtime API and the emulator endpoint |
| `LAMBDA_DOCKER_SOCKET`           | `/var/run/docker.sock` (Linux/macOS), `npipe:////./pipe/docker_engine` (Windows) | Docker endpoint — Unix path, Windows named pipe, or `tcp://host:port` (for DinD). The per-service socket overrides below must all address the **same** daemon: containers are attached to shared networks across service boundaries |
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

## Service names

Every service listed below always runs — there is no way to switch one off, and
nothing to configure to get one. The names matter for one thing: they are what
the per-service storage override `OVERCAST_STATE_<SERVICE>` is keyed by, in
upper case. CloudWatch Logs is `logs`, so its override is `OVERCAST_STATE_LOGS`
— not `OVERCAST_STATE_CLOUDWATCH_LOGS`, which names nothing and is rejected at
startup.

Each name is the service's AWS CLI name, which for several services matches
neither the display name nor the `aws-cdk-lib` module you would import. The CDK
column is there because that is the mapping people most often need to make.

For per-service endpoint coverage, follow the doc links in
[Documentation § Services](./README.md#services).

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

## Log levels

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
