---
title: Manual testing guide
description: How to smoke test Overcast by hand — what the coded tests and the AWS CLI cannot tell you, and the endpoint configurations that actually break.
---

# Manual testing guide

For pre-release smoke testing and for verifying a change by hand. Conventions for *coded* tests
live in [tests/AGENTS.md](../../tests/AGENTS.md); this is about the checks that only a real client,
a real container, and a real deployment shape can give you.

Read this before concluding "the endpoint works" — several classes of bug are invisible to the
tooling people reach for first.

## The invariant: `localhost` is never Overcast, from inside a container

Inside a Lambda (or ECS task) container, `localhost` is *that container*. Overcast is a sibling on
the Docker network, never the loopback. So **any URL a container receives that names `localhost` is
broken by construction** — it will connect to the function itself, or refuse:

```
connect ECONNREFUSED 127.0.0.1:4566
```

Everything Overcast does around endpoints follows from this one fact:

- `AWS_ENDPOINT_URL` in a function's environment is the container-routable address
  (`http://172.18.0.2:4566`), never `localhost`. Check it if a function cannot reach Overcast.
- Loopback origins in values a host-side deploy baked into function/task environment are rewritten
  to that address, so a queue URL from `cdk deploy` still works inside the function.
- Split-horizon hostnames are remapped through `/etc/hosts` (below) so one URL serves both sides.
- Values Overcast mints for a caller — queue URLs, stack outputs — are minted **per caller**: the
  host gets `localhost:<published port>`, a container gets the routable address, for the same
  resource.

The corollary for testing: a check that only ever runs on the host cannot tell you a container can
reach Overcast. Drive the API **from inside a function** for anything endpoint-related.

## The AWS CLI cannot reproduce a whole class of endpoint bug

**The most important thing on this page.** The AWS CLI is not a representative client.

`aws` (botocore) honours `AWS_ENDPOINT_URL` for every service. Several SDKs do not:

| SDK | How it resolves the SQS endpoint |
| --- | --- |
| botocore / AWS CLI | `AWS_ENDPOINT_URL` |
| **JS SDK v3** | the `QueueUrl`'s origin — `@aws-sdk/middleware-sdk-sqs` swaps it in whenever it differs from the resolved endpoint and the client was not constructed with an explicit `endpoint`. `AWS_ENDPOINT_URL` does *not* count, because it resolves through the endpoint ruleset and never lands on `config.endpoint` |
| **.NET, Java v1** | the queue URL *is* the request URI |

