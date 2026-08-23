---
title: "Lambda"
description: "Lambda emulation has two distinct concerns:"
section: "Service Reference"
tags:
  - docs
  - lambda
  - services
---

# Lambda

> AWS docs: https://docs.aws.amazon.com/lambda/latest/api/welcome.html

Lambda emulation has two distinct concerns:

1. **Control plane** — the management API (create/update/invoke functions, manage
   event source mappings). This is straightforward HTTP.
2. **Data plane** — actually executing function code. This requires running
   arbitrary user code, which has significant security and complexity implications.

For v1, the data plane supports **container-based execution** via Docker: `Invoke` calls
spin up official AWS Lambda ECR images (`public.ecr.aws/lambda/{runtime}`) in Docker
containers, communicate with the Lambda Runtime API, and return real response payloads.

> [!NOTE]
> **Real function execution requires Docker.** Without it, `Invoke` returns a stub response
> and does not execute your function code. The Docker socket must be bind-mounted when
> running Overcast in a container — see the [README](../../README.md#docker-compose-recommended-for-local-dev)
> for DinD configuration in CI environments where socket mounting is restricted.

---

## Known limitations

- **Asynchronous invocation is configurable, and only the event queue is
  missing.** `PutFunctionEventInvokeConfig` and its family are implemented, so
  `MaximumRetryAttempts` (0-2) and `MaximumEventAgeInSeconds` (60-21600) apply
  per function, version or alias; a function with no configuration gets AWS's
  defaults of two retries, waiting the one minute then two minutes AWS documents
  for a function error. On-success and on-failure **destinations** receive AWS's
  invocation record — the envelope naming the request, the condition, the attempt
  count and the function's response — and a `DeadLetterConfig` receives the event itself,
  so a function configured with both gets both, as on AWS. All of this applies
  equally to events arriving from S3 notifications, EventBridge and Scheduler
  targets, and SNS `lambda` subscriptions, which share the same async path.
  - **`MaximumEventAgeInSeconds` is measured from acceptance**, covering the
    waits between attempts and the time the handler spends running, and an event
    that outlives it is discarded before the next attempt rather than after it —
    which is where AWS checks it. At AWS's retry waits its sixty-second minimum
    is reachable in the ordinary case: an event whose first attempt fails has
    already expired when the one-minute wait ends, so it never runs again.
  - **An aged-out record's `condition` reads `RetriesExhausted`**, which is
    probably not what AWS sends — its console describes an on-failure
    destination as firing when an event "fails all processing attempts *or*
    exceeds the maximum age", so the two are distinct. AWS documents no string
    for the second, and inventing one would be worse than reusing a real one.
  - A function throttled by its own reserved concurrency is the documented
    exception and is not retried by this loop: AWS sends those events to the
    dead-letter queue "without any retries", and Overcast does the same once the
    invocation has waited out its concurrency back-off. A function throttled by
    *exhausted* concurrency rather than a reserve of zero is the case Overcast
    still gets wrong — AWS returns those to the queue for up to six hours, and
    Overcast dead-letters them once the back-off is spent.
  - **An S3 destination is refused.** AWS allows an S3 bucket as an *on-failure*
    destination; Overcast answers `501` rather than accepting the configuration
    and writing no object. An S3 *on-success* destination is rejected with
    `InvalidParameterValueException`, which is what AWS does — S3 is on-failure
    only.
- **`TracingConfig`, `EphemeralStorage` and `KMSKeyArn` are recorded, not
  honoured.** All three are validated against AWS's own constraints, stored, and
  returned by `GetFunction`, `GetFunctionConfiguration`, `CreateFunction` and
  `UpdateFunctionConfiguration`, so a template or SDK client reads back exactly
  what it set. None of them changes what the function does:
  - **X-Ray tracing is not emulated at all** — there is no X-Ray service in
    Overcast, so no segment is recorded and no trace is available to look at,
    whichever `Mode` is set. `Active` and `PassThrough` behave identically.
  - **The ephemeral storage size is not enforced on the container.** A function
    configured with 512 MB of `/tmp` gets whatever the Docker host gives it,
    which is normally far more.
  - **The KMS key is an association only.** Environment variables are stored in
    plaintext, as all Overcast state is; nothing is encrypted at rest.
- Cold-start latency simulation is not implemented.
- Account-wide concurrency quotas and requests-per-second limits are not
  emulated; only per-function reserved concurrency is enforced.
- Runtime-specific environment validation is minimal.
- Extension Logs API support is limited to HTTP destinations and best-effort
  delivery. Telemetry API subscriptions are not yet implemented.
- Under the JSON log format, the init-phase platform records
  (`platform.initStart`, `platform.initReport`) are not emitted. Both are
  `DEBUG` at the default `SystemLogLevel`, so nothing is missing from a default
  log stream, but lowering the level will not make them appear. Tracked in
  [#660](https://github.com/Neaox/overcast/issues/660).
- `UpdateFunctionConfiguration` with an explicitly empty `LoggingConfig: {}`
  object returns `501`. AWS's semantics for that shape have not been captured,
  and guessing between "no-op" and "reset to defaults" would mutate the function
  either way. `LoggingConfig` with explicit members — including
  `LogFormat: JSON` — applies normally.
- An unqualified `DeleteFunction` removes the function record, its deployment
  package and its resource policies, but published versions, aliases and
  version counters for that name are left behind. Recreating a function under
  the same name therefore inherits them.
- Tags are stored for functions and event source mappings only. The other
  taggable Lambda resources — code signing configurations, capacity providers
  and network connectors — return `501` from the tag operations.
- A function whose code names a `Code.S3ObjectVersion` is pinned to that
  version, so it is excluded from the reactive S3 code sync that refreshes an
  unpinned function when a new object lands at its `S3Bucket`/`S3Key`. That
  matches AWS, where a pinned version never changes; use `UpdateFunctionCode`
  to move it.
- That sync only moves a function onto bytes it is not already running. A
  `PutObject` that re-uploads an unchanged asset, or one that lands just before
  a `CreateFunction` reads the same key, is not a new deployment: `RevisionId`
  and `LastModified` stay put and the warm execution environment survives.

---

## VPC placement — `VpcConfig`

A function with a `VpcConfig` naming subnets is placed on that VPC's network and
nothing else. It can reach what is in the VPC with it, and cannot reach a
container outside it — which is what a `VpcConfig` means on AWS, where placement
subtracts rather than adds.

Overcast's own API endpoint is the exception, and stays reachable from every
function regardless of placement. `AWS_ENDPOINT_URL` and the Lambda Runtime API
ride a separate control plane, so calling S3 or DynamoDB from inside a VPC works
here without the NAT gateway or VPC endpoint AWS would need. That is a
deliberate divergence: the alternative is every VPC-placed function failing on
its first SDK call.

The common mistake this now catches is a database in a VPC and a function
without a `VpcConfig` — which never worked on AWS, and used to work here. The
fix is the AWS one: put the function in the VPC, or set `PubliclyAccessible` on
the instance. A refused connection is named rather than left to hang; see
[Networking § Lambda, ECS and VPCs](../networking.md) for the message and the
full list of what is and is not enforced.

---

## Runtimes

Every runtime identifier Overcast knows about comes from one table in
`internal/services/lambda/runtime_catalog.go`. Request validation, the
runtime-to-image mapping used to execute a function, the AWS deprecation dates,
and the list the web UI's create wizard offers are all derived from it, so those
views cannot disagree with one another.

Two upstream sources feed that table:

- **Runtime identifiers** are `com.amazonaws.lambda#Runtime` from the pinned AWS
  Smithy model (see `models/aws/VERSION`), in the model's own order.
- **Deprecation, block-create and block-update dates**, and the successor AWS
  recommends, come from AWS's published Lambda runtime lifecycle table.

### The three answers a runtime can get

| Situation                                                                | Response                                                                                            |
| ------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| Not a value of the modeled `Runtime` enum, and not a runtime-version ARN  | `400 InvalidParameterValueException` with AWS's enum-validation message listing every modeled value |
| Modeled, but past AWS's deprecation phase for the operation              | `400 InvalidParameterValueException` with AWS's message naming the recommended successor            |
| Modeled and accepted by AWS, but Overcast has no execution image for it  | `501 NotImplemented` — an honest emulator gap; nothing is persisted                                 |

The third case is the important one: a missing execution image is **not** a bad
request. Overcast never invents a `400` for it, because that would report a
request AWS accepts as invalid.

### Deprecation follows AWS's phases, not a single flag

AWS retires a runtime in three steps, and Overcast observes each of them against
the current date:

1. **Deprecation date** — end of support. The runtime still deploys and still
   invokes. Overcast marks it deprecated in the runtime catalog and the web UI
   labels it, but `CreateFunction` keeps working.
2. **Block function create** — `CreateFunction` starts returning
   `InvalidParameterValueException`. Existing functions are untouched.
3. **Block function update** — `UpdateFunctionConfiguration` starts returning the
   same error, so a function can no longer be moved onto that runtime.

A deprecated runtime is therefore not automatically refused. `python3.9`, for
example, lost support in December 2025 but stays deployable on AWS — and on
Overcast — until its block-create date.

### Execution coverage

Overcast maps an official Lambda base image (`public.ecr.aws/lambda/…`) to every
runtime AWS still accepts for `CreateFunction`, across Node.js, Python, Java
(including the Amazon Linux 2023 variants), .NET, Ruby and the `provided` custom
runtimes. Runtimes AWS has already blocked from `CreateFunction` — `go1.x`,
`java8`, `python3.7` and older, `nodejs14.x` and older, the `dotnetcore` family,
`ruby2.x`, and `provided` — carry no image: Overcast will never create a
function on one, so the `400` above is the only response they can produce.

`LAMBDA_SEED_RUNTIME_IMAGES=true` pre-pulls only the images for runtimes AWS
still supports. A deprecated-but-deployable runtime is pulled on demand at its
first cold start instead, so enabling the seed does not download several extra
gigabytes of end-of-life images.

`GET /_overcast/lambda/runtimes` (emulator-only, used by the web UI) returns the catalog
with each runtime's `supported`, `deprecated`, `createBlocked` and
`updateBlocked` flags.

### Container images published to the emulated ECR

A `PackageType=Image` function names its image with `Code.ImageUri`. When that
URI addresses this account's ECR registry —
`{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}`, which is what CDK
synthesises for a `DockerImageFunction`, from `AWS::AccountId` and
`AWS::Region` rather than from the repository it pushed to — Overcast pulls the
image from the registry it actually serves, with the credentials
`ecr:GetAuthorizationToken` issues, and runs the container from that reference.
Pulling the URI as written would leave the machine, reach real AWS's wildcard
ECR domain, and be refused anonymously while the bytes sit in the local
registry.

Any other image is pulled exactly as written: a public image keeps its meaning,
and another registry is never offered these credentials. The function still
reports `Code.ImageUri` as it was deployed — the rewrite decides where the bytes
come from, not what was deployed. If the registry is not running (Docker
unavailable, so nothing could have been pushed to it either) the reference is
left alone rather than pointed at a registry that cannot answer.

See [ECR § Running an image from here](./ecr.md#running-an-image-from-here).

---

## Concurrency and execution environments

Overcast reuses containers for sequential invocations and scales out to one
container per concurrent invocation, the same way Lambda reuses and scales
execution environments. After a burst, the surplus stays warm for reuse until
the 15-minute idle sweep.

### Limits

| Limit | Env var | Default | Behaviour when reached |
| --- | --- | --- | --- |
| Containers across all functions | `LAMBDA_MAX_INSTANCES` | auto (host-derived; 25 fallback) | Reclaim an idle container → queue → throttle |
| Containers for one function | `LAMBDA_MAX_INSTANCES_PER_FUNCTION` | auto (host-derived; 10 fallback) | Queue → throttle |
| Aggregate container memory (Σ `MemorySize`, MB) | `LAMBDA_MAX_MEMORY_MB` | auto (host-derived; unlimited fallback) | Reclaim an idle container → queue → throttle |
| Idle containers kept per function | `LAMBDA_MAX_WARM_INSTANCES` | 10 | Surplus destroyed on release |
| Concurrent container starts | `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` | auto (host-derived; 4 fallback) | Starts queue behind the semaphore |
| `ReservedConcurrentExecutions` | (per function, AWS API) | unset | Throttle immediately |

The instance limits protect your machine; they are not an emulation of AWS's
account quota. An invocation that cannot get a container first reclaims the
least-recently-used idle one (never a provisioned one), then waits — and **if it
is still waiting when the function's timeout expires it is throttled**, with
AWS's 429 `TooManyRequestsException` and `Reason: ConcurrentInvocationLimitExceeded`.
If you hit this in normal use, raise the limit rather than treating it as AWS
behaviour.

When an env var above is unset, the limit is sized to the machine that actually
runs the containers — Docker's own host, read from `GET /info` (`NCPU`,
`MemTotal`), which is the Docker Desktop VM or the remote daemon, not
necessarily where the Overcast process runs:

- `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` = `clamp(NCPU/2, 2, 8)` — every cold
  start bursts to ~2 CPUs during INIT, so this is the point where INIT alone
  could claim every core.
- `LAMBDA_MAX_INSTANCES` = `clamp(MemTotal×0.65 / 256 MiB, 4, 32)` — 65% of
  host memory at an assumed ~256 MiB per container.
- `LAMBDA_MAX_INSTANCES_PER_FUNCTION` = `clamp(maxInstances/2, 2, maxInstances)`.
- `LAMBDA_MAX_MEMORY_MB` = `MemTotal×0.65`, in MB.

The derived values are logged at startup
(`lambda: derived instance limits from the Docker host`). Setting an env var
pins that limit exactly as before; if `GET /info` fails, the previous fixed
defaults (25 / 10 / 4) apply and the memory budget is unlimited.

The memory budget counts bytes, not just containers: a new container is
admitted only while `Σ MemorySize` of live containers (warm and executing)
plus its own `MemorySize` stays inside the budget. Each container is
hard-capped at its function's `MemorySize` with swap disabled, so the sum is
a real bound on host memory, not an estimate. Exhausting the budget follows
the same ladder as the instance limits — reclaim the least-recently-used idle
container, queue, throttle at the function's timeout — and logs one warning
(naming `LAMBDA_MAX_MEMORY_MB`) the first time it forces an invocation to
queue. A reclaim forced by the memory budget is logged at `WARN` (the routine
instance-count reclaim stays `INFO`). While the pool sits above ~90% of the
budget, a container whose invocation just finished is destroyed instead of
kept warm, so queued work regains budget without waiting for the idle sweep;
provisioned environments are always kept, and a running invocation is never
interrupted. Entering that regime is logged once at `WARN` (budget, reserved
memory, and the high-water threshold — expect cold starts while it lasts) and
leaving it once at `INFO` with how many containers were shed and for how
long, rather than one line per destroyed container.

`ReservedConcurrentExecutions` is enforced with AWS's semantics instead: no
queueing, an immediate 429 with
`Reason: ReservedFunctionConcurrentInvocationLimitExceeded`. Setting it to 0
disables the function, the same idiom that works on AWS. Overcast does not
emulate the account-wide unreserved pool.

Asynchronous invocations (`InvocationType: Event`) are never throttled back to
the caller — they were already answered 202. A throttled async invocation is
retried internally, as on AWS, though on a much shorter budget than AWS's six
hours. Event source mappings behave the same way: a throttled batch is left in
flight so the messages return to the queue on the visibility timeout.

This holds however the event was raised. An S3 notification, an EventBridge or
Scheduler target, and an SNS `lambda` subscription all enter the same async path
as an HTTP `InvocationType: Event` invoke, so none of them sees a throttle
either — the event source is told only whether Lambda accepted the event.

### Provisioned concurrency

`PutProvisionedConcurrencyConfig` really does pre-initialize environments.
They are created in the background (`Status: IN_PROGRESS`, then `READY`), held
open regardless of the idle sweep, replenished when one is lost, and rebuilt
against the new configuration after a code or config update. Containers report
`AWS_LAMBDA_INITIALIZATION_TYPE=provisioned-concurrency`, which is what
Powertools and similar libraries read to classify a cold start.

Provisioned concurrency is a **floor, not a ceiling**: when all reserved
environments are busy, further invocations spill over into on-demand capacity
with a cold start rather than being throttled — matching AWS, where only
reserved concurrency caps a function. A reservation is never reclaimed to make
room for another function, and `Allocated`/`Available` are reported from the
environments that actually exist.

These operations live under the `/2019-09-30/` API path, as on AWS.

Without Docker, nothing can be allocated, so the config is stored and reported
as `Status: FAILED` with a `StatusReason` rather than claiming `READY`.

### Environment lifecycle

Overcast keeps warm containers per function and reuses them for sequential
invocations, the same way Lambda reuses execution environments.

A container is created with the function's code, environment variables, memory
limit, timeout, handler, layers and VPC attachment fixed at start — a running
container can never observe a change to any of them. So when `UpdateFunctionCode`
or `UpdateFunctionConfiguration` changes any of those, the environment is
**retired immediately**:

- An **idle** container is destroyed as soon as the update is stored.
- A container **serving an invocation** finishes that invocation in the old
  environment and is destroyed on completion. In-flight invocations are never
  interrupted.
- **No replacement is started** for an on-demand environment: the next
  invocation cold starts one. A provisioned concurrency reservation *is* rebuilt
  in the background, against the new configuration.

Updates that change nothing the container can observe — the description or the
role, for example — leave the warm container in place, so cosmetic edits do not
cost a cold start.

Deleting a function destroys its containers immediately. Otherwise, a container
left idle for 15 minutes is reaped by the background sweeper — unless it holds a
provisioned concurrency reservation, which is exempt.

If a warm container disappears without Overcast asking — you removed it with
`docker rm -f`, it was OOM-killed, or Docker restarted — the Docker event stream
reports it and the environment is dropped from the warm set right away, so the
next invocation is an ordinary cold start.

The web UI's system map and the `GET /_overcast/lambda/instances` endpoint report one
entry per execution environment, so a function serving five concurrent
invocations shows five instances. A retired environment stops being listed at
the moment it is retired rather than lingering until the idle timeout.

---

## Partial batch responses

An event source mapping created with
`FunctionResponseTypes: ["ReportBatchItemFailures"]` — CDK's
`reportBatchItemFailures: true` — is honoured, not just stored. The poller reads
the function's response and acts on the records it names:

```json
{ "batchItemFailures": [{ "itemIdentifier": "<message id or sequence number>" }] }
```

- **SQS.** Only the messages the function did *not* report are deleted. A
  reported message stays in flight and becomes visible again when its visibility
  timeout expires, which is exactly how AWS redelivers it — Lambda has no
  "return this one message" call either. The queue's own `RedrivePolicy` then
  counts the receive and moves it to a dead-letter queue on schedule.
- **DynamoDB Streams.** The batch is retried from the earliest record the
  function named, and everything before it is treated as done. Records *after*
  it are redelivered even if the function said they succeeded, because a stream
  is ordered and AWS checkpoints at the lowest reported sequence number rather
  than skipping the records in between.

AWS's edge cases are reproduced. An empty or absent `batchItemFailures` list — or
no response at all — means the whole batch succeeded, so turning the flag on
cannot change a handler that does not use it. A response Overcast cannot read is
a complete batch *failure* and nothing is acknowledged: invalid JSON, an entry
that is not an object, a missing, empty or non-string `itemIdentifier`, and an
identifier naming a record that was not in the batch. Each one is logged with
the reason, at `warn`.

One deliberate divergence: the member names are matched case-insensitively, so a
handler written against a strongly typed SDK that serialises `BatchItemFailures`
is honoured rather than read as a malformed response. A name that is neither
spelling is still malformed, which is the case that matters.

`FunctionResponseTypes` is validated against AWS's one-member enum:
`ReportBatchItemFailures` is the only accepted value, and anything else is
refused with AWS's own constraint message rather than stored.

---

## Log format and log levels

A function's `LoggingConfig` is honoured end to end. `LogFormat` decides what
Overcast writes around each invocation, and under `JSON` the two log levels
decide how much of it reaches CloudWatch Logs.

`LogGroup` is respected as well: a function with a custom log group writes
there, and the group is created on `CreateFunction` like the default
`/aws/lambda/<function-name>` one.

### Text

The default, and unchanged: the plain-text `START`, `END` and `REPORT` lines
real Lambda writes, byte for byte. `ApplicationLogLevel` and `SystemLogLevel`
do not apply to Text on AWS, so Overcast filters nothing in this mode — every
line the function writes reaches CloudWatch Logs.

### JSON

`LogFormat: JSON` replaces those three lines with the events AWS publishes
through the Lambda Telemetry API, one JSON object per log line, each shaped
`{"time", "type", "record"}` with a millisecond-precision UTC timestamp:

| Event `type`           | Replaces | System log level                | Record                                                                                             |
| ---------------------- | -------- | ------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `platform.start`       | `START`  | `INFO`                          | `requestId`, `version`                                                                             |
| `platform.runtimeDone` | `END`    | `DEBUG` on success, else `WARN` | `requestId`, `status`, `metrics`: `durationMs`, `producedBytes`                                    |
| `platform.report`      | `REPORT` | `INFO` on success, else `WARN`  | `requestId`, `status`, `metrics`: `durationMs`, `billedDurationMs`, `memorySizeMB`, `maxMemoryUsedMB` |

The levels are AWS's own system-log-level event mapping, not an Overcast
convention. `status` is `success`, `failure` (the handler returned an error),
`error` (the execution environment ended the invocation) or `timeout`.
`metrics.initDurationMs` appears in `platform.report` only on the first report
of an on-demand cold start, exactly where the text `REPORT` carries
`Init Duration`. Overcast emits only the subset of the Telemetry API schema it
genuinely observes: the `errorType` member both records model is never
populated, because nothing in the emulator knows an AWS error type to put
there.

At the default `SystemLogLevel` of `INFO`, a successful invocation therefore
logs two records — `platform.runtimeDone` is `DEBUG` and only appears once you
lower the level:

```json
{"time":"2026-08-09T04:21:07.512Z","type":"platform.start","record":{"requestId":"8f2a1c3e-…","version":"$LATEST"}}
{"time":"2026-08-09T04:21:07.884Z","type":"platform.report","record":{"requestId":"8f2a1c3e-…","status":"success","metrics":{"durationMs":371.42,"billedDurationMs":372,"memorySizeMB":512,"maxMemoryUsedMB":78,"initDurationMs":214.88}}}
```

The **init-phase** records, `platform.initStart` and `platform.initReport`, are
not emitted at all. Both are `DEBUG` at the default system log level, so a
default log stream is complete without them, but asking for `DEBUG` will not
produce them either. See [Known limitations](#known-limitations).

### Filtering

Both levels default to `INFO` when `LogFormat` is `JSON`, as on AWS. The level
ordering is `TRACE` < `DEBUG` < `INFO` < `WARN` < `ERROR` < `FATAL`, and a
record is kept when its own level is at or above the configured one.

- **`SystemLogLevel`** filters the platform records above, by the level in the
  table.
- **`ApplicationLogLevel`** filters the function's own stdout and stderr, by the
  `"level"` member of each record. A line that parses as a JSON object with a
  recognised level is filtered on it; everything else — unstructured text,
  malformed JSON, a missing or unknown `"level"` — is treated as `INFO`, which
  is what AWS documents. Level names are matched case-insensitively, because a
  runtime's logger picks its own casing.

Filtering decides what reaches **CloudWatch Logs and the `X-Amz-Log-Result`
tail** (the invoke response's log tail, and the web UI's test tab) only.
Telemetry and Logs API subscribers receive the complete set of records either
way — AWS is explicit that the CloudWatch system log level does not affect
Telemetry API behaviour.

### What the container sees

The logging configuration is handed to the runtime the way AWS hands it over,
because the managed runtimes and Powertools read it to structure their own
output:

| Variable                | Set when              | Value                                      |
| ----------------------- | --------------------- | ------------------------------------------ |
| `AWS_LAMBDA_LOG_FORMAT` | always                | `Text` or `JSON`                           |
| `AWS_LAMBDA_LOG_LEVEL`  | `LogFormat` is `JSON` | the function's effective `ApplicationLogLevel` |

In Text mode `AWS_LAMBDA_LOG_LEVEL` is not set, matching AWS — setting it would
tell a runtime to filter output Lambda has not asked it to filter.

Because those values are baked into a container at start, the logging
configuration is part of the execution environment's identity: changing
`LogFormat` or either level retires the warm environments the same way a memory
or environment-variable change does. See
[Environment lifecycle](#environment-lifecycle).

---

## Lambda Layers

When a function specifies layer ARNs (e.g. from CDK or CloudFormation), Overcast
injects each layer into `/opt` in the Lambda container before startup — matching
real Lambda behavior. Layers published locally via `PublishLayerVersion` are
resolved automatically; external layers (AWS-managed or third-party) require
additional configuration.

### Default behavior (no config)

If a layer ARN is not found locally, and it is not a cache-backed external layer,
`Invoke` returns a Lambda init-style error before starting the runtime:

```
{"errorMessage":"Failed to load Lambda layer arn:aws:lambda:...: layer version not found","errorType":"Runtime.InitError"}
```

This catches missing layer metadata before a container cold start. Foreign-account
AWS-managed or third-party layer ARNs can satisfy that check through the layer
cache or remote-fetch path described below.

### Option 1: Pre-download layers (no AWS credentials needed at runtime)

Download the layer once using the AWS CLI and place the zip in the layer cache
directory that Overcast checks at invocation time. By default this is
`/data/layers` inside the container — mount your local directory there and
you're done.

**Step 1 — Download the layer zip:**

```bash
# Get the presigned download URL
LAYER_URL=$(aws lambda get-layer-version-by-arn \
  --arn "arn:aws:lambda:ap-southeast-2:094274105915:layer:AWSLambdaPowertoolsTypeScriptV2:22" \
  --query 'Content.Location' --output text)

# Download it
curl -o AWSLambdaPowertoolsTypeScriptV2_22.zip "$LAYER_URL"
```

**Step 2 — Place it in the cache directory:**

```bash
mkdir -p .overcast/layers
mv AWSLambdaPowertoolsTypeScriptV2_22.zip .overcast/layers/
```

The filename convention is `{LayerName}_{Version}.zip` — derived directly from
the ARN. For example:

| Layer ARN                                                                                         | Expected filename                                    |
| ------------------------------------------------------------------------------------------------- | ---------------------------------------------------- |
| `arn:aws:lambda:ap-southeast-2:094274105915:layer:AWSLambdaPowertoolsTypeScriptV2:22`             | `AWSLambdaPowertoolsTypeScriptV2_22.zip`             |
| `arn:aws:lambda:ap-southeast-2:665172237481:layer:AWS-Parameters-and-Secrets-Lambda-Extension:11` | `AWS-Parameters-and-Secrets-Lambda-Extension_11.zip` |

**Step 3 — Mount the directory into the container:**

You have two options — mount only the layers directory, or mount the whole
data directory (which also persists SQLite state across restarts):

```yaml
# docker-compose.yml — Option A: mount just the layers directory
services:
  overcast:
    image: overcast:dev
    volumes:
      - "./.overcast/layers:/data/layers:ro"
      - "/var/run/docker.sock:/var/run/docker.sock"
```

```yaml
# docker-compose.yml — Option B: mount the whole data directory
services:
  overcast:
    image: overcast:dev
    volumes:
      - "./.overcast:/data"
      - "/var/run/docker.sock:/var/run/docker.sock"
```

With Option B, drop layer zips into `./.overcast/layers/` on the host and they
appear at `/data/layers/` inside the container. As a bonus, persistent state
(SQLite) also survives container restarts.

On the next invocation, Overcast finds the layer in the cache and injects it
into `/opt` — no AWS credentials required, no env var to set.

For foreign-account layer ARNs, the same cache lookup also satisfies Overcast's
invoke-time layer existence check. You do not need to publish a local replacement
layer when the function references the real AWS-managed ARN.

> **Tip:** To use a different path, set `LAMBDA_LAYER_CACHE_DIR` and mount the
> directory there instead. This is mainly useful when running the native
> binary outside Docker, where there is no `/data` mount.

### Option 2: Automatic remote fetching (requires AWS credentials)

Overcast can fetch layers directly from AWS at invocation time, cache them to
disk, and inject them. This is convenient but requires valid AWS credentials
with `lambda:GetLayerVersion` permission.

```yaml
services:
  overcast:
    image: overcast:dev
    environment:
      - LAMBDA_FETCH_REMOTE_LAYERS=true
      - LAMBDA_REMOTE_AWS_ACCESS_KEY_ID=AKIA...
      - LAMBDA_REMOTE_AWS_SECRET_ACCESS_KEY=...
      - LAMBDA_REMOTE_AWS_SESSION_TOKEN=... # if using SSO/assumed role
```

These are **separate** from the `AWS_ACCESS_KEY_ID=test` credentials used by
Overcast's own APIs — they are only used for layer downloads and never leak to
Lambda containers.

Once fetched, layers are cached on disk (in `LAMBDA_LAYER_CACHE_DIR` or the
default location) so subsequent invocations don't hit AWS again.

Layers that contain `extensions/` entries are supported for Docker-backed zip
functions. See [Lambda Extensions](#lambda-extensions) for lifecycle behavior,
AWS-calling extension requirements, and troubleshooting guidance.

---

## Lambda Extensions

Docker-backed functions — zip and container image alike — start executable
external extensions found directly under `/opt/extensions` before the runtime
entrypoint starts, as children of the same in-container init that owns the
runtime. Layer file modes are preserved, so extension binaries and scripts must
be executable in the layer zip.

Supported Runtime API extension paths:

- `POST /2020-01-01/extension/register`
- `GET /2020-01-01/extension/event/next`
- `POST /2020-01-01/extension/init/error`
- `POST /2020-01-01/extension/exit/error`
- `PUT /2020-08-15/logs`

`INVOKE` events are delivered only to extensions in the same container that
accepted the invocation. `SHUTDOWN` events are sent when Overcast tears down a
warm container.

Logs API subscriptions support HTTP destinations for `platform`, `function`,
and `extension` log types. Function stdout/stderr is delivered as `function`
records; the synthesized invocation records are delivered as `platform`
records — the START/END/REPORT lines under the Text log format, the JSON
events under JSON. Subscribers receive **every** record regardless of
`ApplicationLogLevel` and `SystemLogLevel`, which only filter CloudWatch Logs
and the invoke tail; see [Log format and log levels](#log-format-and-log-levels).
Delivery is best-effort and does not yet implement the full Lambda
buffering/retry contract.

### Reaching Overcast from function code

Lambda containers are siblings of Overcast, not children of it, so `localhost`
inside a function is the function's own container. Overcast sets
`AWS_ENDPOINT_URL` to an address the container can reach, which is enough for
SDK calls that address resources by name.

It is **not** enough for SQS. AWS SDKs resolve the SQS endpoint from the
`QueueUrl` rather than from client configuration, and `AWS_ENDPOINT_URL` loses
to it — see [SQS: queue URLs and endpoint resolution](sqs.md#queue-urls-and-endpoint-resolution).
A queue URL minted on the host and passed into a function (a CDK stack writing
`queue.queueUrl` into function environment) would send the function's SQS client
to its own loopback. Three things keep that working:

1. **URLs the function requests itself** — `CreateQueue`, `GetQueueUrl`,
   `ListQueues` — come back on the origin the function called in on, so they are
   dialable by definition.
2. **Loopback URLs in function environment** are rewritten at container start:
   any `http://localhost:<overcast-port>` or `http://127.0.0.1:<overcast-port>`
   origin in an environment value is re-pointed at the container-reachable
   endpoint. Other hosts and other ports are left alone.
3. **Split-horizon hostnames** resolve to `127.0.0.1` in public DNS, so they work
   from the host, and are remapped to Overcast's address in each container's
   `/etc/hosts`, so the same URL works from inside. Built in:
   `localhost.overcast.sh`, `localhost.localstack.cloud`, and
   `localhost.floci.io`. Add more with `OVERCAST_SPLIT_HORIZON_HOSTS`
   (comma-separated); `OVERCAST_HOSTNAME` is mapped too when it is a DNS name.

Setting `OVERCAST_HOSTNAME=localhost.overcast.sh` gives every service one URL
form that is valid on both sides of the container boundary, which is the
simplest configuration when resource URLs are handed between host and functions.

If your function constructs its own SQS client, passing the endpoint explicitly
is always safe:

```js
new SQSClient({ endpoint: process.env.AWS_ENDPOINT_URL });
```

### Extensions that call AWS

Use a recent extension layer version that honors standard AWS SDK endpoint
configuration. Overcast injects endpoint and region environment variables into
the Lambda container so endpoint-aware extensions use the local emulator instead
of real AWS:

- `AWS_ENDPOINT_URL`
- `AWS_ENDPOINT_URL_SSM`
- `AWS_ENDPOINT_URL_SECRETS_MANAGER`
- `AWS_REGION`
- `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_SESSION_TOKEN`

Reference extensions by the Lambda layer version in your IaC, because that is
what CDK, CloudFormation, and Lambda ARNs expose. For example, the AWS
Parameters and Secrets Lambda Extension for `ap-southeast-2` was verified with
layer version `90`:

```text
arn:aws:lambda:ap-southeast-2:665172237481:layer:AWS-Parameters-and-Secrets-Lambda-Extension:90
arn:aws:lambda:ap-southeast-2:665172237481:layer:AWS-Parameters-and-Secrets-Lambda-Extension-Arm64:90
```

Choose the layer architecture that matches the Lambda function architecture, not
the host machine architecture. For example, an `x86_64` Lambda function should
use the x86_64 extension layer even when Docker is running on Apple Silicon.

The extension binary's own version, when available from extension logs or the
downloaded artifact, is useful for diagnostics but is usually not the version you
configure. In testing, the old AWS Parameters and Secrets layer version `11`
contained extension `1.0.143` and did not honor endpoint environment variables;
current layer version `90` contained extension `1.0.342` and routed SSM and
Secrets Manager requests to Overcast.

Overcast does not add a secret cache in front of the extension. The extension
binary keeps its own execution-environment-local cache, as it does on AWS, so a
warm environment can return a prior value until `SECRETS_MANAGER_TTL` expires.
A new Lambda container starts a new extension process and cache. Set the TTL to
`0` when every request must read the current `AWSCURRENT` value.

### Extension troubleshooting

If an extension still reaches real AWS or returns AWS credential errors:

- Confirm the configured Lambda layer ARN version is recent for the region and
  architecture you are using.
- Confirm the layer architecture matches the Lambda function architecture.
- Prefer checking the layer ARN version first; binary versions are secondary
  evidence from logs or inspected artifacts.
- Avoid blank user-defined endpoint or credential variables. Overcast provides
  the endpoint and test credentials inside the Lambda container.

---

## Hot Reload

Hot reload mounts your local source directory into the Lambda runtime at
`/var/task` so code edits are picked up on the next invoke without uploading a
new zip. This mode is opt-in and intended for local development.

Use hot reload for fast inner-loop development with interpreted runtimes such as
Node.js and Python. Use the normal zip or image deploy path when you need to
validate production-like packaging.

### Quickest CDK path: `cdk watch`

If you just want fast iteration without configuring hot-reload tags, use
`cdk watch`. It calls `UpdateFunctionCode` on every file change, which
invalidates the warm pool entry. No tag, bind mount, or Docker file-sharing
configuration is required:

```bash
AWS_ENDPOINT_URL=http://localhost:2456 cdk watch
```

Each save triggers a redeploy of only the changed function assets. This works
with every runtime and every bundler.

### Bind-mount hot reload

Use bind-mount hot reload when you need sub-second iteration and want to skip the
redeploy cycle entirely.

Enable the feature flag when starting Overcast:

```bash
OVERCAST_LAMBDA_HOT_RELOAD=true overcast serve
# or
docker run -e OVERCAST_LAMBDA_HOT_RELOAD=true overcast
```

`OVERCAST_HOT_RELOAD=true` turns it on for every compute service at once —
Lambda and [ECS](./ecs.md#hot-reload-editing-local-source-inside-a-task) —
and `OVERCAST_LAMBDA_HOT_RELOAD` overrides it either way, so a single service
can be opted out of an umbrella `true`.

Then create or update the function with the `overcast:hot-reload-path` tag set
to an absolute host path:

```bash
aws --endpoint-url http://localhost:4566 lambda create-function \
   --function-name demo-hot \
   --runtime nodejs20.x \
   --handler index.handler \
   --role arn:aws:iam::000000000000:role/lambda-role \
   --zip-file fileb://minimal.zip \
   --tags overcast:hot-reload-path=/absolute/path/to/lambda/source
```

Invoke normally, edit files in the configured source path, and invoke again:

```bash
aws --endpoint-url http://localhost:4566 lambda invoke \
   --function-name demo-hot out.json
```

Path behavior:

- Path must be absolute.
- Windows drive paths are normalized, for example `C:\Users\you\app` becomes
  `/c/Users/you/app`.
- Mount is read-only inside the container at `/var/task:ro`.
- Host files must be readable by the runtime user in the container.

### CDK hot-reload tags

When you add tags to an `AWS::Lambda::Function` resource in a CloudFormation
template or CDK construct, Overcast's CloudFormation provisioner forwards those
tags to the Lambda `CreateFunction` call. Set `overcast:hot-reload-path`
directly in your CDK stack and hot reload activates after `cdk deploy`.

For Node.js 24, you can mount raw TypeScript because the runtime strips
TypeScript types natively:

```typescript
import * as path from "path";
import * as cdk from "aws-cdk-lib";
import * as lambda from "aws-cdk-lib/aws-lambda";

const fn = new lambda.Function(this, "MyFn", {
  runtime: lambda.Runtime.NODEJS_24_X,
  handler: "src/handler.handler",
  code: lambda.Code.fromAsset(path.join(__dirname, "src")),
});

cdk.Tags.of(fn).add("overcast:hot-reload-path", path.resolve(__dirname, "src"));
```

For Node.js 22 and earlier, mount compiled JavaScript output instead of raw
TypeScript:

```typescript
cdk.Tags.of(fn).add(
  "overcast:hot-reload-path",
  path.resolve(__dirname, "dist"),
);
```

Keep `dist/` fresh with your bundler, then deploy once:

```bash
npx esbuild src/handler.ts --bundle --platform=node --outdir=dist --watch
AWS_ENDPOINT_URL=http://localhost:2456 cdk deploy
```

For Python or pre-built JavaScript, point the tag directly at the source tree:

```typescript
cdk.Tags.of(fn).add("overcast:hot-reload-path", path.resolve(__dirname, "src"));
```

### Hot-reload behavior and troubleshooting

- Attached layer archives are expanded into `/opt` before the Lambda container
  starts, the same as normal zip-based invocation.
- If multiple attached layers provide the same file path, later layers in the
  function's `Layers` list override earlier ones.
- Parallel invocations of the same function share one mounted source tree. This
  is convenient for development, but less isolated than AWS production behavior.
- If Docker reports `mounts denied` or an invalid bind mount, allow the directory
  in Docker Desktop File Sharing settings.
- If the runtime reports import or init errors, verify the source directory
  contains the expected handler file at the root of mounted `/var/task`.
- If init fails with a missing layer version, verify same-account layer ARNs
  exist in Overcast or place foreign-account layer zips in
  `LAMBDA_LAYER_CACHE_DIR` using `{LayerName}_{Version}.zip`.
- Overcast logs a `WARN` at container acquire time if `.ts` files are found with
  no `.js` files on Node.js 22 or earlier.

---

## Configuration Reference

| Variable                              | Default          | Description                                             |
| ------------------------------------- | ---------------- | ------------------------------------------------------- |
| `LAMBDA_LAYER_CACHE_DIR`              | `/data/layers`\* | Directory to look up / store cached layer zips          |
| `LAMBDA_FETCH_REMOTE_LAYERS`          | `false`          | Enable automatic fetching from real AWS                 |
| `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` | _(auto)_\*\*     | Max concurrent Docker-backed Lambda container starts    |
| `LAMBDA_MAX_INSTANCES`                | _(auto)_\*\*     | Max containers across all functions                     |
| `LAMBDA_MAX_INSTANCES_PER_FUNCTION`   | _(auto)_\*\*     | Max concurrent containers for one function              |
| `LAMBDA_MAX_MEMORY_MB`                | _(auto)_\*\*     | Aggregate Σ `MemorySize` budget for live containers, MB |
| `LAMBDA_MAX_WARM_INSTANCES`           | `10`             | Idle containers kept warm per function                  |
| `LAMBDA_SEED_RUNTIME_IMAGES`          | `false`          | Pre-pull every currently-supported runtime image at startup |
| `LAMBDA_TAR_CACHE_MB`                 | `256`            | In-memory cache of pre-built code/layer tars (0 = off)  |
| `LAMBDA_PROACTIVE_INIT`               | `true`           | Pre-initialize an environment after config settles; `false` opts out |
| `LAMBDA_INIT_TIMEOUT_SECONDS`         | `10`             | Max seconds to wait for runtime INIT before invocation  |
| `LAMBDA_REMOTE_AWS_ACCESS_KEY_ID`     | —                | AWS access key for remote layer downloads               |
| `LAMBDA_REMOTE_AWS_SECRET_ACCESS_KEY` | —                | AWS secret key for remote layer downloads               |
| `LAMBDA_REMOTE_AWS_SESSION_TOKEN`     | —                | AWS session token for remote layer downloads (optional) |

\* Resolves to `{OVERCAST_DATA_DIR}/layers`. In the standard Docker image
`OVERCAST_DATA_DIR=/data`, so layers are read from `/data/layers`.

\*\* Derived from the Docker host when unset — see
[Limits](#limits) for the formulas and fixed fallbacks.

### Proactive initialization

AWS documents that Lambda "may proactively initialize execution
environments" ahead of traffic; Overcast mirrors that by default
(`LAMBDA_PROACTIVE_INIT=true`; set it to `false` to opt out). Ten seconds
after a function's code or configuration stops
changing (so a CDK deploy's create-then-update burst collapses into one
attempt), one execution environment is created in the background — for
functions that have been invoked this session or have a function URL or
event source mapping — so the next request lands warm. The environment
reports `AWS_LAMBDA_INITIALIZATION_TYPE=on-demand` and its first REPORT line
carries no `Init Duration`, exactly like a proactively initialized
environment on AWS. Creation only proceeds when capacity is idle: it never
queues ahead of a real invocation, respects the instance and memory budgets,
and the environment ages out through the normal idle sweep.

---

## Deleting a version, and where tags live

`DeleteFunction` means two different things depending on whether you pass a
qualifier — either as `?Qualifier=` or inside the function name itself
(`my-function:2`):

| Request                                            | Effect                                                                        |
| -------------------------------------------------- | ----------------------------------------------------------------------------- |
| `DELETE /functions/my-function`                     | Deletes the function, its package and its resource policies                    |
| `DELETE /functions/my-function?Qualifier=2`         | Deletes **only** published version 2, its qualified policy and its provisioned concurrency |
| `DELETE /functions/my-function?Qualifier=$LATEST`   | `400 InvalidParameterValueException` — `$LATEST` only goes with the function   |
| Qualifier naming a version an alias points at       | `409 ResourceConflictException` naming the aliases                             |
| Qualifier naming an alias                           | `409 ResourceConflictException` — `DeleteFunction` never deletes an alias      |
| Qualifier naming neither                            | `404 ResourceNotFoundException`                                                |

A qualified delete never touches `$LATEST`, the function record, other
versions, aliases or unqualified policies, and it does not rewind the version
counter — AWS never reuses a version number.

Tags are attached to the **unqualified** function ARN, never to a version or an
alias, so `TagResource`, `UntagResource` and `ListTags` reject a qualified ARN
with `InvalidParameterValueException`. They take an ARN, not a bare function
name: a name that is not a Lambda ARN fails the `TaggableResource` pattern.
Event source mappings are taggable through the same three operations, and
`CreateEventSourceMapping` accepts a `Tags` map so a mapping can be created
already tagged — which is what CloudFormation does on every deploy that carries
stack tags. Their tags are stored separately and never appear in
`EventSourceMappingConfiguration`, which has no `Tags` member, so `ListTags` is
the only way to read them back.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category                    | ✅ Supported | ❌ Unsupported |
| --------------------------- | ------------ | -------------- |
| Function management         | 10           |                |
| Resource-based policies     | 2            | 1              |
| Code signing                | 6            |                |
| Invocation                  | 2            | 1              |
| Aliases & versions          | 7            |                |
| Function URLs               | 5            |                |
| Event source mappings       | 5            |                |
| Layers                      | 5            |                |
| Asynchronous invocation     | 5            |                |
| Concurrency & configuration | 7            |                |
| Tags                        | 3            |                |

---

## Endpoints

### Function management

| Operation                         | Status       | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | AWS Docs                                                                                      |
| --------------------------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `ListFunctions`                   | ✅ Supported | Returns all stored functions; empty list if none                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListFunctions.html)                   |
| `CreateFunction`                  | ✅ Supported | Stores metadata; validates Runtime against the pinned model and refuses runtimes past AWS's block-create date; a modeled runtime with no execution image returns 501 and persists nothing; auto-creates CWL log group; VpcConfig and ImageConfig supported; LoggingConfig honoured for both Text and JSON LogFormat, with ApplicationLogLevel/SystemLogLevel filtering in JSON mode; FileSystemConfigs round-trip (EFS mounts in live mode; S3 Files runtime mounts tracked in #647); TracingConfig, EphemeralStorage and KMSKeyArn are validated, stored and echoed but change nothing — X-Ray tracing is not emulated, the ephemeral storage size is not enforced on the container, and environment variables are not encrypted at rest; DeadLetterConfig is validated, stored, echoed and honoured — a failed asynchronous invocation is delivered to the SQS queue or SNS topic it names; Code.S3ObjectVersion fetches that version of the object; an unfetchable Code.S3Bucket/S3Key/S3ObjectVersion fails the create and persists nothing | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_CreateFunction.html)                  |
| `DeleteFunction`                  | ✅ Supported | Qualifier deletes only that published version, with its qualified policy and provisioned concurrency; refuses $LATEST and versions an alias references; unqualified deletes the function                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunction.html)                  |
| `GetFunction`                     | ✅ Supported | Returns FunctionConfiguration + Code location block; TracingConfig and EphemeralStorage always present, defaulting to PassThrough and 512 MB as on AWS; DeadLetterConfig reported only when the function has one                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunction.html)                     |
| `GetFunctionConfiguration`        | ✅ Supported | Returns FunctionConfiguration only (no Code block); TracingConfig and EphemeralStorage always present, defaulting to PassThrough and 512 MB as on AWS; DeadLetterConfig reported only when the function has one                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionConfiguration.html)        |
| `UpdateFunctionCode`              | ✅ Supported | Updates code zip or image URI and Architectures; S3ObjectVersion fetches that version of the object, and a version that cannot be read leaves the function untouched; generates new RevisionId                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateFunctionCode.html)              |
| `UpdateFunctionConfiguration`     | ✅ Supported | Presence-aware updates for supported configuration, including TracingConfig, EphemeralStorage and KMSKeyArn (recorded and echoed, never enforced) and DeadLetterConfig (an explicit empty TargetArn removes the target); a Runtime past AWS's block-update date is refused; LoggingConfig with explicit members applies, including LogFormat JSON, but an explicitly empty LoggingConfig object still returns 501 because AWS's semantics for it are uncaptured (#660); unsupported advanced fields fail before mutation                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateFunctionConfiguration.html)     |
| `GetFunctionCodeSigningConfig`    | ✅ Supported | Returns the associated config; ResourceNotFoundException when the function has none                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionCodeSigningConfig.html)    |
| `PutFunctionCodeSigningConfig`    | ✅ Supported | Stores the association and validates the ARN shape; signature validation is not emulated                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PutFunctionCodeSigningConfig.html)    |
| `DeleteFunctionCodeSigningConfig` | ✅ Supported | Removes the association; idempotent                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunctionCodeSigningConfig.html) |

### Resource-based policies

| Operation          | Status         | Notes                                                                                                         | AWS Docs                                                                       |
| ------------------ | -------------- | ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `AddPermission`    | ❌ Unsupported | Policy lifecycle and validation are implemented, but statements are not yet enforced during invocation (#629) | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_AddPermission.html)    |
| `GetPolicy`        | ✅ Supported   | Returns the stored AWS policy document and revision ID                                                        | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetPolicy.html)        |
| `RemovePermission` | ✅ Supported   | Removes a statement by ID; supports revision preconditions                                                    | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_RemovePermission.html) |

### Code signing

| Operation                          | Status       | Notes                                                                                                 | AWS Docs                                                                                       |
| ---------------------------------- | ------------ | ----------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `CreateCodeSigningConfig`          | ✅ Supported | Stored as a real resource; AllowedPublishers required, UntrustedArtifactOnDeployment defaults to Warn | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_CreateCodeSigningConfig.html)          |
| `GetCodeSigningConfig`             | ✅ Supported | Returns the stored configuration                                                                      | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetCodeSigningConfig.html)             |
| `UpdateCodeSigningConfig`          | ✅ Supported | Partial update; omitted members keep their stored value                                               | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateCodeSigningConfig.html)          |
| `DeleteCodeSigningConfig`          | ✅ Supported | ResourceConflictException while a function still references it                                        | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteCodeSigningConfig.html)          |
| `ListCodeSigningConfigs`           | ✅ Supported | Region-scoped; pagination not implemented                                                             | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListCodeSigningConfigs.html)           |
| `ListFunctionsByCodeSigningConfig` | ✅ Supported | Returns the ARNs of functions referencing the configuration                                           | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListFunctionsByCodeSigningConfig.html) |

