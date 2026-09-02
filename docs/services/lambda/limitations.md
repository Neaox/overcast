---
title: "Lambda limitations"
description: "Where Overcast's Lambda diverges from AWS: async retries, concurrency limits, runtime coverage, the JSON log record vocabulary, and VPC placement."
section: "Service Reference"
tags:
  - docs
  - lambda
  - limitations
  - services
---

# Lambda limitations

Back to [Lambda](../lambda.md).

## Divergences

| Area | Divergence |
| --- | --- |
| Async invocation | Retries, destinations and DLQ all work; only the *exhausted*-concurrency retry-to-queue case differs from AWS |
| `TracingConfig` / `EphemeralStorage` / `KMSKeyArn` | Stored and returned, never enforced |
| Resource policies | `AddPermission` stores and validates; statements are not evaluated at invoke time |
| Cold-start latency | Not simulated |
| Concurrency quotas | Account-wide quotas and requests-per-second limits are not emulated; only reserved concurrency is enforced |
| Runtime environment validation | Minimal |
| Extension telemetry (Logs API / Telemetry API) | HTTP destinations only |
| SnapStart | Not emulated — no restore records; `platform.runtimeDone` reports only `responseLatency` |
| `LoggingConfig: {}` (explicitly empty) | `UpdateFunctionConfiguration` returns `501` rather than guessing no-op vs. reset |
| Unqualified `DeleteFunction` | Removes the function record; versions, aliases and version counters are left behind |
| Tagging | Functions and event source mappings only; other taggable resources return `501` |
| Pinned `Code.S3ObjectVersion` | Excluded from the reactive S3 code sync |
| Reactive S3 code sync | Only moves a function onto bytes it is not already running |

### Asynchronous invocation

`PutFunctionEventInvokeConfig` and its family are implemented, so
`MaximumRetryAttempts` (0–2) and `MaximumEventAgeInSeconds` (60–21600) apply per
function, version or alias. A function with no configuration gets AWS's defaults:
two retries, waiting one minute then two. On-success and on-failure
**destinations** receive AWS's invocation record — the envelope naming the
request, the condition, the attempt count and the function's response — and a
`DeadLetterConfig` receives the event itself, so a function configured with both
gets both. All of it applies equally to events from S3 notifications, EventBridge
and Scheduler targets, and SNS `lambda` subscriptions, which share the async path.

- **`MaximumEventAgeInSeconds` is measured from acceptance**, covering the waits
  between attempts and the time the handler spends running, and an event that
  outlives it is discarded before the next attempt rather than after it — which is
  where AWS checks it. At AWS's retry waits its sixty-second minimum is reachable
  in the ordinary case: an event whose first attempt fails has already expired
  when the one-minute wait ends, so it never runs again.
- **An aged-out record's `condition` reads `RetriesExhausted`**, which is probably
  not what AWS sends — its console describes an on-failure destination as firing
  when an event "fails all processing attempts *or* exceeds the maximum age", so
  the two are distinct. AWS documents no string for the second, and inventing one
  would be worse than reusing a real one.
- A function throttled by its **own reserved concurrency** is the documented
  exception and is not retried: AWS sends those events to the dead-letter queue
  "without any retries", and Overcast does the same once the invocation has waited
  out its concurrency back-off. A function throttled by *exhausted* concurrency
  rather than a reserve of zero is the case Overcast still gets wrong — AWS
  returns those to the queue for up to six hours; Overcast dead-letters them.
- **An S3 on-failure destination is refused** with `501` rather than accepted and
  silently never written. An S3 *on-success* destination is rejected with
  `InvalidParameterValueException`, which is what AWS does — S3 is on-failure only.

### Recorded but not honoured

`TracingConfig`, `EphemeralStorage` and `KMSKeyArn` are validated against AWS's
own constraints, stored, and returned by `GetFunction`,
`GetFunctionConfiguration`, `CreateFunction` and `UpdateFunctionConfiguration`, so
a template or SDK client reads back exactly what it set. None of them changes what
the function does:

