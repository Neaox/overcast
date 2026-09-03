---
title: "Lambda examples"
description: "Hot-reloading local source into a running function, deploying a container image through the emulated ECR, supplying layers from a cache or from real AWS, and running extensions that call back into Overcast."
section: "Service Reference"
tags:
  - docs
  - examples
  - lambda
  - services
---

# Lambda examples

Back to [Lambda](../lambda.md).

## Hot reload

Hot reload mounts a local source directory into the runtime at `/var/task`, so
edits are picked up on the next invoke without uploading a new zip. It is opt-in
and meant for interpreted runtimes — Node.js and Python. Use the normal zip or
image deploy path when you need production-like packaging.

### Quickest CDK path: `cdk watch`

If you want fast iteration without configuring anything, `cdk watch` calls
`UpdateFunctionCode` on every file change, which invalidates the warm pool entry.
No tag, no bind mount, no Docker file-sharing configuration:

```bash
AWS_ENDPOINT_URL=http://localhost:4566 cdk watch
```

Each save redeploys only the changed function assets, and it works with every
runtime and bundler.

### Bind-mount hot reload

Use this when you want sub-second iteration and no redeploy cycle at all. Enable
the flag on the server:

```bash
OVERCAST_LAMBDA_HOT_RELOAD=true overcast serve
```

`OVERCAST_HOT_RELOAD=true` turns it on for every compute service at once — Lambda
and [ECS](../ecs/examples.md) — and `OVERCAST_LAMBDA_HOT_RELOAD` overrides it
either way, so one service can be opted out of an umbrella `true`.

Then tag the function with an absolute host path:

```bash
aws lambda create-function --function-name demo-hot \
  --runtime nodejs20.x --handler index.handler \
  --role arn:aws:iam::000000000000:role/lambda-role \
  --zip-file fileb://minimal.zip \
  --tags overcast:hot-reload-path=/absolute/path/to/lambda/source
```

Invoke, edit files in that path, invoke again.

| Rule | Detail |
| --- | --- |
| Absolute paths only | A relative path is refused |
| Windows paths normalised | `C:\Users\you\app` becomes `/c/Users/you/app` |
| Read-only | Mounted at `/var/task:ro` |
| Permissions | Host files must be readable by the runtime user in the container |

In CDK, set the tag on the construct and it activates after `cdk deploy`. Node.js
24 strips TypeScript types natively, so raw `.ts` can be mounted:

```typescript
const fn = new lambda.Function(this, "MyFn", {
  runtime: lambda.Runtime.NODEJS_24_X,
  handler: "src/handler.handler",
  code: lambda.Code.fromAsset(path.join(__dirname, "src")),
});

cdk.Tags.of(fn).add("overcast:hot-reload-path", path.resolve(__dirname, "src"));
```

For Node.js 22 and earlier, point the tag at compiled output instead
(`path.resolve(__dirname, "dist")`) and keep it fresh with your bundler. Overcast
logs a `WARN` at container acquire time if it finds `.ts` files and no `.js` files
on Node.js 22 or earlier.

### What counts as a change

Overcast decides whether a warm environment is still current by fingerprinting the
mounted tree — every entry's path, and every file's size and modification time —
before each invocation. When the fingerprint moves, the warm container is retired
and the next invocation starts a fresh one against the edited source, exactly as
`UpdateFunctionCode` does. That is what makes editing an already-loaded file work
on runtimes that cache modules.

The fingerprint is bounded, so that recomputing it does not become the cost of
invoking:

| Bound | Effect |
| --- | --- |
| Dependency and VCS directories are not looked inside | `node_modules`, `.git`, `__pycache__`, `.venv`, `.mypy_cache`, `.pytest_cache` are fingerprinted by name only |
| 20,000 entries, 24 directory levels | A larger or deeper tree is covered up to the limit; edits past it are not noticed |
| A read costing more than 25 ms is rate-limited | Re-read at most once per 20× the previous read's cost, and at least once every 2 seconds |
| Symbolic links are not followed | A symlink is fingerprinted by name |

Below the 25 ms budget the tree is read before every invocation and an edit is
always live on the next one. A local disk takes 4–11 µs per entry, so a few
thousand files stays under it. The same tree across a Docker Desktop file share —
which is the case when Overcast itself runs in a container — costs about 2 ms per
entry, and there an edit can take up to 2 seconds rather than one invocation to be
seen.