### Invocation

| Operation                  | Status         | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              | AWS Docs                                                                               |
| -------------------------- | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------- |
| `Invoke`                   | ✅ Supported   | Container-based execution via Docker; falls back to stub when Docker unavailable; under LogFormat JSON the START/END/REPORT lines become Telemetry-API-shaped platform.start, platform.runtimeDone and platform.report records, filtered by SystemLogLevel, and function output is filtered by ApplicationLogLevel; platform.initStart and platform.initReport are not emitted yet (#660); an InvocationType=Event invocation whose function errors is retried per the function's FunctionEventInvokeConfig (AWS's default of twice, waiting AWS's one minute then two, when unconfigured) and then delivered to its on-failure destination and its DeadLetterConfig target; MaximumEventAgeInSeconds is measured from acceptance and discards the event before the next attempt rather than after it, but the resulting record's condition reads RetriesExhausted because AWS does not document a distinct value for an aged-out event; records AWS/Lambda CloudWatch metrics (Invocations, Errors, Duration, Throttles, ConcurrentExecutions — service-metrics-platform.md Lambda pilot) at this outcome boundary for every invocation mechanism (sync, async, function URLs, event source mappings); DryRun never invokes and so never records; Resource/ExecutedVersion metric dimensions are not recorded yet | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html)                   |
| `InvokeAsync`              | ❌ Unsupported | stub; returns 501                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_InvokeAsync.html)              |
| `InvokeWithResponseStream` | ✅ Supported   | Invokes synchronously, wraps result in AWS event stream binary encoding (initial-response → PayloadChunk → InvokeComplete); RequestResponse only                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_InvokeWithResponseStream.html) |

