# Repro — Lambda invocation stranded when a function has more than one container

A CDK app that deploys a secret and a Node function which fetches it through the
AWS Parameters and Secrets Lambda Extension, then invokes the function on a cold
container. It reproduces an invocation being handed to a container that had been
displaced from the Runtime API's waiter slot: the function logged `START`,
produced nothing for its whole timeout, and then ran to completion in a *later*
container under the already-dead request ID.

The extension is incidental — it is what made the failure frequent, because a
layered function has a longer, more variable cold start and so a wider window
for two containers to be polling `GET /next` at once. The bug is in
`internal/services/lambda/runtime_api.go`; see
`runtime_api_waiter_test.go` for the unit-level regression tests.

## Prerequisites

The extension is an AWS-managed layer, so it cannot be synthesised. Fetch it
once against real AWS and cache the zip:

```bash
aws lambda get-layer-version-by-arn --arn arn:aws:lambda:ap-southeast-2:665172237481:layer:AWS-Parameters-and-Secrets-Lambda-Extension:90 --query Content.Location --output text
```

Download that URL to `AWS-Parameters-and-Secrets-Lambda-Extension_90.zip`.

## Running

Start a throwaway Overcast on a free port pair (never 4566/4567 — see
[AGENTS.md](../../../AGENTS.md#reserved-ports--4566-and-4567-belong-to-the-user)),
then:

```bash
npm install
OVERCAST_ENDPOINT=http://localhost:4580 LAYER_ZIP=/path/to/layer.zip ./run.sh
```

`run.sh` publishes the layer into the target Overcast, deploys the stack, and
invokes the function once.

CDK asset upload uses virtual-hosted S3 addressing, which needs a resolvable
wildcard domain — point `AWS_ENDPOINT_URL` at `localhost.overcast.sh` rather
than `localhost`, because `*.localhost` does not resolve on Windows.

## Sweeping

The failure was intermittent, so a single green run proves little. `sweep.sh`
runs a cold invocation per secret size, each with its own secret name so the
extension's five-minute cache cannot mask a fetch:

```bash
SIZES="2000 8000 16000" REPEATS=3 bash sweep.sh
```

Every line should read `ok`. A `HUNG` line is the bug.
