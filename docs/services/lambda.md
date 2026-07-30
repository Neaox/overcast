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

- Async invocation (`InvocationType: Event`) is not yet implemented.
- Cold-start latency simulation is not implemented.
- Account-wide concurrency quotas and requests-per-second limits are not
  emulated; only per-function reserved concurrency is enforced.
- Runtime-specific environment validation is minimal.
- Lambda extensions support currently covers Docker-backed zip functions. Image
  function extension startup is not yet wrapped.
- Extension Logs API support is limited to HTTP destinations and best-effort
  delivery. Telemetry API subscriptions are not yet implemented.

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

The web UI's system map and the `GET /_lambda/instances` endpoint report one
entry per execution environment, so a function serving five concurrent
invocations shows five instances. A retired environment stops being listed at
the moment it is retired rather than lingering until the idle timeout.

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

Docker-backed zip functions start executable external extensions found directly
under `/opt/extensions` in attached layers before the runtime entrypoint starts.
Layer file modes are preserved, so extension binaries and scripts must be
executable in the layer zip.

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
records; synthesized START/END/REPORT lines are delivered as `platform`
records. Delivery is best-effort and does not yet implement the full Lambda
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

Enable the global feature flag when starting Overcast:

```bash
OVERCAST_LAMBDA_HOT_RELOAD=true overcast serve
# or
docker run -e OVERCAST_LAMBDA_HOT_RELOAD=true overcast
```

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
| `LAMBDA_SEED_RUNTIME_IMAGES`          | `false`          | Pre-pull every managed runtime image at startup         |
| `LAMBDA_TAR_CACHE_MB`                 | `256`            | In-memory cache of pre-built code/layer tars (0 = off)  |
| `LAMBDA_PROACTIVE_INIT`               | `false`          | Pre-initialize an environment after config settles      |
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
environments" ahead of traffic; with `LAMBDA_PROACTIVE_INIT=true` Overcast
mirrors that. Ten seconds after a function's code or configuration stops
changing (so a CDK deploy's create-then-update burst collapses into one
attempt), one execution environment is created in the background — for
functions that have been invoked this session or have a function URL or
event source mapping — so the next request lands warm. The environment
reports `AWS_LAMBDA_INITIALIZATION_TYPE=on-demand` and its first REPORT line
carries no `Init Duration`, exactly like a proactively initialized
environment on AWS. Creation only proceeds when capacity is idle: it never
queues ahead of a real invocation, respects the instance and memory budgets,
and the environment ages out through the normal idle sweep.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category                    | ✅ Supported | ❌ Unsupported |
| --------------------------- | ------------ | -------------- |
| Function management         | 10           |                |
| Code signing                | 6            |                |
| Invocation                  | 2            | 1              |
| Aliases & versions          | 7            |                |
| Function URLs               | 5            |                |
| Event source mappings       | 5            |                |
| Layers                      | 5            |                |
| Concurrency & configuration | 7            |                |

---

## Endpoints

### Function management

| Operation                         | Status       | Notes                                                                                                                             | AWS Docs                                                                                      |
| --------------------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `ListFunctions`                   | ✅ Supported | Returns all stored functions; empty list if none                                                                                  | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListFunctions.html)                   |
| `CreateFunction`                  | ✅ Supported | Stores metadata; validates runtime; deprecated runtimes rejected; auto-creates CWL log group; VpcConfig and ImageConfig supported | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_CreateFunction.html)                  |
| `DeleteFunction`                  | ✅ Supported |                                                                                                                                   | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunction.html)                  |
| `GetFunction`                     | ✅ Supported | Returns FunctionConfiguration + Code location block                                                                               | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunction.html)                     |
| `GetFunctionConfiguration`        | ✅ Supported | Returns FunctionConfiguration only (no Code block)                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionConfiguration.html)        |
| `UpdateFunctionCode`              | ✅ Supported | Updates code zip; generates new RevisionId                                                                                        | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateFunctionCode.html)              |
| `UpdateFunctionConfiguration`     | ✅ Supported | Patches Timeout/MemorySize/Description/Handler/Role/Environment/Layers/VpcConfig/ImageConfig; generates new RevisionId            | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateFunctionConfiguration.html)     |
| `GetFunctionCodeSigningConfig`    | ✅ Supported | Returns the associated config; ResourceNotFoundException when the function has none                                               | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionCodeSigningConfig.html)    |
| `PutFunctionCodeSigningConfig`    | ✅ Supported | Stores the association and validates the ARN shape; signature validation is not emulated                                          | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PutFunctionCodeSigningConfig.html)    |
| `DeleteFunctionCodeSigningConfig` | ✅ Supported | Removes the association; idempotent                                                                                               | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunctionCodeSigningConfig.html) |

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