### Aliases & versions

| Operation                | Status       | Notes                                                                                          | AWS Docs                                                                             |
| ------------------------ | ------------ | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| `PublishVersion`         | ✅ Supported | Immutable snapshot of function config; version numbers are monotonically incrementing integers | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PublishVersion.html)         |
| `ListVersionsByFunction` | ✅ Supported | Always includes `$LATEST` as first entry                                                       | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListVersionsByFunction.html) |
| `CreateAlias`            | ✅ Supported |                                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_CreateAlias.html)            |
| `UpdateAlias`            | ✅ Supported |                                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateAlias.html)            |
| `DeleteAlias`            | ✅ Supported |                                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteAlias.html)            |
| `GetAlias`               | ✅ Supported |                                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetAlias.html)               |
| `ListAliases`            | ✅ Supported |                                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListAliases.html)            |

### Function URLs

| Operation                 | Status       | Notes                                                                                                    | AWS Docs                                                                              |
| ------------------------- | ------------ | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `CreateFunctionUrlConfig` | ✅ Supported | FunctionUrl always echoes the caller's Host (see docs/networking.md); AuthType stored but never enforced | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_CreateFunctionUrlConfig.html) |
| `GetFunctionUrlConfig`    | ✅ Supported |                                                                                                          | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionUrlConfig.html)    |
| `UpdateFunctionUrlConfig` | ✅ Supported |                                                                                                          | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateFunctionUrlConfig.html) |
| `DeleteFunctionUrlConfig` | ✅ Supported |                                                                                                          | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunctionUrlConfig.html) |
| `ListFunctionUrlConfigs`  | ✅ Supported |                                                                                                          | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListFunctionUrlConfigs.html)  |

