---
title: CloudFormation stack output URLs use the configured origin, not the caller's
description: A stack output naming an Overcast path-style URL is minted from OVERCAST_PORT, so it is unreachable when the published port differs. Found during alpha.26 release smoke testing.
---

# CloudFormation stack output URLs use the configured origin, not the caller's

**Status:** open · **Found:** 2026-07-28, alpha.26 pre-release smoke testing
**Severity:** breaks a common CDK/CloudFormation workflow on any remapped port; correct on the default 1:1 mapping
**Related:** #318 (per-caller queue URLs), #331 (published-port rewriting), #351 (host-routed output origins)

## Symptom

Overcast running with its API port remapped — `docker run -p 4600:4566`, which is what
`scripts/run-test-instance.sh` does and what anyone avoiding a port clash does — returns a
CloudFormation stack output that a JS SDK client cannot use:

```
CFN stack output URL : http://localhost:4566/123456789012/cfn-out-q
SQS-minted URL       : http://localhost:4600/123456789012/cfn-out-q
```

Same queue, same server, two different origins. The SDK says exactly what goes wrong:

```
QueueUrl=http://localhost:4566/… differs from SQSClient resolved endpoint=http://localhost:4600/,
using QueueUrl host as endpoint.
Set [endpoint=string] or [useQueueUrlAsEndpoint=false] on the SQSClient.
```

Host-side JS SDK, `AWS_ENDPOINT_URL` set, no explicit endpoint — the shape a CDK user's
application and tests have:

| Queue URL source | Result |
| --- | --- |
| CloudFormation stack output | **FAIL** — dials `localhost:4566`, nothing there |
| `sqs get-queue-url` | PASS |

## Why it is easy to miss

Three separate things mask it:

1. **The default 1:1 mapping is correct.** `cfg.ExternalBaseURL()` is `localhost:4566` and that
   *is* where the host reaches Overcast, so nothing is wrong until the ports differ.
2. **The AWS CLI is immune.** botocore honours `AWS_ENDPOINT_URL`, so `aws sqs send-message`
   works with the bad URL. Only SDKs that derive the endpoint from the `QueueUrl` — the JS SDK's
   `queueUrlMiddleware`, and .NET/Java v1 which use the queue URL as the request URI — fail. Testing
   with the CLI alone will not reproduce it.
3. **Passing `--endpoint-url` also masks it**, because an explicit client endpoint suppresses the
   queue-URL override. The failure only appears with a default-configured client.

## Mechanism

Two different paths mint client-facing URLs, and only one is request-aware:

| Path | Source | Request-aware? |
| --- | --- | --- |
| SQS's own API (`CreateQueue`, `GetQueueUrl`, `ListQueues`) | `serviceutil.ClientBaseURL` | yes — uses the origin the caller dialled (#318) |
| CloudFormation resource handlers | `cfg.ExternalBaseURL()` | **no** — built from `OVERCAST_PORT` |

`internal/services/cloudformation/provisioner.go` builds queue URLs from `cfg.ExternalBaseURL()`
(three sites around lines 1970/1977/2028). That value is stored as the resource's `Ref` and returned
verbatim as the stack output.

#351 added `Handler.reachableURL` to re-host stack outputs on the caller's origin, but it only
claims values whose host parses as a **host route** (`middleware.ParseHostRoute`) — e.g.
`{apiId}.execute-api.{region}.localhost`. A path-style queue URL's host is plain `localhost:4566`,
which is not a host-route label, so it is returned unchanged. The mechanism to fix this is already
in place; it just does not cover Overcast's own path-style origins.

## What real AWS does

Not directly comparable — on AWS the endpoint is genuinely fixed, so this class of divergence
cannot arise. The relevant contract is the SDK's, and it is explicit: the JS SDK's
`@aws-sdk/middleware-sdk-sqs` replaces the resolved endpoint with the `QueueUrl`'s origin whenever
the two differ and the client was not constructed with an explicit `endpoint`. A queue URL Overcast
hands out must therefore be dialable by the client that receives it — which is the principle #318
already established for SQS's own API.

## Suggested fix

Extend `reachableURL` in `internal/services/cloudformation/handler.go` to also re-origin values whose
origin is Overcast's own. It already has the caller's origin via `clientBaseURL(ctx)`, and it already
runs over every output value (`handler.go` ~line 1329), so this is a contained change at the point
the problem is already being solved for host-routed URLs:

- If the value parses as a URL and its origin equals `cfg.ExternalBaseURL()`'s origin, re-mint it on
  `clientBaseURL(ctx)` keeping path and query.
- Leave everything else untouched. Restricting to "origin we know is ours" keeps ARNs, ECR URIs, S3
  URLs and third-party endpoints alone, which is the same restraint the host-route branch shows.

When the caller has no client endpoint on the context (internal provisioning calls),
`clientBaseURL` already falls back to `ExternalBaseURL`, so the rewrite is a no-op there.

A deeper alternative — making the provisioner mint request-aware URLs at create time — is worse:
provisioning happens through internal requests that have no caller origin, and the stored value
would still be wrong for a *different* caller later. Rewriting at read time is correct for every
caller.

## Reproducing

```sh
docker run -d --name oc -p 4600:4566 overcast:dev
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
aws --endpoint-url http://localhost:4600 cloudformation create-stack --stack-name out-stack \
  --template-body '{"Resources":{"Q":{"Type":"AWS::SQS::Queue","Properties":{"QueueName":"cfn-out-q"}}},
                    "Outputs":{"QueueUrl":{"Value":{"Ref":"Q"}}}}'
# Compare the two origins for the same queue:
aws --endpoint-url http://localhost:4600 cloudformation describe-stacks --stack-name out-stack \
  --query "Stacks[0].Outputs[0].OutputValue"      # http://localhost:4566/...  <- wrong
aws --endpoint-url http://localhost:4600 sqs get-queue-url --queue-name cfn-out-q \
  --query QueueUrl                                # http://localhost:4600/...  <- right
```

Then drive the first URL from a **JS SDK** client with `AWS_ENDPOINT_URL` set and no explicit
`endpoint`; it fails. The AWS CLI will not reproduce it.

## Scope check

Other CloudFormation-minted URLs were reviewed:

- **API Gateway / AppSync / Lambda function URLs** — host-routed, already re-hosted by #351.
- **S3, ECR, ARNs** — not origin-bearing in the affected sense, and deliberately excluded.

So SQS queue URLs are the observed case; the fix is written generically against "origin is ours" so
any future path-style URL is covered by construction.