Two more things to know. **Overcast must be able to read the path itself:** the
bind mount is created by the Docker daemon, so it works whether or not Overcast
can see the directory, but the fingerprint is read by Overcast from its own
filesystem — so when Overcast runs in a container, mount the source into it at the
same path as well, or the first invocation runs the mounted source and every later
one keeps that same container. And **filesystem timestamp granularity is 1–2
seconds** on some filesystems: two saves within one tick that leave the file the
same size are one change, and the second is not seen until something else changes.

If none of that suits your project — a very large tree, a slow file share, or
dependencies you edit directly — use `cdk watch` instead.

## Container images

A `PackageType=Image` function runs from an image you pushed to Overcast's
[ECR](../ecr.md). Build on an AWS Lambda base image exactly as you would for real
AWS — the base image's Runtime Interface Client is what Overcast drives.

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

cat > Dockerfile <<'EOF'
FROM public.ecr.aws/lambda/nodejs:20
COPY app.js ${LAMBDA_TASK_ROOT}/
CMD ["app.handler"]
EOF

URI=$(aws ecr create-repository --repository-name my-fn \
  --query 'repository.repositoryUri' --output text)   # localhost:4510/000000000000/my-fn

docker build -t my-fn .
aws ecr get-login-password | docker login --username AWS --password-stdin "${URI%%/*}"
docker tag my-fn "$URI:v1"
docker push "$URI:v1"

aws lambda create-function --function-name my-fn --package-type Image \
  --role arn:aws:iam::000000000000:role/lambda-role \
  --code ImageUri=000000000000.dkr.ecr.us-east-1.amazonaws.com/my-fn:v1

aws lambda invoke --function-name my-fn --payload '{}' \
  --cli-binary-format raw-in-base64-out out.json && cat out.json
```

**Push to `repositoryUri`, deploy the `amazonaws.com` URI.** Both addresses name
the same repository: the first is where the Docker daemon can reach the registry,
the second is what AWS and CDK write, and Overcast resolves it back to the
registry serving it. Either form works in `ImageUri` — the `amazonaws.com` one is
what a template that also deploys to AWS will contain.

`ImageConfig` overrides the image's own `ENTRYPOINT`, `CMD` and `WORKDIR`, and
`update-function-code --image-uri` moves the function onto a new tag or digest:

```bash
aws lambda update-function-configuration --function-name my-fn \
  --image-config 'Command=["other.handler"]'

aws lambda update-function-code --function-name my-fn \
  --image-uri 000000000000.dkr.ecr.us-east-1.amazonaws.com/my-fn:v2

aws lambda wait function-updated --function-name my-fn
```

The new image is pulled in the background, so `update-function-code` answers
`LastUpdateStatus: InProgress` and the wait is the one that tells you the pull
landed — or that it failed, with `ImageAccessDenied` or `InvalidImage` in
`LastUpdateStatusReasonCode`. Every other update settles before the call
returns; see [Limitations](limitations.md#other-divergences).

CDK's `DockerImageFunction` with `DockerImageCode.fromImageAsset` needs none of
this by hand: `cdk deploy` builds the image, pushes it to the repository
`cdk bootstrap` created, and writes the `amazonaws.com` URI itself. See
[CDK § Container assets](../../cdk.md#container-assets-are-served-from-overcasts-own-registry).

## Layers

Layer ARNs from CDK or CloudFormation are injected into `/opt` before the runtime
starts, matching real Lambda. Layers published locally with
`PublishLayerVersion` resolve automatically; when several layers provide the same
path, later entries in the function's `Layers` list win.

A layer ARN that is neither local nor cache-backed fails before the cold start,
with a Lambda init-style error rather than a container that starts and cannot
import:

```
{"errorMessage":"Failed to load Lambda layer arn:aws:lambda:...: layer version not found","errorType":"Runtime.InitError"}
```

### Option 1 — pre-download the layer (no AWS credentials)

Download the zip once and drop it in the layer cache directory, which defaults to
`{OVERCAST_DATA_DIR}/layers` — `/data/layers` in the standard image.

```bash
LAYER_URL=$(aws lambda get-layer-version-by-arn \
  --arn "arn:aws:lambda:ap-southeast-2:094274105915:layer:AWSLambdaPowertoolsTypeScriptV2:22" \
  --query 'Content.Location' --output text)