### Event source mappings

| Operation                  | Status       | Notes                                                                                                                                                | AWS Docs                                                                               |
| -------------------------- | ------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `CreateEventSourceMapping` | ✅ Supported | SQS→Lambda, DynamoDB Streams→Lambda; `FunctionResponseTypes: ["ReportBatchItemFailures"]` is honoured; Tags are stored and readable through ListTags | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_CreateEventSourceMapping.html) |
| `GetEventSourceMapping`    | ✅ Supported |                                                                                                                                                      | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetEventSourceMapping.html)    |
| `UpdateEventSourceMapping` | ✅ Supported |                                                                                                                                                      | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateEventSourceMapping.html) |
| `DeleteEventSourceMapping` | ✅ Supported |                                                                                                                                                      | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteEventSourceMapping.html) |
| `ListEventSourceMappings`  | ✅ Supported | Filters by `FunctionName` and `EventSourceArn`                                                                                                       | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListEventSourceMappings.html)  |

### Layers

| Operation             | Status       | Notes                                                           | AWS Docs                                                                          |
| --------------------- | ------------ | --------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `PublishLayerVersion` | ✅ Supported | Increments per-layer version counter; stores zip content        | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PublishLayerVersion.html) |
| `GetLayerVersion`     | ✅ Supported | Returns metadata and content info for the specified version     | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetLayerVersion.html)     |
| `ListLayerVersions`   | ✅ Supported | Returns all versions for a layer, newest first                  | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListLayerVersions.html)   |
| `ListLayers`          | ✅ Supported | Returns distinct layer names with their latest matching version | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListLayers.html)          |
| `DeleteLayerVersion`  | ✅ Supported | Removes the specific layer version; 404 if not found            | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteLayerVersion.html)  |

