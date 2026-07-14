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
- Runtime-specific environment validation is minimal.
- Lambda extensions support currently covers Docker-backed zip functions. Image
  function extension startup is not yet wrapped.
- Extension Logs API support is limited to HTTP destinations and best-effort
  delivery. Telemetry API subscriptions are not yet implemented.

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

Overcast injects `AWS_ENDPOINT_URL`, `AWS_ENDPOINT_URL_SSM`, and
`AWS_ENDPOINT_URL_SECRETS_MANAGER` into Docker Lambda containers so SDK-backed
functions and extensions can route AWS service calls back to the emulator.
Verified with AWS Parameters and Secrets Lambda Extension 1.0.342
(2026-07-14): SSM requests honor `AWS_ENDPOINT_URL_SSM` and Secrets Manager
requests honor `AWS_ENDPOINT_URL_SECRETS_MANAGER`, allowing the real extension
layer to fetch Overcast parameters and secrets.

---

## Lambda Hot Reload Guide

Hot reload mounts your local source directory into the Lambda runtime at
`/var/task` so code edits are picked up on the next invoke, without uploading
a new zip.

This mode is intentionally opt-in and intended for local development.

### When to use it

- Fast inner-loop development for interpreted runtimes (Node.js, Python).
- Debugging handler logic where packaging slows iteration.

### When not to use it

- Production-like packaging validation (use normal zip/image deploy path).
- Functions that require Lambda layers in Overcast hot-reload mode.

### Prerequisites

1. Enable global feature flag:

```bash
export OVERCAST_LAMBDA_HOT_RELOAD=true
```

2. Docker-backed Lambda execution enabled in your environment.
3. Function created with tag `overcast:hot-reload-path` pointing to an
   absolute host path.

### Create a hot-reload function (AWS CLI)

```bash
aws --endpoint-url http://localhost:4566 lambda create-function \
   --function-name demo-hot \
   --runtime nodejs20.x \
   --handler index.handler \
   --role arn:aws:iam::000000000000:role/lambda-role \
   --zip-file fileb://minimal.zip \
   --tags overcast:hot-reload-path=/absolute/path/to/lambda/source
```

Notes:

- Path must be absolute.
- Windows drive paths are normalized (for example,
  `C:\Users\you\app` -> `/c/Users/you/app`).
- Mount is read-only inside the container (`/var/task:ro`).

### Invoke and iterate

```bash
aws --endpoint-url http://localhost:4566 lambda invoke \
   --function-name demo-hot out.json
```

Edit local files in the configured source path and invoke again.

### Behavior and caveats

- Layers are supported in hot-reload mode. Attached layer archives are
  expanded into `/opt` before the Lambda container starts.
- If multiple attached layers provide the same file path, later layers in the
  function's `Layers` list override earlier ones.
- Parallel invocations of the same function share one mounted source tree.
  This is convenient for dev, but less isolated than AWS production behavior.
- Host files must be readable by the runtime user in the container.

### Troubleshooting

- Error mentions `mounts denied` or invalid bind mount:
  Docker Desktop likely is not allowed to mount that path. Add the directory
  to Docker Desktop File Sharing settings:
  https://docs.docker.com/desktop/settings-and-maintenance/settings/#file-sharing
- Runtime import/init errors in hot-reload mode:
  verify the source directory contains the expected handler file at the root
  of the mounted `/var/task`.
- Runtime init error mentioning missing layer version:
  verify same-account layer ARNs exist in the emulator, or for foreign-account
  AWS-managed/third-party layers verify the zip exists in `LAMBDA_LAYER_CACHE_DIR`
  using the `{LayerName}_{Version}.zip` filename convention.

---

## CDK Integration: Hot Reload with `NodejsFunction`

### How tag forwarding works

When you add `Tags` to an `AWS::Lambda::Function` resource in a CloudFormation
template (or a CDK construct), Overcast's CloudFormation provisioner forwards
those tags to the Lambda CreateFunction call. This means you can set the
`overcast:hot-reload-path` tag directly in your CDK stack and hot-reload will
activate automatically on `cdk deploy`.

CloudFormation represents tags as an array of `{Key, Value}` objects; the
provisioner converts them to the `map[string]string` format that Lambda expects.

### Quickest path: `cdk watch` (no tags needed)

If you just want fast iteration without configuring hot-reload tags, use
`cdk watch`. It calls `UpdateFunctionCode` on every file change, which
invalidates the warm pool entry — no tag, no bind mount, no Docker file-sharing
config required:

```bash
AWS_ENDPOINT_URL=http://localhost:2456 cdk watch
```

Each save triggers a redeploy of only the changed function assets. This works
with every runtime and every bundler.

> `cdk watch` is the zero-config option. Reach for hot-reload bind mounts only
> when you need sub-second iteration and want to skip the redeploy cycle entirely.

---

### Hot-reload bind mount: `nodejs24.x` (raw TypeScript — zero build step)

Node.js 24 strips TypeScript types natively, so you can mount your `.ts` source
directory directly with no bundler involved:

```typescript
import * as path from "path";
import * as cdk from "aws-cdk-lib";
import * as lambda from "aws-cdk-lib/aws-lambda";

const fn = new lambda.Function(this, "MyFn", {
  runtime: lambda.Runtime.NODEJS_24_X,
  handler: "src/handler.handler",
  code: lambda.Code.fromAsset(path.join(__dirname, "src")),
});

// Mount the raw TypeScript source — Node 24 runs .ts directly.
cdk.Tags.of(fn).add("overcast:hot-reload-path", path.resolve(__dirname, "src"));
```

Save a `.ts` file → next invoke picks up changes immediately. No build step,
no `dist/` directory to manage.

> Overcast will emit a warning at container acquire time if `.ts` files are found
> with no `.js` files on runtimes older than Node 24, guiding you to the correct
> setup for your runtime.

---

### Hot-reload bind mount: `nodejs22.x` and earlier (esbuild watch)

On older runtimes, the Lambda runtime cannot import `.ts` files. You need to
keep a compiled JS output directory up to date and mount that instead:

```typescript
// Set the tag to the esbuild output directory, not the source.
cdk.Tags.of(fn).add(
  "overcast:hot-reload-path",
  path.resolve(__dirname, "dist"),
);
```

Run esbuild in watch mode in a separate terminal so compiled JS is regenerated
on every save:

```bash
# Terminal 1 — keep dist/ fresh
npx esbuild src/handler.ts --bundle --platform=node --outdir=dist --watch

# Terminal 2 — deploy once (tags are set by CDK)
AWS_ENDPOINT_URL=http://localhost:2456 cdk deploy
```

Each esbuild rebuild is immediately picked up on the next invoke — no redeploy
needed. For `NodejsFunction` (which runs esbuild at synth time), use
`lambda.Function` + `Code.fromAsset` pointing at your own `dist/` instead.

---

### Plain `Function` (Python / pre-built JS)

For non-TypeScript handlers the source directory is the task directory — point
the tag directly at your source tree:

```typescript
import * as lambda from "aws-cdk-lib/aws-lambda";

const fn = new lambda.Function(this, "MyFn", {
  runtime: lambda.Runtime.PYTHON_3_12,
  handler: "index.handler",
  code: lambda.Code.fromAsset(path.join(__dirname, "src")),
});

cdk.Tags.of(fn).add("overcast:hot-reload-path", path.resolve(__dirname, "src"));
```

Edit any `.py` file → next invoke uses the updated source immediately.

---

### Enabling hot-reload mode

Hot-reload is opt-in. Start Overcast with the flag enabled:

```bash
OVERCAST_LAMBDA_HOT_RELOAD=true overcast serve
# or
docker run -e OVERCAST_LAMBDA_HOT_RELOAD=true overcast
```

Then deploy once with `cdk deploy`. Subsequent code changes are reflected
immediately on the next Lambda invoke without redeploying.

### Limitations and caveats

- `cdk watch` and `cdk deploy --hotswap` work without the hot-reload flag — they
  call `UpdateFunctionCode` directly. Use bind-mount hot-reload only when you
  want to skip the redeploy cycle entirely.
- Lambda layers are supported with hot-reload mode. Attached layer archives are
  expanded into `/opt` inside the Lambda container before startup (for example,
  Node uses `nodejs/node_modules/*` and Python uses `python/*`).
- The same `/opt` layer injection behavior is used for normal zip-based
  invocation too (not just hot-reload mode).
- If multiple attached layers contain the same file path, later layers in the
  function's `Layers` list override earlier ones.
- The CloudFormation provisioner converts CFN tag arrays to the Lambda tag map
  format and applies stack-level tags to Lambda resources. Resource-level tags
  take precedence on key conflicts.
- Overcast logs a `WARN` at container acquire time if `.ts` files are found with
  no `.js` files on Node 22 or earlier, pointing to the correct fix.

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

### Extensions stripping

Lambda extensions (files under `/opt/extensions/`) cannot function outside the
real Lambda platform. Overcast automatically strips the `extensions/` directory
from all layers before injecting them into `/opt`, regardless of whether the
layer was pre-downloaded or remotely fetched.

### Environment variables reference

| Variable                              | Default          | Description                                             |
| ------------------------------------- | ---------------- | ------------------------------------------------------- |
| `LAMBDA_LAYER_CACHE_DIR`              | `/data/layers`\* | Directory to look up / store cached layer zips          |
| `LAMBDA_FETCH_REMOTE_LAYERS`          | `false`          | Enable automatic fetching from real AWS                 |
| `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS` | `4`              | Max concurrent Docker-backed Lambda container starts    |
| `LAMBDA_INIT_TIMEOUT_SECONDS`         | `10`             | Max seconds to wait for runtime INIT before invocation  |
| `LAMBDA_REMOTE_AWS_ACCESS_KEY_ID`     | —                | AWS access key for remote layer downloads               |
| `LAMBDA_REMOTE_AWS_SECRET_ACCESS_KEY` | —                | AWS secret key for remote layer downloads               |
| `LAMBDA_REMOTE_AWS_SESSION_TOKEN`     | —                | AWS session token for remote layer downloads (optional) |