curl -o AWSLambdaPowertoolsTypeScriptV2_22.zip "$LAYER_URL"
mkdir -p .overcast/layers && mv AWSLambdaPowertoolsTypeScriptV2_22.zip .overcast/layers/
```

The filename is `{LayerName}_{Version}.zip`, derived from the ARN — so
`arn:…:layer:AWS-Parameters-and-Secrets-Lambda-Extension:11` becomes
`AWS-Parameters-and-Secrets-Lambda-Extension_11.zip`. Mount the directory into
the container:

```yaml
services:
  overcast:
    image: overcast:dev
    volumes:
      - "./.overcast:/data" # or ./.overcast/layers:/data/layers:ro
      - "/var/run/docker.sock:/var/run/docker.sock"
```

The same lookup satisfies the invoke-time existence check for a foreign-account
ARN, so you never need to publish a local replacement layer. Set
`LAMBDA_LAYER_CACHE_DIR` to use a different path — mainly useful when running the
native binary outside Docker.

### Option 2 — fetch from real AWS (needs credentials)

Overcast can download a missing layer at invocation time and cache it. This needs
both the flag and credentials with `lambda:GetLayerVersion`:

```yaml
services:
  overcast:
    image: overcast:dev
    environment:
      - LAMBDA_FETCH_REMOTE_LAYERS=true
      - LAMBDA_REMOTE_AWS_ACCESS_KEY_ID=AKIA...
      - LAMBDA_REMOTE_AWS_SECRET_ACCESS_KEY=...
      - LAMBDA_REMOTE_AWS_SESSION_TOKEN=... # if using SSO or an assumed role
```

These are **separate** from the `AWS_ACCESS_KEY_ID=test` credentials Overcast's
own APIs take, and are never handed to Lambda containers. A remotely fetched layer
is cached under a content-addressed name rather than the friendly one, so do not
expect `{LayerName}_{Version}.zip` to appear.

## Extensions

Docker-backed functions — zip and container image alike — start executables found
directly under `/opt/extensions` before the runtime entrypoint, as children of the
same in-container init that owns the runtime. Layer file modes are preserved, so
extension binaries must be executable in the layer zip.

| Path | Purpose |
| --- | --- |
| `POST /2020-01-01/extension/register` | Registration |
| `GET /2020-01-01/extension/event/next` | Event loop |
| `POST /2020-01-01/extension/init/error` | Init failure |
| `POST /2020-01-01/extension/exit/error` | Exit failure |
| `PUT /2020-08-15/logs` | Logs API subscription |
| `PUT /2022-07-01/telemetry` | Telemetry API subscription |

`INVOKE` events reach only extensions in the container that accepted the
invocation; `SHUTDOWN` is sent when Overcast tears a warm container down. An
extension subscribed through one API is refused by the other, as on AWS. The
Telemetry API validates `schemaVersion` — `2022-07-01`, `2022-12-13` or
`2025-01-29` — and refuses an unknown one rather than half-honouring it.

Both surfaces deliver HTTP destinations for `platform`, `function` and `extension`
records. Function stdout and stderr arrive as `function` records; the synthesised
invocation records arrive as `platform` ones. From `schemaVersion: 2022-12-13` a
`LogFormat: JSON` function's line is embedded as the JSON object it already is;
older schemas, the Logs API, Text-format functions and non-object lines receive
the string. Subscribers also get `platform.extension` at each registration and
`platform.telemetrySubscription` or `platform.logsSubscription` on subscribing —
neither is written to CloudWatch.

Subscribers receive **every** record regardless of `ApplicationLogLevel` and
`SystemLogLevel`, which filter CloudWatch Logs and the invoke tail only. Delivery
is at-least-once and batched, cut at the subscription's `maxItems`, `maxBytes` or
`timeoutMs`; a transport failure is retried with backoff, and a destination that
fails every attempt loses that batch — and is told, with a `platform.logsDropped`
event opening the next batch carrying `reason`, `droppedRecords` and
`droppedBytes`.

### Extensions that call AWS

Overcast injects endpoint and region variables into the container so an
endpoint-aware extension reaches the emulator rather than real AWS:
`AWS_ENDPOINT_URL`, `AWS_ENDPOINT_URL_SSM`, `AWS_ENDPOINT_URL_SECRETS_MANAGER`,
`AWS_REGION`, and the `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` /
`AWS_SESSION_TOKEN` triple.

Reference extensions by layer version in your IaC, because that is what CDK,
CloudFormation and Lambda ARNs expose. The AWS Parameters and Secrets Lambda
Extension for `ap-southeast-2` was verified at layer version `90`:

```text
arn:aws:lambda:ap-southeast-2:665172237481:layer:AWS-Parameters-and-Secrets-Lambda-Extension:90
arn:aws:lambda:ap-southeast-2:665172237481:layer:AWS-Parameters-and-Secrets-Lambda-Extension-Arm64:90
```

Choose the layer architecture that matches the **function's** architecture, not
the host's — an `x86_64` function needs the x86_64 layer even on Apple Silicon.

Overcast adds no secret cache in front of an extension. The extension binary keeps
its own environment-local cache, as it does on AWS, so a warm environment can
return a prior value until `SECRETS_MANAGER_TTL` expires. Set the TTL to `0` when
every request must read the current `AWSCURRENT` value.

## Reaching real AWS from a local function

A hybrid stack — most of it emulated, one client talking to a real regional
endpoint, a peered private endpoint, or a third-party API — works out of the
box. `OVERCAST_VPC_EGRESS` defaults to `open`, so every container Overcast
starts has a route out, `VpcConfig` or not.

The only work is telling the SDK which client goes where. Overcast injects
these into every container it starts:

| Variable | Effect |
| --- | --- |
| `AWS_ENDPOINT_URL` | Every SDK client defaults to Overcast |
| `AWS_ENDPOINT_URL_<SERVICE>` | Per-service override, same precedence as on AWS |
| `AWS_REGION` | The emulator's region |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN` | Dummy credentials the emulator accepts |