| Operation                  | Status         | Notes                                                                                                                                            | AWS Docs                                                                               |
| -------------------------- | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------- |
| `Invoke`                   | ✅ Supported   | Container-based execution via Docker; falls back to stub when Docker unavailable                                                                 | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_Invoke.html)                   |
| `InvokeAsync`              | ❌ Unsupported | stub; returns 501                                                                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_InvokeAsync.html)              |
| `InvokeWithResponseStream` | ✅ Supported   | Invokes synchronously, wraps result in AWS event stream binary encoding (initial-response → PayloadChunk → InvokeComplete); RequestResponse only | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_InvokeWithResponseStream.html) |

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

| Operation                  | Status       | Notes                                          | AWS Docs                                                                               |
| -------------------------- | ------------ | ---------------------------------------------- | -------------------------------------------------------------------------------------- |
| `CreateEventSourceMapping` | ✅ Supported | SQS→Lambda, DynamoDB Streams→Lambda            | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_CreateEventSourceMapping.html) |
| `GetEventSourceMapping`    | ✅ Supported |                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetEventSourceMapping.html)    |
| `UpdateEventSourceMapping` | ✅ Supported |                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateEventSourceMapping.html) |
| `DeleteEventSourceMapping` | ✅ Supported |                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteEventSourceMapping.html) |
| `ListEventSourceMappings`  | ✅ Supported | Filters by `FunctionName` and `EventSourceArn` | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListEventSourceMappings.html)  |

### Layers

| Operation             | Status       | Notes                                                           | AWS Docs                                                                          |
| --------------------- | ------------ | --------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `PublishLayerVersion` | ✅ Supported | Increments per-layer version counter; stores zip content        | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PublishLayerVersion.html) |
| `GetLayerVersion`     | ✅ Supported | Returns metadata and content info for the specified version     | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetLayerVersion.html)     |
| `ListLayerVersions`   | ✅ Supported | Returns all versions for a layer, newest first                  | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListLayerVersions.html)   |
| `ListLayers`          | ✅ Supported | Returns distinct layer names with their latest matching version | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListLayers.html)          |
| `DeleteLayerVersion`  | ✅ Supported | Removes the specific layer version; 404 if not found            | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteLayerVersion.html)  |

### Concurrency & configuration

| Operation                            | Status       | Notes                                                                                                                        | AWS Docs                                                                                         |
| ------------------------------------ | ------------ | ---------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `PutFunctionConcurrency`             | ✅ Supported | Enforced: over-limit invokes get 429 TooManyRequestsException; 0 throttles the function entirely                             | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PutFunctionConcurrency.html)             |
| `GetFunctionConcurrency`             | ✅ Supported | Returns 404 if no concurrency limit is set                                                                                   | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionConcurrency.html)             |
| `DeleteFunctionConcurrency`          | ✅ Supported | Clears reserved concurrency limit; returns 204                                                                               | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunctionConcurrency.html)          |
| `PutProvisionedConcurrencyConfig`    | ✅ Supported | Pre-warms the requested execution environments in the background (IN_PROGRESS then READY); FAILED when Docker is unavailable | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PutProvisionedConcurrencyConfig.html)    |
| `GetProvisionedConcurrencyConfig`    | ✅ Supported | Reports live Allocated/Available; ProvisionedConcurrencyConfigNotFoundException if not set                                   | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetProvisionedConcurrencyConfig.html)    |
| `DeleteProvisionedConcurrencyConfig` | ✅ Supported | Releases the reservation; the environments age out on the idle TTL rather than being killed                                  | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteProvisionedConcurrencyConfig.html) |
| `ListProvisionedConcurrencyConfigs`  | ✅ Supported | Single page; NextMarker is always null                                                                                       | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListProvisionedConcurrencyConfigs.html)  |

<!-- END overcast:capabilities -->