- **X-Ray tracing is not emulated at all.** No segment is recorded and no trace
  exists, whichever `Mode` is set; `Active` and `PassThrough` behave identically.
- **The ephemeral storage size is not enforced.** A function configured with
  512 MB of `/tmp` gets whatever the Docker host gives it, normally far more.
- **The KMS key is an association only.** Environment variables are stored in
  plaintext, as all Overcast state is.

### Other divergences

- **An update that is already applied says so.** AWS answers every update
  `LastUpdateStatus: InProgress` and settles a moment later; Overcast answers
  `Successful`, because a zip deployment and every `UpdateFunctionConfiguration`
  are durably applied before the call returns. `aws lambda wait function-updated`
  returns on its first poll rather than never (#1550). The one update that really
  is asynchronous — `UpdateFunctionCode` pointing a `PackageType=Image` function
  at a new image — answers `InProgress` and settles to `Successful`, or `Failed`
  with `ImageAccessDenied`/`InvalidImage`/`InternalError`, when the pull does.
- Extension telemetry subscriptions — the Logs API (`PUT /2020-08-15/logs`) and
  the Telemetry API (`PUT /2022-07-01/telemetry`) — support HTTP destinations
  only. Buffering configuration is honoured with AWS's defaults; out-of-range
  values are clamped to the documented limits rather than rejected, because AWS's
  rejection behaviour is not documented.
- SnapStart restore records (`platform.restoreStart`, `platform.restoreRuntimeDone`,
  `platform.restoreReport`) are not emitted, because SnapStart itself is not
  emulated. Of the documented `platform.runtimeDone` spans only `responseLatency`
  is emitted: `responseDuration` ends only after the answer has finished streaming
  through the init's unbuffered proxy, and `runtimeOverhead` exists only at the
  runtime's next poll — both after the record is already on its way.
- `UpdateFunctionConfiguration` with an explicitly empty `LoggingConfig: {}`
  returns `501`. AWS's semantics for that shape have not been captured, and
  guessing between "no-op" and "reset to defaults" would mutate the function
  either way. `LoggingConfig` with explicit members applies normally.
- Tags are stored for functions and event source mappings only. Code signing
  configurations, capacity providers and network connectors return `501` from the
  tag operations.
- A function whose code names a `Code.S3ObjectVersion` is pinned to that version
  and excluded from the reactive S3 code sync that refreshes an unpinned function
  when a new object lands at its `S3Bucket`/`S3Key`. That matches AWS; use
  `UpdateFunctionCode` to move it.
- That sync only moves a function onto bytes it is not already running. A
  `PutObject` re-uploading an unchanged asset, or one landing just before a
  `CreateFunction` reads the same key, is not a new deployment: `RevisionId` and
  `LastModified` stay put and the warm environment survives.

## Deleting a version, and where tags live

`DeleteFunction` means two different things depending on whether you pass a
qualifier — either as `?Qualifier=` or inside the function name (`my-function:2`):

| Request | Effect |
| --- | --- |
| `DELETE /functions/my-function` | Deletes the function, its package and its resource policies |
| `DELETE /functions/my-function?Qualifier=2` | Deletes **only** published version 2, its qualified policy and its provisioned concurrency |
| `?Qualifier=$LATEST` | `400 InvalidParameterValueException` — `$LATEST` only goes with the function |
| Qualifier naming a version an alias points at | `409 ResourceConflictException` naming the aliases |
| Qualifier naming an alias | `409 ResourceConflictException` — `DeleteFunction` never deletes an alias |
| Qualifier naming neither | `404 ResourceNotFoundException` |

A qualified delete never touches `$LATEST`, the function record, other versions,
aliases or unqualified policies, and it does not rewind the version counter — AWS
never reuses a version number.

Tags attach to the **unqualified** function ARN, never to a version or alias, so
`TagResource`, `UntagResource` and `ListTags` reject a qualified ARN with
`InvalidParameterValueException`. They take an ARN, not a bare function name.
Event source mappings are taggable through the same three operations, and
`CreateEventSourceMapping` accepts a `Tags` map — which is what CloudFormation
does on every deploy carrying stack tags. Their tags are stored separately and
never appear in `EventSourceMappingConfiguration`, so `ListTags` is the only way
to read them back.

## Concurrency and execution environments

Containers are reused for sequential invocations and scaled out one per
concurrent invocation, the same way Lambda reuses and scales execution
environments. After a burst, the surplus stays warm until the 15-minute idle
sweep.

| Limit | Environment variable | Default | When reached |
| --- | --- | --- | --- |
| Containers across all functions | `LAMBDA_MAX_INSTANCES` | auto (25 fallback) | Reclaim an idle container → queue → throttle |
| Containers for one function | `LAMBDA_MAX_INSTANCES_PER_FUNCTION` | auto (10 fallback) | Queue → throttle |
| Aggregate container memory (Σ `MemorySize`) | `LAMBDA_MAX_MEMORY_MB` | auto (unlimited fallback) | Reclaim an idle container → queue → throttle |
| Idle containers kept per function | `LAMBDA_MAX_WARM_INSTANCES` | 10 | Surplus destroyed on release |
| Concurrent container starts | `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` | auto (4 fallback) | Starts queue behind a semaphore |
| `ReservedConcurrentExecutions` | per function, AWS API | unset | Throttle immediately |

Derivations and every other `LAMBDA_*` variable are in
[Configuration](../../configuration.md).

An invocation that cannot get a container first reclaims the least-recently-used
idle one (never a provisioned one), then waits — and if it is still waiting when
the function's timeout expires it is throttled, with a 429
`TooManyRequestsException` and `Reason: ConcurrentInvocationLimitExceeded`. The
instance limits protect your machine; they are not an emulation of AWS's account
quota.

The memory budget counts bytes, not containers: a new container is admitted only
while `Σ MemorySize` of live containers plus its own stays inside the budget. Each
container is hard-capped at its function's `MemorySize` with swap disabled, so the
sum is a real bound. While the pool sits above ~90% of the budget, a container
whose invocation just finished is destroyed instead of kept warm, so queued work
regains budget without waiting for the sweep; provisioned environments are always
kept and a running invocation is never interrupted. Entering and leaving that
regime is logged once each — expect cold starts while it lasts.

`ReservedConcurrentExecutions` is enforced with AWS's semantics instead: no
queueing, an immediate 429 with
`Reason: ReservedFunctionConcurrentInvocationLimitExceeded`. Setting it to 0
disables the function, the same idiom that works on AWS.

Asynchronous invocations are never throttled back to the caller — they were
already answered 202 — and are retried internally, on a much shorter budget than
AWS's six hours. Event source mappings behave the same way: a throttled batch is
left in flight so the messages return on the visibility timeout. This holds
however the event was raised: an S3 notification, an EventBridge or Scheduler
target and an SNS `lambda` subscription all enter the same async path.

### Provisioned concurrency

Environments are created in the background (`Status: IN_PROGRESS`, then `READY`),
held open regardless of the idle sweep, replenished when one is lost, and rebuilt
against the new configuration after a code or config update. Containers report
`AWS_LAMBDA_INITIALIZATION_TYPE=provisioned-concurrency`, which is what Powertools
and similar libraries read to classify a cold start.

Provisioned concurrency is a **floor, not a ceiling**: when all reserved
environments are busy, further invocations spill over into on-demand capacity with
a cold start rather than being throttled — matching AWS, where only reserved
concurrency caps a function. A reservation is never reclaimed for another
function. Without Docker nothing can be allocated, so the config is stored and
reported `Status: FAILED` with a `StatusReason` rather than claiming `READY`.

### Environment lifecycle

A container is created with the function's code, environment variables, memory
limit, timeout, handler, layers, logging configuration and VPC attachment fixed at
start — a running container can never observe a change to any of them. So an
`UpdateFunctionCode` or `UpdateFunctionConfiguration` that changes one retires the
environment immediately:

- An **idle** container is destroyed as soon as the update is stored.
- A container **serving an invocation** finishes it in the old environment and is
  destroyed on completion. In-flight invocations are never interrupted.
- **No replacement is started** for an on-demand environment; the next invocation
  cold starts one. A provisioned reservation *is* rebuilt in the background.

Updates that change nothing the container can observe — the description or the
role — leave the warm container in place, so cosmetic edits cost no cold start.
Deleting a function destroys its containers immediately, and a container that
disappears without Overcast asking (`docker rm -f`, an OOM kill, a Docker restart)
is dropped from the warm set as soon as the Docker event stream reports it.

`GET /_overcast/lambda/instances` reports one entry per execution environment,
each carrying an `initOrigin` of `on-demand`, `proactive` or `provisioned` — fixed
for the environment's lifetime. The `lambda:InstanceEvicted` event additionally
carries `evictedReason`: `idle-ttl`, `config-change`, `function-deleted`,
`container-died`, `unhealthy`, `surplus`, `memory-pressure` or `shutdown`.

### CPU is shared with the init, and small functions feel it in bursts

Every execution environment runs an Overcast init process as PID 1 — the parent of
the runtime, and what makes an invocation's log attribution exact. It is inside
the container, so its CPU comes out of the function's allocation, exactly as AWS's
own RAPID init does.

Lambda's allocation is proportional to memory, so a 128 MB function gets about 7%
of a vCPU, and the kernel enforces that as a quota per 100 ms period. Under a
sustained burst of back-to-back invocations, the small amount of extra CPU the
init uses is enough to exhaust that quota, and the container is throttled until
the period rolls over. Measured on a 128 MB `nodejs22.x` hello-world driven with
no think time: warm p50 is unchanged at ~5 ms, but roughly one invocation in eight
stalls for ~80 ms, taking p95 from ~15 ms to ~80 ms.

It is not a defect and it is not emulated away — throttling a container that
overruns its CPU allocation is what Lambda does. It disappears entirely with more
memory (at 1769 MB, a full vCPU, there are no stalls at all) or any gap between
invocations.

### Init delivery is shared across instances

That init binary reaches each container through a named Docker volume
(`overcast-lambda-init-<hash>-<arch>`) rather than a fresh copy per cold start;
its name is content-addressed, so any Overcast instance on the same daemon can
safely reuse one seeded by another instance's build — but only the instance
that seeded a volume prunes or removes it, so two Overcasts sharing a daemon
never delete a volume the other is still using. A volume this instance reused
but does not own, and then found empty and could not remove, is not reused
again — the affected cold starts fall back to copying the init into the
container instead, and it's surfaced as an informational advisory on
`GET /_overcast/debug/metrics` rather than only a debug-level log line.

## Runtimes

Every runtime identifier comes from one table, so request validation, the
runtime-to-image mapping, AWS's deprecation dates and the web console's create wizard
cannot disagree. Runtime identifiers are the modelled `Runtime` enum from the
pinned AWS Smithy model; deprecation, block-create and block-update dates come
from AWS's published runtime lifecycle table.

| Situation | Response |
| --- | --- |
| Not a value of the modelled `Runtime` enum, and not a runtime-version ARN | `400 InvalidParameterValueException` listing every modelled value |
| Modelled, but past AWS's deprecation phase for the operation | `400 InvalidParameterValueException` naming the recommended successor |
| Modelled and accepted by AWS, but Overcast has no execution image for it | `501 NotImplemented` — an honest emulator gap; nothing is persisted |

The third case is the important one: a missing execution image is **not** a bad
request, so Overcast never invents a `400` for it. (The check applies to `Zip`
packages; an `Image` function brings its own.)

AWS retires a runtime in three steps and Overcast observes each against the
current date: the **deprecation date** ends support but leaves the runtime
deployable and invokable; **block function create** starts refusing
`CreateFunction`; **block function update** starts refusing
`UpdateFunctionConfiguration`. So a deprecated runtime is not automatically
refused — `python3.9` lost support in December 2025 but stays deployable until its
block-create date.

Overcast maps an official base image to every runtime AWS still accepts for
`CreateFunction`, across Node.js, Python, Java (including the Amazon Linux 2023
variants), .NET, Ruby and the `provided` custom runtimes. Runtimes AWS has already
blocked from `CreateFunction` — `go1.x`, `java8`, `python3.7` and older,
`nodejs14.x` and older, the `dotnetcore` family, `ruby2.x` and `provided` — carry
no image, so the `400` above is the only response they can produce.

`LAMBDA_SEED_RUNTIME_IMAGES=true` pre-pulls only the images for runtimes AWS still
supports; a deprecated-but-deployable runtime is pulled on demand at its first
cold start. `GET /_overcast/lambda/runtimes` returns the catalog with each
runtime's `supported`, `deprecated`, `createBlocked` and `updateBlocked` flags.

## Log format and log levels

`LogFormat` decides what Overcast writes around each invocation, and under `JSON`
the two log levels decide how much of it reaches CloudWatch Logs. `LogGroup` is
respected too: a function with a custom group writes there, and the group is
created on `CreateFunction` like the default `/aws/lambda/<function-name>` one.

**Text** is the default and unchanged: the plain-text `START`, `END` and `REPORT`
lines real Lambda writes, byte for byte. `ApplicationLogLevel` and
`SystemLogLevel` do not apply to Text on AWS, so Overcast filters nothing in this
mode.

**JSON** replaces those three lines with the events AWS publishes through the
Telemetry API, one object per line, shaped `{"time", "type", "record"}`:

| Event `type` | Replaces | System log level | Record |
| --- | --- | --- | --- |
| `platform.initStart` | — | `DEBUG` | `initializationType`, `phase`, `functionName`, `functionVersion` |
| `platform.initRuntimeDone` | — | `DEBUG` on success, else `WARN` | `initializationType`, `phase`, `status` |
| `platform.initReport` | — | `DEBUG` on-demand, `INFO` provisioned, `WARN` when it failed | plus `metrics`: `durationMs` |
| `platform.start` | `START` | `INFO` | `requestId`, `version`, `tracing` |
| `platform.runtimeDone` | `END` | `DEBUG` on success, else `WARN` | `requestId`, `status`, `tracing`, `spans` (`responseLatency`), `metrics`: `durationMs`, `producedBytes` |
| `platform.report` | `REPORT` | `INFO` on success, else `WARN` | `requestId`, `status`, `tracing`, `metrics`: `durationMs`, `billedDurationMs`, `memorySizeMB`, `maxMemoryUsedMB` |

The levels are AWS's own system-log-level mapping, not an Overcast convention.
`status` is `success`, `failure` (the handler returned an error), `error` (the
environment ended the invocation) or `timeout`. `metrics.initDurationMs` appears
in `platform.report` only on the first report of an on-demand cold start.
`tracing` carries the `X-Amzn-Trace-Id` the runtime genuinely received; `spanId`
stays absent, because Overcast mints no spans.

`platform.runtimeDone`'s metrics are measured by the in-container init — the
invocation being handed to the runtime, to its answer arriving back at the init's
proxy — so they are a different, smaller span than `platform.report`'s
host-measured `durationMs`, as AWS's two records are two different measurements.
The host's own measurements are the fallback where the runtime never answered.
Overcast emits only the subset of the schema it genuinely observes: `errorType` is
populated for the one outcome whose AWS name is documented (a runtime that exited
is `Runtime.ExitError`), and `platform.initStart` omits `runtimeVersion`,
`runtimeVersionArn`, `instanceId` and `instanceMaxMemory` for the same reason.

The three **init-phase** records come from the in-container init, which is PID 1
and the proxy in front of the Runtime API — so it sees the phase begin and end
without inferring either, and they travel on the same sequence-ordered stream as
the container's own output. All three are `DEBUG` for a successful on-demand cold
start, so a default log stream never shows them; a provisioned environment's
`platform.initReport` is `INFO`, and an environment whose runtime died before
asking for work reports `status: error` on both closing records at `WARN`.
`platform.initReport`'s `durationMs` is measured inside the environment, a
different span from the host-measured `Init Duration`; the two differ by a few
milliseconds. In **Text** format none of the three is written to CloudWatch Logs.

### Filtering

Both levels default to `INFO` under `JSON`, as on AWS. The ordering is `TRACE` <
`DEBUG` < `INFO` < `WARN` < `ERROR` < `FATAL`, and a record is kept when its own
level is at or above the configured one.

- **`SystemLogLevel`** filters the platform records above, by the level in the
  table.
- **`ApplicationLogLevel`** filters the function's own stdout and stderr, by the
  `"level"` member of each record. A line that parses as a JSON object with a
  recognised level is filtered on it; everything else — unstructured text,
  malformed JSON, a missing or unknown level — is treated as `INFO`, which is what
  AWS documents. Level names are matched case-insensitively.

Filtering decides what reaches **CloudWatch Logs and the `X-Amz-Log-Result`
tail** only. Telemetry and Logs API subscribers receive the complete set either
way — AWS is explicit that the CloudWatch system log level does not affect
Telemetry API behaviour.

The configuration is handed to the runtime the way AWS hands it over, because the
managed runtimes and Powertools read it: `AWS_LAMBDA_LOG_FORMAT` is always set to
`Text` or `JSON`, and `AWS_LAMBDA_LOG_LEVEL` is set to the effective
`ApplicationLogLevel` only under `JSON`. Because those values are baked into a
container at start, changing `LogFormat` or either level retires the warm
environments.

## Partial batch responses

An event source mapping created with
`FunctionResponseTypes: ["ReportBatchItemFailures"]` is honoured, not just stored.
The poller reads the response and acts on the records it names:

```json
{ "batchItemFailures": [{ "itemIdentifier": "<message id or sequence number>" }] }
```

- **SQS.** Only the messages the function did *not* report are deleted. A reported
  message stays in flight and becomes visible again when its visibility timeout
  expires, which is exactly how AWS redelivers it. The queue's own `RedrivePolicy`
  then counts the receive and dead-letters on schedule.
- **DynamoDB Streams.** The batch is retried from the earliest record the function
  named, and everything before it is treated as done. Records *after* it are
  redelivered even if the function said they succeeded, because a stream is
  ordered and AWS checkpoints at the lowest reported sequence number.

AWS's edge cases are reproduced. An empty or absent `batchItemFailures` list — or
no response at all — means the whole batch succeeded, so turning the flag on cannot
change a handler that does not use it. A response Overcast cannot read is a
complete batch *failure* and nothing is acknowledged: invalid JSON, an entry that
is not an object, a missing, empty or non-string `itemIdentifier`, and an
identifier naming a record that was not in the batch. Each is logged with the
reason.

One deliberate divergence: the member names are matched case-insensitively, so a
handler written against a strongly typed SDK that serialises `BatchItemFailures`
is honoured rather than read as malformed. `FunctionResponseTypes` itself is
validated against AWS's one-member enum.

## VPC placement — `VpcConfig`

A function with a `VpcConfig` naming subnets is placed on that VPC's network and
nothing else. It can reach what is in the VPC with it, and cannot reach a
container outside it — which is what a `VpcConfig` means on AWS, where placement
subtracts rather than adds.

Overcast's own API endpoint is the exception and stays reachable from every
function regardless of placement. `AWS_ENDPOINT_URL` and the Lambda Runtime API
ride a separate control plane, so calling S3 or DynamoDB from inside a VPC works
here without the NAT gateway or VPC endpoint AWS would need. That is a deliberate
divergence: the alternative is every VPC-placed function failing on its first SDK
call.

The common mistake this catches is a database in a VPC and a function without a
`VpcConfig` — which never worked on AWS. The fix is the AWS one: put the function
in the VPC, or set `PubliclyAccessible` on the instance. A refused connection is
named rather than left to hang; see
[Networking § Lambda, ECS and VPCs](../../networking.md).