### Asynchronous invocation

| Operation                         | Status       | Notes                                                                                                                                                                                                                                                                                                                                                                                                                         | AWS Docs                                                                                      |
| --------------------------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `PutFunctionEventInvokeConfig`    | ✅ Supported | Overwrites the configuration, removing members the request omits; MaximumRetryAttempts and MaximumEventAgeInSeconds are validated against AWS's ranges and honoured by the async invoke path; SQS, SNS, Lambda and EventBridge destinations receive AWS's invocation record; an S3 on-failure destination returns 501 because the record is not written to S3, and an S3 on-success destination is rejected as AWS rejects it | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PutFunctionEventInvokeConfig.html)    |
| `UpdateFunctionEventInvokeConfig` | ✅ Supported | Partial update; members the request omits keep their stored value, which is the only difference from Put                                                                                                                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateFunctionEventInvokeConfig.html) |
| `GetFunctionEventInvokeConfig`    | ✅ Supported | ResourceNotFoundException when the function has no configuration; LastModified is Unix seconds, as AWS returns for this resource                                                                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionEventInvokeConfig.html)    |
| `DeleteFunctionEventInvokeConfig` | ✅ Supported | Returns 204; ResourceNotFoundException when there is no configuration to delete                                                                                                                                                                                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunctionEventInvokeConfig.html) |
| `ListFunctionEventInvokeConfigs`  | ✅ Supported | Every qualifier's configuration for the function; MaxItems is validated but the result is a single page, so NextMarker is never returned                                                                                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListFunctionEventInvokeConfigs.html)  |

### Concurrency & configuration

| Operation                            | Status       | Notes                                                                                                                                                                                                                                      | AWS Docs                                                                                         |
| ------------------------------------ | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------ |
| `PutFunctionConcurrency`             | ✅ Supported | Enforced: over-limit invokes get 429 TooManyRequestsException; 0 throttles the function entirely                                                                                                                                           | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PutFunctionConcurrency.html)             |
| `GetFunctionConcurrency`             | ✅ Supported | A function with no reservation answers 200 with an empty body, as on AWS; a reservation of 0 is reported rather than omitted, since 0 is the documented way to switch a function off. ResourceNotFoundException is for the function itself | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionConcurrency.html)             |
| `DeleteFunctionConcurrency`          | ✅ Supported | Clears reserved concurrency limit; returns 204                                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunctionConcurrency.html)          |
| `PutProvisionedConcurrencyConfig`    | ✅ Supported | Pre-warms the requested execution environments in the background (IN_PROGRESS then READY); FAILED when Docker is unavailable                                                                                                               | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PutProvisionedConcurrencyConfig.html)    |
| `GetProvisionedConcurrencyConfig`    | ✅ Supported | Reports live Allocated/Available; ProvisionedConcurrencyConfigNotFoundException if not set                                                                                                                                                 | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetProvisionedConcurrencyConfig.html)    |
| `DeleteProvisionedConcurrencyConfig` | ✅ Supported | Releases the reservation; the environments age out on the idle TTL rather than being killed                                                                                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteProvisionedConcurrencyConfig.html) |
| `ListProvisionedConcurrencyConfigs`  | ✅ Supported | Single page; NextMarker is always null                                                                                                                                                                                                     | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListProvisionedConcurrencyConfigs.html)  |

### Tags

| Operation       | Status       | Notes                                                                                                                                  | AWS Docs                                                                    |
| --------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| `TagResource`   | ✅ Supported | Function and event-source-mapping ARNs; merges tags; max 50; validates key/value lengths; rejects qualified ARNs and non-ARN resources | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_TagResource.html)   |
| `UntagResource` | ✅ Supported | Removes specified keys; idempotent on missing keys; tagKeys is required                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UntagResource.html) |
| `ListTags`      | ✅ Supported | Returns the resource's tags; code-signing-config, capacity-provider and network-connector ARNs return 501                              | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListTags.html)      |

<!-- END overcast:capabilities -->