So a queue URL Overcast hands out on an origin the caller cannot dial fails in the JS/.NET/Java
SDKs and passes in the CLI. Two real bugs hid behind exactly this: per-caller queue URL minting
(#318) and CloudFormation stack output origins (#353).

Two further things also mask it, so avoid both when testing endpoints:

- **`--endpoint-url` / an explicit client `endpoint`** suppresses the queue-URL override entirely.
- **A 1:1 port mapping** makes the configured origin accidentally correct.

**Therefore:** any change touching endpoints, origins, URL minting or routing must be exercised with
a real SDK client — ideally from inside a Lambda container — on a *remapped* port, with no explicit
endpoint. Not with `aws` alone.

A minimal JS probe (the compat CDK suite has `@aws-sdk/*` installed, so run it from
`compat/suites/cdk`):

```js
import { SQSClient, SendMessageCommand } from "@aws-sdk/client-sqs"
// No endpoint config: this is the case that catches origin bugs.
const r = await new SQSClient({}).send(new SendMessageCommand({ QueueUrl: process.argv[2], MessageBody: "probe" }))
console.log(r.MessageId)
```

## Endpoint configurations worth covering

A Lambda using AWS SDKs must reach Overcast **out of the box**. The deployment shapes that behave
differently:

| Shape | Why it differs |
| --- | --- |
| 1:1 ports (`-p 4566:4566`) | the configured origin is accidentally correct — passes even when the logic is wrong |
| **Remapped** (`-p 4580:4566`) | the published port differs from the listen port; this is what `scripts/run-test-instance.sh` does, and where origin bugs surface |
| `OVERCAST_HOSTNAME=<split-horizon name>` | the configured name wins and must resolve from both sides |
| `OVERCAST_HOSTNAME=<compose service name>` | resolves for sibling containers, not for the host |
| Slim image | same API surface, no web UI or SQLite — worth one pass |

And the client shapes, all with a **bare** client (`new XClient({})`):

- SQS with a queue URL baked in at deploy time (the CDK/CloudFormation flow)
- SQS discovering its own URL via `GetQueueUrl`
- S3 (both default virtual-hosted addressing and `forcePathStyle`)
- DynamoDB, SNS, Lambda — plain `AWS_ENDPOINT_URL` consumers
- A user who sets `AWS_ENDPOINT_URL` in the function's own environment, usually by copying host
  config; Overcast rewrites loopback origins on its own port, so this should still work
- An SQS → Lambda event source mapping, which exercises Overcast → function *and* function → Overcast

## Split-horizon hostnames, and the one name that cannot work

Overcast rewrites `/etc/hosts` in every container it starts so these resolve back to it:

```
172.18.0.2	localhost.overcast.sh
172.18.0.2	localhost.localstack.cloud
172.18.0.2	localhost.floci.io
```

They resolve to `127.0.0.1` in public DNS and to Overcast inside a container, so a single URL works
from both sides — that is what makes them the right value for `OVERCAST_HOSTNAME`. `OVERCAST_HOSTNAME`
and `OVERCAST_SPLIT_HORIZON_HOSTS` are added to the same list.

Bare **`localhost` is deliberately not hijacked** and cannot be: a container needs its own loopback.
So `OVERCAST_HOSTNAME=localhost` tells Overcast to advertise itself under a name meaning "me" to
every container that hears it, and URLs it mints are undialable from a Lambda. That is configuration
error rather than a defect, but it fails silently — worth checking when a container cannot reach
Overcast.

To see what a function actually got, read `/etc/hosts` and the environment from inside it rather
than inferring:

```js
exports.handler = async () => ({
  awsEndpointUrl: process.env.AWS_ENDPOINT_URL,
  etcHosts: require("fs").readFileSync("/etc/hosts", "utf8").split("\n"),
})
```

## Ports

Never bind **4566** or **4567** — those belong to the user's own instance. Use
`scripts/run-test-instance.sh` (or `.ps1`), which picks a free pair at or above 4570 and refuses the
reserved pair. When pointing someone at a test instance, give the full URL including the port.

Remember that a remapped port is not just polite, it is *better coverage*: it is the configuration
that catches origin bugs.

## CDK

`cdk deploy` against Overcast needs virtual-hosted S3 addressing for its asset upload, so the
endpoint must be a wildcard-DNS name:

```sh
export AWS_ENDPOINT_URL=http://localhost.overcast.sh:4580   # not http://localhost:4580
```

With a bare `localhost` endpoint the deploy fails at asset publish with
`getaddrinfo ENOTFOUND cdk-hnb659fds-assets-….localhost`.

Worth exercising, because CDK's defaults differ from a hand-written template in ways that have
found real bugs:

- CDK almost never sets `FunctionName`/`QueueName`, leaving CloudFormation to generate physical
  names — a stack with **two** unnamed resources of a type is the case that caught #335
- Stateful resources default to `RemovalPolicy.RETAIN`, so `cdk destroy` leaving DynamoDB tables and
  S3 buckets behind is correct, not a leak
- Reading a stack output and using it from an SDK is the flow that caught #353

## Before concluding a manual test passed

- Did a **real SDK client** exercise it, not just `aws`?
- Was the endpoint **remapped**, so a config-derived origin could not accidentally be right?
- Was the client **bare** — no `--endpoint-url`, no explicit `endpoint`?
- If it involves containers, was it verified **from inside** one?
- Both **console and slim** images, where the change could affect either?
- Containers and stacks **cleaned up** afterwards (`docker ps -a` empty of `overcast-*`)?
