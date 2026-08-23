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

## Your own AWS environment is a variable too

Before trusting any hand-run `aws` result, account for what your shell already sets. A developer
machine carries `AWS_PROFILE`, `AWS_REGION`, SSO state, sometimes a stale `AWS_ENDPOINT_URL` — and
they leak into every call silently. A wrong region is the nastiest of them, because resources really
are per-region here as on AWS: a create in one region and a list in another correctly disagree, and
it reads exactly like data loss.

Use the wrapper rather than remembering:

```sh
OVERCAST_PORT=4580 scripts/awslocal.sh sqs list-queues
```

It unsets **every** `AWS_*` variable in its own process and sets back only the endpoint, placeholder
credentials, the region (`OVERCAST_REGION`, default `us-east-1`) and an empty `AWS_PAGER`. Exporting
`AWS_ACCESS_KEY_ID` by hand is not equivalent — it leaves everything you did not think of in place.

**But not for the checks on this page.** The wrapper passes `--endpoint-url`, which is precisely
what suppresses the queue-URL origin override described above. Endpoint, origin and routing checks
need a bare client with no endpoint configured. Use the wrapper for setup and for everything else;
drop to a bare SDK client for the thing being tested.

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

### Subdomains need the resolver, not `/etc/hosts`

`/etc/hosts` is an exact-match table with no wildcard syntax, so those entries cover **only the apex
names**. Subdomains — which is what SDKs actually build — are served by Overcast's own resolver
(`internal/dns`), which containers are pointed at via Docker's `--dns`:

| From inside a Lambda | Answered by |
| --- | --- |
| `localhost.overcast.sh` | `/etc/hosts` **and** the resolver |
| `mybucket.s3.localhost.overcast.sh` | the resolver only |
| `abc123.execute-api.us-east-1.localhost.overcast.sh` | the resolver only |
| a container name, `example.com` | Docker's embedded DNS / upstream, unchanged |

Why this matters when testing: with `OVERCAST_DNS=false`, or when port 53 could not be bound (it
needs privilege outside a container), the apex still resolves and every subdomain silently falls
back to public DNS — which answers `127.0.0.1`, the container itself. The lookup **succeeds**, so it
presents as a connection failure rather than a resolution one. Check the startup log for
`dns resolver listening` before concluding that a virtual-hosted URL is broken for some other
reason, and check `/etc/resolv.conf` inside the container is still `127.0.0.11` (Docker keeps its
own resolver in front and uses Overcast's as an upstream).

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

## Reading a function container's own logs

A Lambda container runs under Overcast's init (`internal/lambdainit`), which owns the runtime's
stdout and stderr. It **tees** every line it reads to its own — which are the container's — so
`docker logs <container>` still shows the function's output byte for byte, exactly as it did before
the init existed. That copy is the debugging backup, never a second transport: CloudWatch, the
`X-Amz-Log-Result` tail and the Telemetry API are fed from the init's own frame stream and are never
reconciled against the daemon's copy.

Interleaved with the function's output you will see lines prefixed `[overcast-init]` on stderr.
Those are Overcast's own diagnostics — the runtime's pid and argv, each extension it started, where
each invocation began and ended and how many lines it drained, log-channel reconnects and gaps, the
child's exit code. They go to the container's stderr and nowhere else: they never reach CloudWatch,
the tail or the Telemetry API, which stay AWS-shaped. They are the first place to look when a
container dies before its runtime ever polls for work.

## Ports

Never bind **4566** or **4567** — those belong to the user's own instance. Use
`scripts/run-test-instance.sh` (or `.ps1`), which picks a free pair at or above 4570, refuses 4566
and 4567 in either role, and publishes both to `127.0.0.1` so the emulator is not reachable from the
network. When pointing someone at a test instance, give the full URL including the port.

The script takes named options and has no `docker run` passthrough — `--image`, `--base-port`,
`--name`, `--env`, `--data-volume`, `--mount-docker-socket`, `--no-logs`. Add
`--mount-docker-socket` when you are testing Lambda, ECS, RDS, ElastiCache or MSK, or the web
console's endpoint discovery; leave it off otherwise.

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

- Was your **own AWS environment** neutralised (`scripts/awslocal.sh`), so an ambient `AWS_REGION`
  or `AWS_PROFILE` is not what you are actually observing?
- Did a **real SDK client** exercise it, not just `aws`?
- Was the endpoint **remapped**, so a config-derived origin could not accidentally be right?
- Was the client **bare** — no `--endpoint-url`, no explicit `endpoint`?
- If it involves containers, was it verified **from inside** one?
- If it uses a **subdomain** form (virtual-hosted S3, `execute-api`), was it resolved from inside a
  container rather than only from the host? Those two disagree.
- Both **console and slim** images, where the change could affect either?
- Containers and stacks **cleaned up** afterwards (`docker ps -a` empty of `overcast-*`)?