\* Resolves to `{OVERCAST_DATA_DIR}/layers`. In the standard Docker image
`OVERCAST_DATA_DIR=/data`, so layers are read from `/data/layers`.

---

## Web UI Guidance: Should Hot Reload Be Exposed?

Yes. This feature should be exposed in the web UI, but as an explicit
development workflow, not a default Lambda path.

### Recommended UI model

1. Add a "Development" section on the Lambda function create/edit surface.
2. Add a toggle: `Enable hot reload (mount local source)`.
3. Show an absolute-path input only when toggle is enabled.
4. Validate path client-side (absolute-only) and show inline error text.
5. Persist by writing the function tag:
   `overcast:hot-reload-path=<path>`.

### UX principles (clean and idiomatic)

- Keep hot reload out of the primary deployment flow by default.
- Use progressive disclosure:
  basic users see standard code deploy fields first; advanced users can expand
  Development settings.
- If function has layers attached, keep the toggle enabled and note that layers
  are mounted under `/opt`.
- Include a short "How it works" helper text:
  "Mounts this local path to /var/task for fast local iteration."
- On mount-related failures, surface friendly guidance with a direct link to
  Docker Desktop File Sharing docs.

### Suggested interaction details

- Toggle on with empty path: block save; show `Enter an absolute path`.
- Relative path input: block save; show `Path must be absolute`.
- Windows input accepted; display normalized path preview for transparency.
- Show a small dev-mode badge on function details when hot reload is active.

### API/implementation notes

- Keep implementation tag-driven so behavior is consistent across CLI, SDK,
  and UI.
- Do not auto-enable globally from UI; respect
  `OVERCAST_LAMBDA_HOT_RELOAD` server-side feature flag.
- Prefer explicit validation errors over silent fallback to zip copy.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category                    | ✅ Supported | ❌ Unsupported |
| --------------------------- | ------------ | -------------- |
| Function management         | 8            |                |
| Invocation                  | 2            | 1              |
| Aliases & versions          | 7            |                |
| Event source mappings       | 5            |                |
| Layers                      | 5            |                |
| Concurrency & configuration | 5            |                |

---

## Endpoints

### Function management

| Operation                      | Status       | Notes                                                                                                                             | AWS Docs                                                                                   |
| ------------------------------ | ------------ | --------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| `ListFunctions`                | ✅ Supported | Returns all stored functions; empty list if none                                                                                  | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_ListFunctions.html)                |
| `CreateFunction`               | ✅ Supported | Stores metadata; validates runtime; deprecated runtimes rejected; auto-creates CWL log group; VpcConfig and ImageConfig supported | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_CreateFunction.html)               |
| `DeleteFunction`               | ✅ Supported |                                                                                                                                   | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunction.html)               |
| `GetFunction`                  | ✅ Supported | Returns FunctionConfiguration + Code location block                                                                               | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunction.html)                  |
| `GetFunctionConfiguration`     | ✅ Supported | Returns FunctionConfiguration only (no Code block)                                                                                | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionConfiguration.html)     |
| `UpdateFunctionCode`           | ✅ Supported | Updates code zip; generates new RevisionId                                                                                        | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateFunctionCode.html)           |
| `UpdateFunctionConfiguration`  | ✅ Supported | Patches Timeout/MemorySize/Description/Handler/Role/Environment/Layers/VpcConfig/ImageConfig; generates new RevisionId            | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_UpdateFunctionConfiguration.html)  |
| `GetFunctionCodeSigningConfig` | ✅ Supported | Always returns ResourceNotFoundException; code signing is not enforced by the emulator                                            | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionCodeSigningConfig.html) |

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

| Operation                         | Status       | Notes                                                                                  | AWS Docs                                                                                      |
| --------------------------------- | ------------ | -------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `PutFunctionConcurrency`          | ✅ Supported | Stores reserved concurrency limit; 0 = throttled                                       | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PutFunctionConcurrency.html)          |
| `GetFunctionConcurrency`          | ✅ Supported | Returns 404 if no concurrency limit is set                                             | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetFunctionConcurrency.html)          |
| `DeleteFunctionConcurrency`       | ✅ Supported | Clears reserved concurrency limit; returns 204                                         | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_DeleteFunctionConcurrency.html)       |
| `PutProvisionedConcurrencyConfig` | ✅ Supported | Stores config per qualifier; immediately reports Status=READY (no actual provisioning) | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_PutProvisionedConcurrencyConfig.html) |
| `GetProvisionedConcurrencyConfig` | ✅ Supported | Returns ProvisionedConcurrencyConfigNotFoundException if not set                       | [docs](https://docs.aws.amazon.com/lambda/latest/dg/API_GetProvisionedConcurrencyConfig.html) |

<!-- END overcast:capabilities -->
