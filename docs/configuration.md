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
| Variable                         | Default                | Description                                                                          |
| --------------------------------- | ---------------------- | ------------------------------------------------------------------------------------ |
| `OVERCAST_UI_PORT`               | `4567`                 | Port for the web management console (`--ui-port`); `0` disables it. Falls back to an ephemeral port when 4567 is taken. Full binary and full image only |
| `OVERCAST_LISTEN`                | `0.0.0.0` containerised, `127.0.0.1` native | Address(es) to bind the AWS API to; comma-separate to bind several. See [Bind address and port](#bind-address-and-port) |
| `OVERCAST_HOSTNAME`              | `localhost`            | Hostname embedded in client-facing URLs. **Set it to `localhost.overcast.sh`** unless you are offline — see [Networking](./networking.md) |
| `LOCALSTACK_HOST` *(alias)*      | _(none)_               | LocalStack alias for `OVERCAST_HOSTNAME`; accepts `hostname[:port]`. See [LocalStack aliases](#localstack-aliases) |
| `HOSTNAME_EXTERNAL` *(alias)*    | _(none)_               | Legacy LocalStack alias for `OVERCAST_HOSTNAME`, chained after `LOCALSTACK_HOST`. Never carried a port suffix |
| `OVERCAST_SPLIT_HORIZON_HOSTS`   | _(none)_               | Extra comma-separated hostnames remapped to Overcast inside the containers it starts, on top of the three built-in ones |
| `OVERCAST_PORT`                  | `4566`                 | TCP port for the AWS API |
| `EDGE_PORT` *(alias)*            | _(none)_               | LocalStack alias for `OVERCAST_PORT` |
| `GATEWAY_LISTEN` *(alias)*       | _(none)_               | LocalStack alias for `OVERCAST_LISTEN` **and** `OVERCAST_PORT` together, in `<ip>:<port>[,...]` form. See [Bind address and port](#bind-address-and-port) |
| `OVERCAST_STATE`                 | `auto`                 | Storage backend: `auto`, `memory`, `hybrid`, `persistent` or `wal` — see [Storage and persistence](./storage.md) for how `auto` picks and the durability tradeoffs |
| `OVERCAST_STATE_<SERVICE>`       | _(global)_             | Per-service backend override, e.g. `OVERCAST_STATE_S3=memory` — see [Storage and persistence § Per-service storage overrides](./storage.md#per-service-storage-overrides) |
| `OVERCAST_HYBRID_FLUSH_INTERVAL` | `5s`                   | How often the hybrid backend flushes in-memory state to disk                         |
| `OVERCAST_HYBRID_SYNC`           | `interval`             | Hybrid pending-log fsync policy: `always`, `interval`, or `never`                    |
| `OVERCAST_HYBRID_SYNC_INTERVAL`  | `100ms`                | Periodic fsync interval used when `OVERCAST_HYBRID_SYNC=interval`                    |
| `OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD` | `10000`         | Unflushed-write count that triggers an early hybrid flush ahead of the timer (`<= 0` disables) |
| `OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD`  | `8388608`       | Approximate unflushed-write bytes that trigger an early hybrid flush (default 8 MiB; `<= 0` disables) |
| `OVERCAST_HYBRID_MAINTENANCE_INTERVAL`  | `5m`            | How often the hybrid backend runs background SQLite housekeeping (passive WAL checkpoint + conditional incremental vacuum) |
| `OVERCAST_WAL_FSYNC`             | `interval`             | WAL fsync policy: `always`, `interval`, or `never`                                   |
| `OVERCAST_WAL_FSYNC_INTERVAL`    | `100ms`                | Periodic fsync interval used when `OVERCAST_WAL_FSYNC=interval`                      |
| `OVERCAST_WAL_MAX_LOG_BYTES`     | `67108864`             | WAL log compaction threshold in bytes (default 64 MiB)                               |
| `OVERCAST_DATA_DIR`              | `~/.overcast/data`     | Directory for store files and other on-disk state; the Docker images bake `/data`. LocalStack's `DATA_DIR` is an alias, and setting either counts as an explicit data directory for `OVERCAST_STATE=auto` |
| `OVERCAST_CA_DIR`                | `$OVERCAST_DATA_DIR/ca` | Where the local CA lives — separable from the data dir because a CA outlives disposable state. May be read-only; see [HTTPS § Docker](./https.md#docker) |
| `OVERCAST_DEFAULT_REGION`        | `us-east-1`            | Fallback region used in ARNs when the SigV4 header carries none. LocalStack's `DEFAULT_REGION` is an alias |
| `OVERCAST_ACCOUNT_ID`            | `000000000000`         | Account ID embedded in ARNs                                                          |
| `OVERCAST_LOG_LEVEL`             | `info`                 | `trace`, `debug`, `info`, `warn`, `error` — see [Log levels](#log-levels). LocalStack's `DEBUG=1` is an alias for `debug` |
| `OVERCAST_DEBUG`                 | `false`                | Enable `/_overcast/debug/*` endpoints — see [Debug endpoints](./debug-endpoints.md)  |
| `OVERCAST_DEBUG_TRACE_BUFFER`    | `1000`                 | Request traces always retained — the floor. Only read when `OVERCAST_DEBUG=true`; see [Debug endpoints § Trace retention](./debug-endpoints.md#trace-retention) |
| `OVERCAST_DEBUG_TRACE_CEILING`   | `10000`                | How far a burst may grow retention past the floor |
| `OVERCAST_DEBUG_TRACE_WINDOW`    | `1h`                   | How long traces above the floor survive before being reclaimed |
| `OVERCAST_DEBUG_TRACE_PINNED`    | `1000`                 | Traces kept because they went wrong, exempt from the floor and the window |
| `OVERCAST_DEBUG_TRACE_BYTES_MB`  | `512`                  | Retained request/response body budget. Ordinary overflow is reclaimed first, then the oldest kept failures; never below the floor |
| `OVERCAST_SERVICE_METRICS`       | `auto`                 | Whether emulated services record CloudWatch metrics for their own activity: `auto` (today identical to `enabled`), `enabled`, or `disabled`. `disabled` stops that automatic collection; `PutMetricData` from your own code is unaffected |
| `OVERCAST_SIGV4_VALIDATE`        | `false`                | Verify SigV4 signatures (header-signed and presigned) and reject invalid or expired ones with `403 InvalidSignatureException`. Unsigned requests still pass through |
| `OVERCAST_ENFORCE_IAM`           | `false`                | Evaluate the calling principal's IAM policies and return AWS-shaped `AccessDenied` when they do not allow the request — see [IAM § Request-time enforcement](./services/iam.md#request-time-enforcement-opt-in) |
| `OVERCAST_ENFORCE_APIGATEWAY_THROTTLE` | `false`          | Reject API Gateway requests over their usage plan's throttle or quota with `429`. Off by default: the limits are measured and reported, never enforced — see [API Gateway](./services/apigateway.md#usage-plan-throttling-and-quotas) |
| `OVERCAST_CFN_SYNC_WAIT_MS`      | `1000`                 | Milliseconds CloudFormation waits for fast stack provisioning before returning (`0` disables) |
| `OVERCAST_STEPFUNCTIONS_EXECUTION_TIMEOUT` | `15m`        | Runaway guard on one execution, never on `StartExecution` itself. A state machine's own `TimeoutSeconds` can lower it but not raise it |
| `OVERCAST_TLS`                   | —                      | `auto` = serve API **and** web console over HTTPS with a certificate minted from the local Overcast CA (unlocks browser HTTP/2) — see [HTTPS and HTTP/2](./https.md) |
| `OVERCAST_TLS_CERT`              | —                      | Path to your own TLS certificate (enables HTTPS for API and web console; mutually exclusive with `OVERCAST_TLS=auto`) |
| `OVERCAST_TLS_KEY`               | —                      | Path to the matching TLS private key                                                 |
| `OVERCAST_SHUTDOWN_TIMEOUT`      | `5s`                   | Graceful shutdown wait, which also budgets the final store flush. Nothing is lost when it runs out — unflushed writes replay from the pending log |
| `OVERCAST_PROTOCOL_STRICT`       | `false`                | Return `415` when a request arrives in a protocol the target service does not declare, instead of attempting the decode anyway |
| `OVERCAST_DNS`                   | `true`                 | Run the built-in DNS resolver that serves the split-horizon names to the containers Overcast starts. Failing to bind the port is not fatal |
| `OVERCAST_DNS_PORT`              | `53`                   | Port for the built-in DNS resolver. Docker's `--dns` cannot express a port, so anything other than `53` is only useful for tests |
| `OVERCAST_HOT_RELOAD`            | `false`                | Umbrella switch for hot reload across every compute service — see [The inner loop](./local-dev.md) |
| `OVERCAST_LAMBDA_HOT_RELOAD`     | _(`OVERCAST_HOT_RELOAD`)_ | Per-service override: hot reload for Lambda functions                             |
| `OVERCAST_ECS_HOT_RELOAD`        | _(`OVERCAST_HOT_RELOAD`)_ | Per-service override: hot reload for ECS tasks                                    |
| `OVERCAST_EC2_VPC_STRATEGY`      | `shared`               | How VPCs map to Docker networks when their CIDRs overlap: `shared`, `strict` or `remapped` — see [EC2 limitations § VPC networking strategies](./services/ec2/limitations.md#vpc-networking-strategies) |
| `OVERCAST_MCP_REMOTE_EXPOSURE`   | `false`                | **Security-relevant.** Declares that `/_overcast/mcp` will be reachable by non-local clients, and requires `OVERCAST_MCP_AUTH_TOKEN`. See [Exposing MCP](#exposing-mcp) |
| `OVERCAST_MCP_AUTH_TOKEN`        | —                      | Bearer token every MCP request must present once set. Treat it like any other credential |
| `OVERCAST_NETWORK`               | `overcast`             | Docker network every container Overcast starts joins when it belongs to no VPC. Overcast derives `<name>_control` from it for the Lambda Runtime API — see [Networking](./networking.md) |
| `OVERCAST_VPC_EGRESS`            | `open`                 | Whether the containers Overcast starts reach anything outside this machine: `open` (all do), `none` (none do; the Lambda Runtime API still works), `routed` (each subnet gets what its route table says — a `0.0.0.0/0` route to an internet or NAT gateway grants egress, no default route withholds it; **run Overcast in a container**, since natively on Docker Desktop it cannot withhold and reports `routed-egress-not-enforced`) — see [Egress modes](./networking.md#egress-modes) |
| `OVERCAST_VPC_EGRESS_POOL`       | `198.18.0.0/16`        | IPv4 range the per-VPC egress networks of `routed` are carved from, one `/24` each, so they never draw on Docker's ~31-network default address pools. `/8` to `/24`; the default supports 256 VPCs with egress — see [The address-pool ceiling](./networking.md#the-address-pool-ceiling) |
| `OVERCAST_CONTROL_PLANE_INTERNAL` | `auto`                | **Deprecated**, use `OVERCAST_VPC_EGRESS`. Pins whether `<name>_control` alone is created `--internal`: `auto`, `true`, `false`. Still honoured; setting it logs a deprecation notice — see [Control-plane isolation](./networking.md#control-plane-isolation) |
| `DOCKER_HOST`                    | —                      | Docker's own variable, read when `LAMBDA_DOCKER_SOCKET` is unset — the one Colima, Rancher Desktop, Podman and rootless Docker tell you to set. `unix://`, `tcp://`, `npipe://` and `http://` are dialable; `ssh://` and `https://` are not, and warn |
| `LAMBDA_DOCKER_SOCKET`           | _(`DOCKER_HOST`, else `/var/run/docker.sock` on Linux/macOS, `npipe:////./pipe/docker_engine` on Windows)_ | Docker endpoint — Unix path, Windows named pipe, or `tcp://host:port` for DinD. Every per-service socket override below must address the **same** daemon |
| `LAMBDA_RUNTIME_API_PORT`        | `9001`                 | Port of the shared Lambda Runtime API listener; `0` = ephemeral. A taken default falls back to an ephemeral port — see [Running two instances on one host](#running-two-instances-on-one-host) |
| `LAMBDA_RUNTIME_API_HOST`        | `auto`                 | Address Lambda containers dial for the Runtime API. `auto` establishes it by having a throwaway container connect to each candidate in turn, best first, and remembers the answer per Docker daemon and control plane. Set a bare address — usually `host.docker.internal`, or an IP — to pin it and skip the probe; a scheme, port or path is rejected at startup (the port is `LAMBDA_RUNTIME_API_PORT`, and the two are joined). See [Containers cannot reach the Runtime API](./services/lambda/troubleshooting.md#containers-cannot-reach-the-runtime-api) |
| `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` | _(auto)_               | Max concurrent Docker-backed Lambda container starts. Unset: derived from the Docker host as `clamp(NCPU/2, 2, 8)` (each start bursts ~2 CPUs during INIT); `4` when Docker `/info` is unavailable |
| `LAMBDA_MAX_INSTANCES`           | _(auto)_               | Max Lambda containers across all functions. Unset: derived from the Docker host as `clamp(MemTotal×0.65 / 256 MiB, 4, 32)`; `25` when `/info` is unavailable |
| `LAMBDA_MAX_INSTANCES_PER_FUNCTION` | _(auto)_            | Max concurrent containers for one function. Unset: `clamp(maxInstances/2, 2, maxInstances)`; `10` when `/info` is unavailable |
| `LAMBDA_MAX_MEMORY_MB`           | _(auto)_               | Aggregate memory budget for live Lambda containers (Σ `MemorySize`, in MB). Unset: 65% of the Docker host's `MemTotal`; unlimited when `/info` is unavailable |
| `LAMBDA_MAX_WARM_INSTANCES`      | `10`                   | Idle containers kept warm per function after a burst                                 |
| `LAMBDA_SEED_RUNTIME_IMAGES`     | `false`                | Pre-pull every currently-supported Lambda runtime image at startup                   |
| `LAMBDA_INIT_TIMEOUT_SECONDS`    | `10`                   | Max seconds to wait for a Lambda runtime to finish INIT. LocalStack's `LAMBDA_RUNTIME_ENVIRONMENT_TIMEOUT` is an alias |
| `LAMBDA_KEEP_CONTAINERS`         | `false`                | Keep stopped Lambda containers after expiry/delete (useful for debugging)            |
| `LAMBDA_TAR_CACHE_MB`            | `256`                  | In-memory cache of pre-built cold-start code and layer tars; `0` disables it         |
| `LAMBDA_PROACTIVE_INIT`          | `true`                 | Pre-initialise one execution environment once a function's configuration settles; set `false` to opt out |
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
| `OVERCAST_SMTP_PORT`             | `1025`                 | Port for the mock SMTP server; `0` = ephemeral. A taken default falls back to an ephemeral port — see [Running two instances on one host](#running-two-instances-on-one-host) |
| `OVERCAST_SMTP_HOST`             | —                      | External SMTP relay hostname (disables the mock server)                              |
| `OVERCAST_SMTP_FROM`             | `overcast@localhost`   | Envelope From address for outbound SNS email notifications                           |
| `OVERCAST_SMTP_USERNAME`         | —                      | SMTP AUTH PLAIN username for external relay                                          |
| `OVERCAST_SMTP_PASSWORD`         | —                      | SMTP AUTH PLAIN password for external relay                                          |
| `OVERCAST_SMTP_TLS`              | `false`                | Enable implicit TLS (port 465) for external relay                                    |
| `OVERCAST_SMTP_INBOX_MAX`        | `500`                  | Maximum number of captured messages retained before eviction                         |
| `OVERCAST_INIT_ENABLED`          | `true`                 | Run init-hook scripts found in `OVERCAST_INIT_DIRS` at startup; set `false` to disable |
| `OVERCAST_INIT_DIRS`             | `/etc/localstack/init,/etc/overcast/init` | Comma-separated base directories scanned for init-hook scripts in `boot.d/`, `start.d/`, `ready.d/` and `shutdown.d/` — see [Migrating from LocalStack](./migration-from-localstack.md#init-hooks) |
| `OVERCAST_INIT_TIMEOUT`          | `30s`                  | Per-script timeout for init hooks                                                    |
| `SERVICES` *(ignored)*           | _(none)_               | LocalStack variable read and logged, with no effect: every service always runs |
| `LOCALSTACK_API_KEY` *(ignored)* | _(none)_               | LocalStack variable read and logged, with no effect: nothing here is auth-gated |
| `LOCALSTACK_AUTH_TOKEN` *(ignored)* | _(none)_            | Same as `LOCALSTACK_API_KEY` |

## Bind address and port

`OVERCAST_LISTEN` accepts a comma-separated list, so one instance can be
reachable from this machine *and* from its containers over the Docker bridge
without being on any network the machine is attached to:

```bash
OVERCAST_LISTEN=127.0.0.1,172.17.0.1 overcast serve
```

A wildcard cannot be combined with a specific address, and the web console binds
the first address only. An explicit value always wins over the
environment-dependent default in either direction — `OVERCAST_LISTEN=0.0.0.0`
restores the old native reach from a VM or another machine. `OVERCAST_HOST` was
renamed to this and removed; a leftover one fails startup naming the replacement
rather than being silently ignored.

`GATEWAY_LISTEN` maps to `OVERCAST_LISTEN` and `OVERCAST_PORT` together and
counts as an explicit bind-address setting. Every entry must share one port: a
value naming two has no single `OVERCAST_PORT` to become, so startup fails
rather than dropping a bind.

## Running two instances on one host

Move the AWS API port and, if state is persistent, the data directory. The
listeners Overcast binds for itself get out of the way on their own; the ports
it publishes for database-style containers do not, so move those bases too if
both instances will run them:

```bash
OVERCAST_PORT=4576 OVERCAST_DATA_DIR=~/.overcast/second RDS_PORT_BASE=34060 overcast serve
```

| Listener           | Default | When the default is taken                                        |
| ------------------ | ------- | ---------------------------------------------------------------- |
| AWS API            | `4566`  | Startup fails — set `OVERCAST_PORT`                              |
| Web console        | `4567`  | Falls back to an ephemeral port, logged at startup               |
| Lambda Runtime API | `9001`  | Falls back to an ephemeral port, logged at startup               |
| SMTP capture       | `1025`  | Falls back to an ephemeral port, logged at startup               |
| ECR registry       | `4510`  | Falls back to an ephemeral port                                  |
| Container DNS      | `53`    | The second instance runs without its resolver — see below        |

The fallbacks are safe because nothing is told the default port: each Lambda
execution environment is handed its own per-container Runtime API address, and
the mailer that feeds the Inbox learns the address the SMTP server actually
bound. The console prints its port, and Lambda and the Inbox keep working.

`RDS_PORT_BASE`, `ELASTICACHE_PORT_BASE`, `MSK_PORT_BASE` and
`EFS_NFS_PORT_BASE` are different: each instance hands out ports above its base
from its own records, without asking the host, so two instances with the same
base both offer their first database the same port and the second one fails to
start the container. Give the second instance bases of its own.

A port you set yourself is pinned, and a pinned port that is taken is not
replaced. For the web console that stops startup. The Lambda Runtime API and
SMTP capture start degraded instead: a warning at startup names the variable
to change, Lambda invocations fail until it is fixed — as do SES, SNS and
Cognito mail — and `GET /_overcast/health` reports `status: degraded` with the
failed listener, its bind error and the fix under `listeners`. A listener that
fell back appears there too, with `fellBack: true` and its actual address.

Port `53` cannot be shared, so the second instance runs without the built-in
resolver: the containers it starts still reach it by the exact split-horizon
hostnames, but not by their subdomains (virtual-hosted S3, API Gateway and
Lambda function URLs). Both instances also share the `overcast` Docker network
by default; set a different `OVERCAST_NETWORK` on the second if their
containers must not see each other.

## LocalStack aliases

Every row marked *(alias)* is read directly, so a LocalStack `environment:`
block usually carries over untouched. Setting an alias and its Overcast name to
the same value is fine; setting them to **different** values fails startup
naming both, rather than silently preferring one. A startup log line names every
alias that was recognised. The full mapping — including the variables
deliberately *not* aliased — is in
[Migrating from LocalStack](./migration-from-localstack.md#environment-variables).

## Exposing MCP

`OVERCAST_MCP_REMOTE_EXPOSURE=true` makes `OVERCAST_MCP_AUTH_TOKEN` mandatory —
Overcast refuses to start without one — and turns on bearer-token auth for every
MCP request. It does not itself change what Overcast binds: if `OVERCAST_LISTEN`
exposes the port, the MCP endpoint is exposed with it, so set both *before*
exposing the port beyond localhost. Browser `Origin` checks (localhost origins
only) are enforced on MCP either way.

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
| `trace` | Everything in `debug`, plus emulator machinery: health-check probes, web console polling, background flush/sweep ticks. Very high volume — use for a short capture window, not always-on. |
| `warn`  | One-liners for handled-but-unexpected conditions (a malformed record was skipped, a slow filesystem was detected). |
| `error` | One-liners for failures that need attention (storage degraded, a migration failed).                       |

For contributors: the full call-site policy (what belongs at `debug` vs
`trace`) is documented in [CONTRIBUTING.md § Log levels](https://github.com/overcast-sh/overcast/blob/main/CONTRIBUTING.md#log-levels).