So the default is "everything is local", and you opt one client out of it —
with a real endpoint and real credentials:

```javascript
import { S3Client } from "@aws-sdk/client-s3";
import { SecretsManagerClient } from "@aws-sdk/client-secrets-manager";

// Emulated: picks up AWS_ENDPOINT_URL, no configuration needed.
const localS3 = new S3Client({});

// Real AWS: an explicit endpoint beats the injected variable, and real
// credentials beat the injected dummies.
const realSecrets = new SecretsManagerClient({
  region: "ap-southeast-2",
  endpoint: "https://secretsmanager.ap-southeast-2.amazonaws.com",
  credentials: {
    accessKeyId: process.env.REAL_AWS_ACCESS_KEY_ID,
    secretAccessKey: process.env.REAL_AWS_SECRET_ACCESS_KEY,
  },
});
```

Pass the real credentials in as function environment variables under names of
your own — never the `AWS_*` ones, which Overcast owns and would overwrite.

| Rule | Detail |
| --- | --- |
| Explicit `endpoint` wins | Per-client configuration beats `AWS_ENDPOINT_URL` in every AWS SDK |
| Real calls need real credentials | The injected dummies are rejected by AWS with `InvalidClientTokenId` |
| Costs are real | This is your account. A loop in a local function bills like a loop in a deployed one |
| Not for CI | Set `OVERCAST_VPC_EGRESS=none` there, so the same code fails fast with `ENETUNREACH` instead of quietly reaching production. Run Overcast in a container on the runner — on Docker Desktop the control plane stays routable and `none` cannot withhold egress ([why](../../networking/egress.md)) |
| Or make it match your template | `OVERCAST_VPC_EGRESS=routed` gives the function egress only where its subnet's route table does, so a `VpcConfig` in a private subnet with no NAT gateway fails locally as it would deployed. Same container requirement — see [`routed`](../../networking/routed-egress.md) |

If a call to real AWS returns `ENETUNREACH`, egress is off: check
`overcast network status` and see
[A function in a VPC fails with `ENETUNREACH`](../../troubleshooting.md#a-function-in-a-vpc-fails-with-enetunreach).
