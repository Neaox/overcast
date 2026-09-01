# How Overcast works

> An orientation document. It explains the major mechanisms at a level you can
> hold in your head, and links to the detailed docs for each one. Read it once
> end to end; after that, use it as a map.
>
> **Who this is for:** developers who use AWS and want to know how Overcast
> works underneath. It assumes you have used AWS and can program; it assumes
> nothing about the codebase, and nothing about any particular AWS service —
> plenty of people deploy to AWS daily without having written an IAM policy or a
> CloudFormation template.
>
> It also does not assume you have looked at what an SDK call is on the wire.
> [§2](#2-what-an-aws-sdk-call-actually-is) covers that, and is the one section
> to skip if you already have.

---

## Contents

1. [What Overcast is, and what it deliberately isn't](#1-what-overcast-is-and-what-it-deliberately-isnt)
2. [What an AWS SDK call actually is](#2-what-an-aws-sdk-call-actually-is)
3. [The shape of the repository](#3-the-shape-of-the-repository)
4. [The request path](#4-the-request-path)
5. [One handler, many protocols](#5-one-handler-many-protocols)
6. [State and storage](#6-state-and-storage)
7. [Docker: making services real](#7-docker-making-services-real)
8. [Cross-service integration](#8-cross-service-integration)
9. [CloudFormation and CDK](#9-cloudformation-and-cdk)
10. [What Overcast adds on top of AWS](#10-what-overcast-adds-on-top-of-aws)
11. [How we know it behaves like AWS](#11-how-we-know-it-behaves-like-aws)
12. [Keeping 50 services consistent](#12-keeping-50-services-consistent)
13. [Performance](#13-performance)
14. [Where to go next](#14-where-to-go-next)

---

## 1. What Overcast is, and what it deliberately isn't

Overcast is a single Go binary that pretends to be AWS. You point an AWS SDK,
the AWS CLI, Terraform or the CDK at `http://localhost:4566` instead of the
real thing, and they work — unmodified, with no special client library.

That last clause is the whole product. Overcast is not an AWS-shaped API that
you write code against; it is an emulator faithful enough that code written for
real AWS runs against it by changing one endpoint URL.

### The non-goals matter more than the goals

Every one of these is a deliberate decision, and each explains something about
the design:

- **Not a staging environment.** There is no 100% API parity and there never
  will be. Do not make production go/no-go decisions on the basis of an
  Overcast test.
- **Not a security boundary.** Credentials are accepted but not validated by
  default. Never expose it on a public network — and note that `OVERCAST_LISTEN`
  (renamed from `OVERCAST_HOST`, which has been removed — see
  [#870](https://github.com/overcast-sh/overcast/issues/870)) defaults to `0.0.0.0`
  when Overcast is containerised (required for Docker's `-p` publishing to
  reach it) and to `127.0.0.1` when it is not (#761) — so a native run with
  nothing set no longer listens on every interface. An explicit
  `OVERCAST_LISTEN` always wins over either default, in both directions: set
  it to `0.0.0.0` to restore the old native reach (from a VM, a second
  machine, or a phone on the same network), or to `127.0.0.1` inside a
  container if you want the narrower behaviour there too.
- **Not a performance testing tool.** No latency emulation, no request-rate
  limits, no per-service quotas. Overcast is deliberately as fast as it can be;
  making it artificially slow to "feel like AWS" is explicitly rejected. There
  is one narrow exception, covered in [§13](#13-performance).
- **Not a production dependency, ever.** Local development and CI only.
- **Not a perfect replica.** The design target is the most-used ~20% of each
  service's surface, emulated with high fidelity. Edge cases may differ.

The full list, with reasoning, is in
[AGENTS.md § Non-goals](../../AGENTS.md#non-goals--decision-guide-for-agents).

### How honesty is encoded

An emulator that quietly does the wrong thing is worse than one that refuses.
Overcast has three mechanisms for admitting what it does not do.

**Service tiers.** Every service carries an overall tier, visible on
`/_overcast/health` and in the web console:

| Tier | Meaning |
|---|---|
| **Full** | The high-traffic surface is behaviourally complete, side effects included. Exotic operations may still return `501`. |
| **Partial** | Full resource lifecycle plus real side effects for the most-used subset. |
| **Inert** | Full CRUD with AWS-faithful validation, errors and defaults — but no side effects. IAM stores policies; by default it does not enforce them. |
| **Stub** | Registered and protocol-correct, but operations return `501`. |

**The `501` contract.** An unimplemented operation never returns a bare `404`
or a plausible-looking empty `200`. It returns `501 Not Implemented`, in the
correct error format for that service's protocol, with an
`x-emulator-unsupported: true` header. Both are set by the framework, never by
hand. An SDK surfaces this as a clear error rather than a mystery.

**The asymmetry rule.** When behaviour is unclear and evidence runs out, being
*stricter* than AWS is preferred to being *more permissive*. Rejecting
something AWS accepts fails loudly, locally, now. Accepting something AWS
rejects fails silently here and then breaks in a real account later. A `501`
with an honest explanation beats a `200` that is wrong.

---

## 2. What an AWS SDK call actually is

This section exists because everything else depends on it. If you have never
watched what an AWS SDK puts on the network, the rest of Overcast will look
arbitrary.

### It's just HTTP

When you write this:

```js
await s3.send(new PutObjectCommand({ Bucket: "my-bucket", Key: "hello.txt", Body: "hello" }));
```

the SDK does three things: works out **which host** to talk to, encodes your
arguments into an **HTTP request**, and **signs** it. That's all. There is no
persistent connection to AWS, no special transport, no binary RPC channel. It
is an ordinary HTTP request that you could reproduce with `curl` if you were
patient enough to compute the signature.

That request looks roughly like this:

```http
PUT /my-bucket/hello.txt HTTP/1.1
Host: s3.us-east-1.amazonaws.com
Content-Type: text/plain
x-amz-content-sha256: 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
X-Amz-Date: 20260809T000000Z
Authorization: AWS4-HMAC-SHA256 Credential=AKIA.../20260809/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=9f0e...

hello
```

An emulator, then, is a web server that accepts requests of this shape and
replies with what AWS would have replied. Everything hard about Overcast is a
consequence of "what AWS would have replied" being much more varied than you
might expect.

### Which host? — endpoint resolution

Every SDK has a built-in table of rules mapping *(service, region)* to a
hostname: `s3.us-east-1.amazonaws.com`, `sqs.eu-west-2.amazonaws.com`, and so
on. You never see this because it is automatic.

Overcast works because that resolution is overridable — `AWS_ENDPOINT_URL`, the
CLI's `--endpoint-url`, or a client-level option. Point it at
`http://localhost:4566` and every call lands on Overcast instead.

**This override is not honoured uniformly, and that has bitten this project
repeatedly.** The clearest case: the JavaScript SDK's SQS client rewrites the
request endpoint to the origin of the *queue URL* you pass in, ignoring
`AWS_ENDPOINT_URL`. .NET and Java v1 go further — for them the queue URL
literally *is* the request URI. So if Overcast hands out a queue URL naming a
host the caller cannot reach, those SDKs fail while the AWS CLI succeeds,
because the CLI ignores the queue URL's origin entirely.

That is why Overcast mints client-facing URLs **per request, on the origin the
caller actually reached it on** — a request arriving on `localhost:4566` gets a
`localhost:4566` queue URL; one arriving from inside a container on the Docker
network gets an address routable from there. It is also why testing an
endpoint change with the AWS CLI alone proves nothing.

Full treatment: [docs/dev/manual-testing.md](./manual-testing.md).

### What's in the body? — wire protocols

Here is the part you may never have had to think about, even using AWS daily.

A **wire protocol** is the convention for encoding *which operation you are
calling* and *what arguments you are passing* into an HTTP request — and for
encoding the response and any error coming back. It is not a network protocol
like TCP or HTTP itself; it sits on top of HTTP. Think of it as the calling
convention. The term is AWS's own, via Smithy, its interface definition
language: see [Smithy's wire protocol selection
guide](https://smithy.io/2.0/guides/wire-protocol-selection.html) for the
specification.

AWS does not have one wire protocol. It has several, because services built in
different eras adopted whatever was current at the time, and AWS keeps old
services working forever. Listed oldest first, because the order turns out to
be the point:

| Protocol | Roughly when it was the default for new services | How the operation is named | Body format | Used by |
|---|---|---|---|---|
| **REST-XML** | The earliest era, ~2006 onwards | HTTP method + URL path | XML, plus raw bodies | S3, CloudFront, Route 53 |
| **AWS Query** | Also earliest, ~2006–2011 | `Action=CreateQueue` field in a form-encoded body | `application/x-www-form-urlencoded` in, XML out | IAM, STS, SNS, EC2, CloudFormation, RDS, SES |
| **AWS JSON 1.0 / 1.1** | ~2012 onwards | `X-Amz-Target: DynamoDB_20120810.PutItem` header | JSON in, JSON out | DynamoDB, SQS, Kinesis, ECS, KMS, most modern services |
| **REST-JSON** | ~2014 onwards | HTTP method + URL path template | JSON body, plus values bound to path/headers | API Gateway, Lambda control plane |
| **Smithy RPC v2 CBOR** | Newest, rolling out through SDKs now | Path `/service/X/operation/Y` + `Smithy-Protocol` header | CBOR (binary JSON) | An increasing number of services, as SDKs adopt it |

(Treat the dates as approximate — the ordering is what matters. *Smithy*, in
the last row, is the interface definition language AWS now publishes its
service models in; the newest protocol is named after it, and those same
published models turn up again in [§11](#11-how-we-know-it-behaves-like-aws) as
a source of truth Overcast checks itself against.)

### Why five, and why two of them are the same age

The first two overlap because web APIs in 2006 had not settled a question that
is still open: **is an API a set of resources, or a set of actions?**

Resource-oriented design says resources are URLs and HTTP verbs carry the
meaning. RPC-over-HTTP says you call a named procedure with named arguments.
AWS Query is the lightweight RPC option — the same model as SOAP, which early
AWS services also offered, but with arguments flattened into form fields
instead of an envelope.

AWS answered the question per service, according to the domain:

- **S3 is resources.** An object *is* a URL. `PUT /bucket/key` with the bytes
  as the body is the obvious expression; `GET` fetches, `DELETE` removes. S3
  also has to carry arbitrary raw payloads — you cannot wrap a 5 GB video in an
  XML envelope — so the body must *be* the object. REST-XML is the only
  workable choice.
- **EC2 is actions.** `RunInstances`, `AuthorizeSecurityGroupIngress`. There is
  no natural URL for "authorize ingress on a security group". Same for IAM's
  `AssumeRole` and SQS's `SendMessage`. RPC fits; REST would be contortion.

That fork never resolved. It simply changed serialisation format each era:

|  | XML era | JSON era | CBOR era |
|---|---|---|---|
| **Resource-shaped** | REST-XML | REST-JSON | — |
| **Action-shaped** | AWS Query | AWS JSON 1.0/1.1 | Smithy RPC v2 |

So the five protocols are two API styles crossed with three serialisation
eras. That is much easier to hold than a list, and it predicts: a new
resource-shaped service will speak REST-JSON, a new action-shaped one JSON or
CBOR.

The empty cell is structural, not an omission. CBOR's benefit is compact binary
encoding of a *whole* body, which is what an RPC protocol has — everything is
in the payload, so switching JSON for CBOR compresses all of it. A REST
protocol deliberately scatters values across path, headers and query string,
and those are text by definition; and for the service where it would matter
most, S3, the body is already raw bytes with nothing to re-encode. So the
action-shaped branch keeps chasing better serialisation because that is where
its cost lives, while the resource-shaped branch stopped at JSON.

It also explains a structural decision in Overcast. S3 being resource-shaped is
exactly why it carries no distinguishing header — its operation is encoded in
the URL, and that URL is a bucket name that could be almost any string. Had S3
been an action-shaped service in 2006, the router in
[§4](#4-the-request-path) would be markedly simpler.

**A service's protocol is fixed at launch and essentially never changes**,
because AWS keeps old clients working indefinitely. So the protocol is a fossil
record: if a service speaks Query, it is old; if it speaks JSON, it arrived
after roughly 2012. That is the entire explanation for why an emulator needs
five decoders instead of one.

Migrations do happen, but rarely, and additively. SQS moved from Query to JSON,
and Overcast still accepts **both** — along with CBOR — because older .NET and
Java SDKs never moved and would otherwise break.

Two consequences follow immediately, and they shape large parts of the
codebase:

**One.** To answer a request, Overcast must first work out which protocol it is
looking at, then decode accordingly. That is a real dispatch problem, not a
detail — see [§4](#4-the-request-path) and [§5](#5-one-handler-many-protocols).

**Two.** S3 is the awkward one. A JSON-protocol request announces itself with
an `X-Amz-Target` header; a Query request has `Action=` in the body. An S3
request has **no distinguishing marker at all** — it is a plain HTTP verb
against a path, and the path is a bucket name that could be nearly any string.
This single fact is why S3 is the *last* thing the router tries, rather than
just another registered service.

### Who are you? — SigV4

Every AWS request is signed. The signature lives in the `Authorization`
header, and its structure is the other thing that makes emulation practical:

```
Authorization: AWS4-HMAC-SHA256
  Credential=AKIAIOSFODNN7EXAMPLE/20260809/us-east-1/dynamodb/aws4_request,
  SignedHeaders=host;x-amz-date,
  Signature=9f0e...
```

The `Credential=` portion contains a **credential scope**:
`<access-key>/<date>/<region>/<service>/aws4_request`. That is plain text. It
is an *input* to the signature, not something the signature conceals.

So Overcast can read the region and the target service straight out of a
signed request without doing any cryptography. Region determines which
partition of state the request touches; service helps identify the request when
the path alone is ambiguous. Both come free.

Signature *validation* is implemented and can be switched on with
`OVERCAST_SIGV4_VALIDATE=true`, but it is **off by default** — any credentials
are accepted, `test`/`test` by convention. This follows from "not a security
boundary": validating signatures locally adds friction and catches nothing you
care about in development.

### What comes back when it fails

Errors are as protocol-specific as requests. The same logical failure is:

- `<Error><Code>NoSuchBucket</Code>…</Error>` — bare XML, for S3
- `<ErrorResponse><Error><Code>…` — wrapped XML, for Query services
- `{"__type": "ResourceNotFoundException", "message": "…"}` — for JSON services

Getting these exactly right matters more than it looks. SDKs map the error code
onto a typed exception class, and their **retry logic keys off specific
codes** — a throttling response has to say `ThrottlingException` for a JSON
service and `SlowDown` for S3, or the SDK's built-in retry will not engage.
Inventing a generic error would break client behaviour that AWS users depend on
without realising it.

There is even a wire-level inconsistency Overcast must reproduce: most services
return the request ID in `x-amzn-requestid`, but S3 uses `x-amz-request-id`.
Real AWS is inconsistent here; so is Overcast, deliberately.

**Further reading:** [docs/dev/smithy.md](./smithy.md) for protocol
identification and dispatch in depth; [docs/sdk-cli.md](../sdk-cli.md) for
per-language endpoint configuration.

---

## 3. The shape of the repository

```
cmd/overcast/         the CLI: serve, bridge, status, trust, https, import
internal/
  router/             wires every service into one HTTP server; owns middleware
  middleware/         request ID, logging, recovery, SigV4, region, IAM
  protocol/           AWS wire formats — codecs, errors, ARNs, typed dispatch
  services/           one package per AWS service (CloudWatch Logs nests
                      under cloudwatch/, so 49 directories, 50 services)
  state/              the Store interface and its four backends
  serviceutil/        shared helpers every service uses
  docker/             Docker Engine API client
  events/             internal event bus
  bff/                serves the embedded web console
  …                   ~28 packages in total
tests/
  integration/        HTTP-level tests, one directory per service
  helpers/            TestServer, assertions, mock store
compat/               real AWS SDKs and IaC tools, run against a live instance
docs/                 user docs, service reference, dev docs, plan docs
```

**Proportions worth knowing.** Service emulation code is roughly 65% of all Go
in the repository. The integration test suite is almost exactly the same size
as the service code it tests. The web console is a substantial TypeScript
application in its own right, around a quarter the size of the Go backend.

**Fifty services are registered.** Coverage per service ranges from
comprehensive to stub — see [STATUS.md](../../STATUS.md), which is generated
from the capability registry rather than hand-maintained.

**Service packages are flat.** A service's own files live directly in
`internal/services/<name>/` with no subdirectories, because a subdirectory
would be a separate Go package and lose access to unexported types. Files are
split by concern (`service.go`, `typed_ops.go`, `typed_logic.go`, `store.go`),
not by operation.

Two directories do nest — `cloudwatch/logs`, which is a *separate* registered
service rather than part of CloudWatch, and `cognito/templates`. Neither
violates the rule as intended, but `CONTRIBUTING.md` states it without the
carve-out, so the wording is being tightened in
[#746](https://github.com/overcast-sh/overcast/issues/746).

**Some sources are generated and committed.** The capability registry, the docs
search index, and the AWS operation manifest are all generated from code or
from pinned AWS models, and CI fails if the committed output does not match a
fresh regeneration. Never hand-edit them. See
[AGENTS.md § Generated files](../../AGENTS.md#generated-files).

---

## 4. The request path

Follow one call: an SQS `SendMessage` from a Node.js client. Everything below
is runnable — worth having it going in another window while you read.

### Run it

Start Overcast with debug enabled, so the trace at the end of this section
works:

```bash
docker run --rm -p 4566:4566 -p 4567:4567 \
  -e OVERCAST_DEBUG=true \
  ghcr.io/overcast-sh/overcast:alpha
```

Then, with `npm install @aws-sdk/client-sqs`:

```js
import {
  SQSClient,
  CreateQueueCommand,
  SendMessageCommand,
} from "@aws-sdk/client-sqs";

// The only thing that differs from real AWS is `endpoint`.
// Credentials are required by the SDK but not validated by default.
const sqs = new SQSClient({
  endpoint: "http://localhost:4566",
  region: "us-east-1",
  credentials: { accessKeyId: "test", secretAccessKey: "test" },
});

const { QueueUrl } = await sqs.send(
  new CreateQueueCommand({ QueueName: "demo" }),
);

const res = await sqs.send(
  new SendMessageCommand({ QueueUrl, MessageBody: "hello" }),
);

console.log("queue:      ", QueueUrl);
console.log("message id: ", res.MessageId);
console.log("request id: ", res.$metadata.requestId);
```

**Use the `QueueUrl` that `CreateQueue` returned; do not build it by hand.**
That is the per-caller URL minting from
[§2](#2-what-an-aws-sdk-call-actually-is) in action — Overcast minted that URL
on the origin your process reached it on, and this SDK will send the message to
the queue URL's origin rather than to the endpoint you configured. Hand-writing
the URL is how you produce a bug that reproduces in Node and not in the CLI.

### What went on the wire

The `SendMessage` call became this:

```http
POST / HTTP/1.1
Host: localhost:4566
Content-Type: application/x-amz-json-1.0
X-Amz-Target: AmazonSQS.SendMessage
Authorization: AWS4-HMAC-SHA256 Credential=test/20260809/us-east-1/sqs/aws4_request, ...

{"QueueUrl":"http://localhost:4566/000000000000/demo","MessageBody":"hello"}
```

Note what identifies the operation: not the path, which is just `/`, but the
`X-Amz-Target` header. SQS is an action-shaped service speaking AWS JSON 1.0.
And note the credential scope — `us-east-1/sqs` — which is where the region and
service come from without any cryptography.

### What happened inside

**At startup**, `router.New` builds the HTTP router (the project uses
[chi](https://github.com/go-chi/chi)), registers the middleware
chain, constructs every service, wires cross-service dependencies, and
registers a small number of catch-all routes last. Service constructors must
be pure field assignment — no store reads, no I/O — because they all run before
the server can accept a connection. Anything expensive is deferred behind a
a run-once guard (Go's `sync.Once`) and done on first use instead. See
[§13](#13-performance).

**The request arrives** as `POST /` with `X-Amz-Target: AmazonSQS.SendMessage`
and a JSON body. The whole path looks like this — note that the middleware
chain is traversed in both directions, which is why the log line for a request
appears only once its handler has finished:

```mermaid
sequenceDiagram
    autonumber
    participant C as Node.js client
    participant M as Middleware chain
    participant R as Router
    participant S as SQS service
    participant ST as state.Store
    participant B as Internal event bus

    C->>M: POST / · X-Amz-Target: AmazonSQS.SendMessage
    Note over M: request ID · region from the SigV4 scope <br/>caller's origin · wire protocol identified
    M->>R: request, enriched context
    R->>S: matched on the AmazonSQS. target prefix
    Note over S: codec.Decode into a Go struct
    S->>ST: load queue, store message
    ST-->>S: ok
    S->>B: publish SQSMessageSent
    Note over S: codec.WriteResponse, same codec
    S-->>R: response struct, encoded
    R-->>M: response
    Note over M: only now does the logger record <br/>status, service and operation
    M-->>C: 200 · MessageId, MD5OfMessageBody
```

Step by step:

1. **Middleware chain**, in order: real IP, CORS, body draining, host-based
   addressing, request ID, panic recovery, debug tracing, logging, readiness,
   request events, SigV4 parsing, IAM enforcement, region resolution, client
   endpoint capture, and protocol identification.

   Most are unremarkable. Three matter:
   - **Region** extracts the region from the SigV4 credential scope and puts it
     in the request context. State is partitioned by region, so this decides
     which resources the request can see.
   - **Client endpoint** records the origin the caller actually dialled, so
     that any URL minted in the response is reachable *by this caller*.
   - **Protocol** identifies the wire protocol by inspecting headers in a fixed
     precision order, and stashes the codec and operation name in the context.

2. **Routing.** Most services register explicit routes. The generic
   `POST /` handler looks at `X-Amz-Target`, matches the `AmazonSQS.` prefix to
   the SQS service, and hands off.

3. **The service dispatches** to a typed operation: decode the body into a Go
   struct, call a plain function, encode the result. Covered in
   [§5](#5-one-handler-many-protocols).

4. **The handler** validates input, reads and writes through `state.Store`,
   publishes an event on the internal bus, and returns a response struct.

5. **The response** is encoded by the same codec that decoded the request, so
   the reply is always in the protocol the caller used.

### Now go and look at it

Everything above is observable. Take the request ID the script printed and ask
for its trace:

```bash
curl -s http://localhost:4566/_overcast/debug/trace/<request-id> | jq
```

You get the request and response as they actually were — headers, bodies — plus
every internal hop the request triggered along the way. For a `SendMessage`
that is a short list. Run the same thing against a `cdk deploy` and one trace
contains hundreds of hops, which is the point: this is how you find out why
something failed three services deep.

Two other things worth trying while it is running: the console at
<http://localhost:4567>, and `curl -N http://localhost:4566/_overcast/events`, which
streams internal events live as you make calls. Both are covered in
[§10](#10-what-overcast-adds-on-top-of-aws).

### Why S3 is last

One arrow in the sequence diagram above — *matched on the `AmazonSQS.` target
prefix* — hides the least obvious decision in the router. Our request was easy
to place because it announced itself with a header. Not every request does.

Because an S3 request has no distinguishing marker ([§2](#2-what-an-aws-sdk-call-actually-is)),
S3 cannot simply be registered alongside everything else — it would swallow
requests belonging to other services.

Instead, S3's routes are registered on a **private router** that is only
consulted after everything else has declined. Before falling through to it, the
router asks a **generated registry of every AWS operation** — built from AWS's
own published service models — whether the request matches some other modelled
AWS operation. If it does, and the evidence says the caller was not addressing
S3, the request gets a protocol-correct `501` for the *right* service rather
than a confusing S3 error.

So the full claiming order is:

```mermaid
flowchart TD
    R([Request]) --> M[Middleware chain <br/>request ID · region · protocol identification]
    M --> E{Matches an explicitly <br/>registered route?}
    E -->|yes| H([Service handler])
    E -->|no| T{"Carries X-Amz-Target <br/>or Action= ?"}
    T -->|"yes — a registered <br/>service claims it"| H
    T -->|"yes — modelled by AWS <br/>but not implemented here"| N([501 + x-emulator-unsupported])
    T -->|no| G{"Does the generated AWS <br/>operation registry claim it, <br/>and was the caller <br/>not addressing S3?"}
    G -->|yes| N
    G -->|no| S([S3 private router])
```

Two practical consequences:

- **Adding a service with versioned REST paths** (`/2018-10-31/...`) means
  registering the routes *and* adding the path prefix to service detection in
  the logger. Otherwise its requests will be logged as `service=s3` and will
  bypass middleware that branches on service name.
- **A bug looks like S3.** A typo in a route, a missing registration, or a
  misnamed path parameter makes a supported request miss its handler and land
  in the fallback. If you see `service=s3` for something obviously not S3,
  suspect the route before suspecting the classifier.

---

## 5. One handler, many protocols

Fifty services, each with dozens of operations, several of which are exposed
over more than one wire protocol. Written naively that is *operations ×
protocols* pieces of encoding code. Here is how that is avoided.

**A codec per protocol.** Each wire protocol gets one implementation of a small
interface: decode a request into a Go value, write a response, write an error.
Roughly six of these exist for the whole system.

**A typed operation wrapper.** An operation is registered as a generic value
parameterised by its request and response types, wrapping a function of the
form:

```go
func(ctx context.Context, in *CreateQueueRequest) (*CreateQueueResponse, *protocol.AWSError)
```

That function is pure business logic. It never sees an `http.Request`, never
marshals anything, and has no idea which protocol the caller used. The wrapper
decodes with whichever codec the middleware identified, calls the function, and
encodes the result with the same codec.

**One struct serves every protocol, via *struct tags*.**

> A note on the word, because it is overloaded here. **Struct tags** are a Go
> language feature and have nothing to do with **AWS resource tags** — the
> key/value labels you put on a bucket or a queue. Both appear in this
> document. Where it says "struct tag", it means the language feature.

If you work in another language, a struct tag is Go's equivalent of a
serialisation annotation: Java's `@JsonProperty`, C#'s `[JsonPropertyName]`, a
Python dataclass field's metadata. It is a string literal attached to a field
that encoders read at runtime to decide what that field is called on the wire.

```go
type sendMessageRequest struct {
	QueueUrl    string `json:"QueueUrl"`
	MessageBody string `json:"MessageBody"`
}
```

`json:"QueueUrl"` says "when encoding to or decoding from JSON, this field is
named `QueueUrl`". The useful part is that a field can carry **several**
annotations at once, under different keys — `json`, `xml`, `cbor` — and each
encoder reads only the key it understands. So one struct definition can
describe several wire formats simultaneously.

Overcast leans on that hard. A request struct carries `json` tags, plus `cbor`
or `xml` tags where a format needs a different name, and whichever codec is
active reads the one it recognises.

The Query decoder is the neat case: it has no tag key of its own. It uses
reflection to read the *same* `json` tags, because AWS's Query member names and
its JSON member names both derive from the same modelled shape. So a single
annotated struct serves JSON, CBOR and Query decoding between them.

```mermaid
flowchart LR
    Q[AWS Query] --> D
    J[AWS JSON 1.0 / 1.1] --> D
    C[Smithy RPC v2 CBOR] --> D
    D{{"codec.Decode <br/>whichever protocol <br/>the caller used"}} --> F
    F["handler(ctx, *Request) (*Response, *AWSError) <br/> <br/>plain Go — no HTTP, no marshalling, <br/>no knowledge of the protocol"]
    F --> W{{"codec.WriteResponse <br/>the same codec"}}
    W --> O([Response in the <br/>caller's own protocol])
```

The result: adding a protocol to a service that already has typed operations is
close to free.

### What is deliberately *not* abstracted

An accurate picture matters more here than a tidy one — and the reasons differ.
Some of these are principled and permanent; others are simply undone.

- **REST protocols stay outside this layer entirely — by design, permanently.**
  For S3, CloudFront, API Gateway and Lambda, the route *is* the operation
  identity, and values bind to path segments, headers and raw streaming bodies
  rather than to one decodable payload. There is often no structured body to
  hand a codec at all — an S3 `PutObject` body is the object. Routing these
  through a whole-body encoding layer would mean rebuilding HTTP binding rules
  inside a mechanism designed for something else. Do not expect this to change.

- **Query XML response envelopes are hand-written — tracked, not principled.**
  Every Query operation declares its own struct wrapping the result in
  `<XxxResponse><XxxResult>…</XxxResult><ResponseMetadata>…`; there are 131 such
  declarations. The wrapper is identically shaped every time, so a shared helper
  or a generator could remove it. It persists because it has never been a
  priority, not because it should. Tracked as
  [#755](https://github.com/overcast-sh/overcast/issues/755).

- **Request and response structs are hand-written — planned for generation.**
  AWS publishes machine-readable models for every operation, and Overcast
  already ingests them for routing and `501`s ([§4](#4-the-request-path)) —
  but not for producing types. Generating them from the same source is designed
  and sequenced but not started, tracked as
  [#756](https://github.com/overcast-sh/overcast/issues/756). Until it happens, every
  struct and every tag is typed by hand, which is exactly where a wrong member
  name comes from.

- **Migration to the typed pattern is incomplete — and in one case deliberately
  reversed.** Many services carry both a typed registry and an older
  hand-written handler set covering the same operations. EC2 goes further: its
  typed dispatch is switched off entirely, leaving 69 typed operations
  unreachable, after it was found to ignore filters on several `Describe*`
  calls and to drop mutations such as `TerminateInstances`. Routing the whole
  service back to the legacy path was the right call — but it means the
  operation manifest lists 69 operations as typed that no request ever reaches.
  Re-enabling needs an operation-by-operation audit, tracked as
  [#754](https://github.com/overcast-sh/overcast/issues/754).

If you are adding a service, use the typed pattern; if you are reading an old
one, expect to find both.

**Further reading:** [docs/dev/smithy.md](./smithy.md) for protocol
identification and the codec interfaces;
[#756](https://github.com/overcast-sh/overcast/issues/756) for where the migration is
going.

---

## 6. State and storage

Every emulated resource — buckets, queues, tables, functions — is a value in a
key-value store behind one interface, `state.Store`. Services never touch a
database directly.

Keys are namespaced `service:kind` (`sqs:queues`, `s3:objects`), and region is
part of the key, not the namespace. That is how one process holds resources for
several regions without them colliding, and why a resource created in
`us-east-1` is genuinely invisible from `eu-west-1` — as on AWS.

### Four backends

| Backend | Behaviour |
|---|---|
| `memory` | Everything in process memory. Fastest, lost on restart. |
| `hybrid` | Reads from memory, flushes to SQLite asynchronously, with a durable pending log so nothing is lost on a crash. |
| `persistent` | Every write goes to SQLite synchronously. Slowest, most durable. |
| `wal` | In-memory with an append-only log, replayed at startup and periodically compacted. |

The default is `auto`, which resolves at startup: if a volume or bind mount is
present at the data directory, or a database already exists there, it picks
`hybrid`; otherwise `memory`. This gives the behaviour each environment wants
with no configuration — CI containers with no volume get fast ephemeral
storage; a `docker run -v ...` gets persistence.

Except in a `-tags nosqlite` build (the `overcast-slim` image and the
`overcastd` binaries), where `hybrid` and `persistent` are compile-time stubs
that return an error. `auto` short-circuits to `memory` there before weighing
any signal, so a mounted volume persists nothing; `wal` is the only durable
backend those artifacts have. See
[docs/storage.md § Builds without SQLite](../storage.md#builds-without-sqlite).

### Hybrid is not a cache

The name misleads. Hybrid classifies every namespace as either **hot**
(control-plane metadata: queue definitions, function configs — seeded into
memory at startup and kept there) or **cached** (bulk data-plane records:
messages, log events, metric datapoints — read from SQLite on *every* access,
with a small overlay for pending writes).

There is no read-through cache for the second category, by design. It means
high-volume data never has to fit in memory.

### Where large payloads actually live

Three different answers, worth knowing before you go looking in the wrong
place:

- **S3 object bodies** are not in the store at all. They are files on disk,
  deliberately, to avoid unbounded heap growth and to allow streaming.
- **Lambda deployment packages** *are* in the store, base64-encoded, in their
  own namespace.
- **EFS file data** is in real Docker volumes, entirely outside Overcast's
  storage layer. Only metadata is in the store.

### One rule worth knowing

**A single corrupt record must never take down more than itself.** The existing
code already behaves this way, and it is the rule new service code is expected
to follow: reading many records skips malformed ones and logs the gap; reading
one named resource returns an AWS-shaped not-found rather than a 500.
`InternalError` is reserved for genuine infrastructure failure — the store being
unavailable — not for one bad payload.

**Further reading:** [docs/storage.md](../storage.md) for the user-facing
comparison; [docs/dev/storage-backends.md](./storage-backends.md) for the
internals, including known limitations of each backend.

---

## 7. Docker: making services real

For several services, Overcast does not simulate behaviour — it runs the real
thing in a container.

| Service | What gets started |
|---|---|
| Lambda | One container per execution environment, from AWS's own runtime images |
| ECS | A container per task container definition |
| RDS | A real database engine (MySQL, Postgres) per instance |
| ElastiCache | A real Redis/Valkey/Memcached instance |
| EFS | A Docker volume per file system (plus optional NFS export containers) |

```mermaid
flowchart TB
    subgraph host["Your machine"]
        APP["Your code / CLI <br/>AWS_ENDPOINT_URL=http://localhost:4566"]
    end

    subgraph docker["Docker daemon — everything below is a sibling"]
        OC["Overcast <br/>:4566 AWS API · :4567 console <br/>:9001 Lambda Runtime API · :53 DNS"]
        LAM["Lambda container <br/>AWS runtime image + your code"]
        DB[("RDS engine container <br/>real MySQL / Postgres")]
    end

    APP -->|AWS API calls| OC
    OC ==>|"creates, via the Docker API"| LAM
    OC ==>|"creates, via the Docker API"| DB
    LAM -->|"calls AWS APIs back <br/>endpoint injected as a name"| OC
    LAM -->|"polls for work <br/>Lambda Runtime API"| OC
    LAM -.->|"resolves the DB hostname via a <br/>Docker network alias, not via Overcast"| DB
```

**These are siblings, not children.** Overcast asks the Docker daemon to create
containers alongside itself. This is true whether Overcast runs natively or is
itself containerised. The immediate consequence: `localhost` inside a Lambda
container is *that container*, never Overcast.

Overcast needs access to the Docker socket to do any of this. Running natively
it has that already; running in a container you supply it, by mounting
`/var/run/docker.sock` in. Without it these services degrade to metadata-only
rather than failing — resources are created and describable, but nothing runs
behind them — and everything else (S3, SQS, DynamoDB, and so on) is unaffected.

### A Lambda invocation, end to end

1. `Invoke` arrives. The pool checks concurrency limits and either reuses a
   warm container, starts a cold one, or queues.
2. A cold start pulls the runtime image if needed, builds a single tar
   containing the function code, any layers and **Overcast's init**, creates the
   container, copies the tar in, starts it, and waits for an IP. The init is the
   container's entrypoint; the command the container would otherwise have run —
   a zip function's `/lambda-entrypoint.sh <handler>`, an image function's
   `ENTRYPOINT`+`CMD` — becomes its argument list.
3. The init runs as PID 1, launches that command and any `/opt/extensions`
   binaries as its children, and serves the Runtime API on `127.0.0.1:9001`
   inside the container, proxying it to Overcast's per-environment endpoint
   (port 9001 on the host). The container's AWS runtime client long-polls it for
   work — the same protocol real Lambda uses. Its first poll is the signal that
   initialisation finished.
4. Overcast hands over the event; the handler runs; the client posts the
   response back; the waiting HTTP request completes. Because the init owns the
   children's stdout and stderr, it knows which invocation every line belongs
   to: it ships them to Overcast on a second, long-lived connection, and it
   drains both pipes before forwarding the response, so the invocation's log
   tail and its CloudWatch ordering are exact rather than inferred. See
   [the plan](../plans/lambda-in-container-init.md).
5. The container returns to a warm pool rather than being destroyed, so the
   next invocation skips steps 2 and 3.

Because code, environment and memory are baked in at creation, a configuration
change *retires* the environment rather than mutating it — a running container
could never observe the change.

### Two resolvers, and why a missing alias hangs

Containers Overcast starts need to resolve two different kinds of name, and
**two separate mechanisms answer them**:

| Question | Answered by |
|---|---|
| "Where is Overcast?" | Overcast's own DNS server, which claims the split-horizon domains |
| "Where is that other container?" (a database endpoint, say) | Docker's embedded resolver, from **network aliases** on the container |

Both are doing the right thing, and the split is necessary rather than
accidental. Overcast's resolver has to be *authoritative* for the whole
split-horizon domain, because that is what makes wildcard subdomain names
resolve from inside a container — `mybucket.s3.localhost.overcast.sh`, an API
Gateway invoke URL, a Lambda function URL — and `/etc/hosts` cannot express a
wildcard. Docker's resolver has to own container-to-container names, because
only Docker knows which containers are attached to which network.

Where it gets awkward is how the two compose when something is missing — enough
that the repository treats it as a known hazard.
[docs/dev/container-networking.md](./container-networking.md) opens by warning
that "the last agent to need container-to-container name resolution built a
second DNS mechanism next to the one that already worked", and
[AGENTS.md](../../AGENTS.md) calls picking the wrong resolver "the classic
mistake here".

If a resource's hostname was never registered as a Docker network alias,
Docker's resolver misses and forwards the query upstream — to Overcast's
resolver, which *is* authoritative for that domain and duly answers with
Overcast's own address. The client opens a TCP connection to Overcast on the
database port and hangs, because nothing there speaks MySQL. The symptom is a
hang rather than a name-resolution error, so you go looking at the database
instead of at DNS.

**This is not something a stricter resolver would fix.** Overcast cannot
distinguish "this subdomain should have been a container" from "this subdomain
is one of mine": resource hostnames are minted at runtime inside a namespace
Overcast legitimately owns, and refusing to answer for names it does own would
break virtual-hosted S3 and every host-routed service. Forging `NXDOMAIN` would
be worse still, since clients cache negative answers and the failure would
outlive the fix.

So the bug is never in the resolvers. It is that the name was never registered
with Docker.

What a resolver *can* do is recognise the shapes Overcast itself mints — a name
matching `{id}.{region}.rds.{base}` is a data-plane endpoint whatever else is
true of it — and refuse those specifically, naming the resource and the caller,
rather than answering with an address that hangs. That is narrower than the
"stricter resolver" ruled out above, which is why it is possible at all; it is
planned rather than built, in
[docs/plans/container-network-topology.md](../plans/container-network-topology.md).

**Overcast does that registration itself**, as part of starting the container —
there is nothing for you to configure, and nothing to set up in Docker. This
only becomes your problem if you are *adding* a service that starts containers,
because the registration has three parts and missing any one of them produces
the same hang:

1. **The hostname is attached to the container as a Docker network alias.** An
   alias is an extra name Docker's resolver will answer for. A container
   attached without one is reachable by IP address and by nothing else.

2. **Every hostname the endpoint could have been handed out under is
   registered.** Overcast builds a resource's hostname from the host the
   *calling client* used, so the same database is legitimately
   `mydb.us-east-1.rds.localhost` to one caller and
   `mydb.us-east-1.rds.localhost.overcast.sh` to another. Whichever process
   resolves the name later is often not the one that received it, so every base
   has to work — not only the one in play when the container was created.

3. **It happens on every network a caller might be on.** Aliases are
   per-network, so a container is invisible by name on any network it did not
   register them on. Overcast keeps this from being a per-service decision:
   every container it starts shares one **data plane**, and `internal/dataplane`
   is the only thing that picks it. There used to be one network per emulator
   service instead, and the reachability of any given pair came down to how
   completely that service remembered to bridge the gap — which is the bug
   this list exists to prevent.

`internal/dataplane` is the reference implementation — `Hostnames` for point 2,
`Attach` for points 1 and 3 — and `internal/services/rds/endpoint.go` shows a
service using it. Both are worth reading before adding another
container-backed service.

**Further reading:** [docs/dev/container-networking.md](./container-networking.md)
— short, and required before touching either resolver.

---

## 8. Cross-service integration

Real AWS services trigger each other: S3 notifies SQS, SQS drives Lambda,
DynamoDB streams fan out, EventBridge routes events to arbitrary targets.
Overcast reproduces this, and uses **three different mechanisms** to do it.
Knowing which applies is the key to reading any pipeline in the codebase.

### The three patterns

**1. Direct Go interfaces.** Narrow interfaces, implemented by the concrete
service, wired together once at startup. SNS holds a "message enqueuer" that is
really SQS. A plain method call — no HTTP, no serialisation.

*Used when* both ends are known at wiring time and the path is hot: SNS→SQS,
S3→Lambda, SQS→Lambda event source mappings.

**2. Replay through the router.** The caller builds a real HTTP request and
serves it against Overcast's own router — in process, no socket, no network,
just a handler call with a response recorder.

Going the long way round rather than calling the target service directly buys
four things:

- **Behaviour is defined once.** The target's validation, defaulting, error
  shapes and ARN formats all apply, because it is the same handler an SDK call
  reaches. A CloudFormation resource handler writing to a service's store
  directly would drift from what that same resource does when created through
  the API — and the drift would be invisible until someone compared the two.
- **Failures are real failures.** A missing queue produces SQS's own error,
  not a nil the caller forgot to check. Silent no-ops are the failure mode this
  pattern exists to prevent.
- **The hop is traceable.** Each one is recorded, so `/_overcast/debug/trace/{id}`
  renders a CloudFormation deploy as the nested call chain it actually is. A
  direct Go call would not appear at all.
- **The caller needs no knowledge of the destination.** Dispatching to an
  arbitrary ARN would otherwise mean the calling service importing every
  possible target — a maintenance problem, and in Go frequently an import
  cycle.

The cost is a marshal, the middleware chain and a route match. Nobody has
benchmarked it, so treat "small" as an inference rather than a measurement: it
rests on the call being in-process, with no network involved. Measuring it is
tracked as [#757](https://github.com/overcast-sh/overcast/issues/757).

What makes it affordable is where it gets used. Every case is one where the
surrounding work dwarfs it — provisioning a stack resource, delivering an event
to a target, starting a container. The selection rule below is what keeps it off
the hot paths; anything high-volume uses pattern 1 or 3 instead.

*Used when* the destination is an arbitrary ARN supplied at runtime:
EventBridge rule targets, Pipes, CloudWatch alarm actions, Step Functions task
states, and every CloudFormation resource handler.

**3. The internal event bus.** Publish-and-forget, many subscribers. Producers
do not know who is listening.

*Used when* the producer genuinely has no fixed consumer: DynamoDB stream
records are published once and consumed independently by Lambda event source
mappings and by Pipes.

The three have visibly different shapes:

```mermaid
flowchart LR
    subgraph one["1 · Direct interface — fixed pair, hot path"]
        direction LR
        A1[SNS] -->|"Go method call"| B1[SQS]
    end

    subgraph two["2 · Router replay — runtime-supplied ARN"]
        direction LR
        A2[EventBridge rule] --> R2[["Overcast's own router <br/>full middleware chain"]] --> B2["whichever service <br/>the target ARN names"]
    end

    subgraph three["3 · Event bus — producer knows no consumers"]
        direction LR
        A3[DynamoDB write] --> BUS[["internal event bus"]]
        BUS --> C1[Lambda ESM]
        BUS --> C2[Pipes]
        BUS --> C3[console live feed]
    end
```

### The selection rule

- Destination fixed at wiring time, result needed, hot path → **interface**
- Destination is a runtime-supplied ARN → **router replay**, always
- Producer does not know its consumers → **event bus**

The middle rule is the one that gets broken. Hand-rolling an ARN switch instead
of using the shared dispatcher works for the two target types you thought of
and silently drops the rest.

### Two things called a bus

These share vocabulary, appear in the same sections, and are unrelated.

**EventBridge** is an AWS service, which Overcast emulates like any other.
`PutEvents` publishes an event, rules match it against a pattern, and matching
events are delivered to targets. Its behaviour is dictated by AWS, its state is
visible through the AWS API, and the audience for it is **your** code.

**The internal event bus** (`internal/events`) is Overcast's own plumbing, and
nothing about it is an AWS concept. Services publish to it whenever something
happens — an object was written, a container started, a message was delivered —
and Overcast's own machinery subscribes: the console's live feed at `/_overcast/events`,
the topology graph, trace correlation, and the handful of pipelines whose
producer has no fixed consumer. The audience is **Overcast itself**.

Why not use the emulated EventBridge for the internal wiring too, given it is
sitting right there?

**Because the two are under opposite constraints, and keeping them apart is
what preserves both.** The internal bus answers to nobody. Overcast is free to
emit whatever is useful on it — Docker container lifecycle, HTTP request
records, storage flush ticks — precisely because it is not pretending to be an
AWS service. The emulated EventBridge has no such freedom: it must carry what
real EventBridge would carry and nothing else, because a rule in your stack is
matching against it.

Merge them and you lose one side or the other. Either the internal bus inherits
the fidelity constraint and can no longer carry any event AWS has no shape for,
or the emulated bus starts delivering Overcast's own chatter to user rules that
real AWS would never have triggered.

**And it would couple the plumbing to an emulated service.** S3 notifications
working would then depend on EventBridge's emulation being correct — a bug in
one surfacing as a bug in the other.

The two meet wherever an emulated service emits an event real AWS would also
emit. Five do today:

- **CloudWatch alarms** publish a `CloudWatch Alarm State Change` on every
  transition.
- **S3** publishes object events when a bucket has EventBridge notifications
  enabled.
- **EC2** publishes an `EC2 Instance State-change Notification` every time an
  instance's state actually changes — including the initial transition into
  `pending` — from the store layer (`ec2Store.putInstance`), so every call
  site that commits a state (`RunInstances`, `StartInstances`,
  `StopInstances`, `TerminateInstances`, and the scheduler callbacks that
  fast-forward `pending`→`running` and `stopping`→`stopped`) notifies without
  each needing its own wiring.
- **ECS** publishes an `ECS Task State Change` event the same way, from
  `ecsStore.putTask`, whenever a task's `lastStatus` changes.
- **Step Functions** publishes a `Step Functions Execution Status Change`
  event (source `aws.states`) the same way, from `Store.PutExecution`,
  whenever an execution's status actually changes — the initial transition
  into `RUNNING`, then whichever terminal status
  (`SUCCEEDED`/`FAILED`/`TIMED_OUT`/`ABORTED`) it reaches, with `error`/
  `cause` populated on a failure.

CloudWatch and S3 also publish separately onto the internal bus so the console
can render them; EC2, ECS and Step Functions already have their own
internal-bus lifecycle events (`ec2:InstanceLaunched`/`Started`/`Stopped`/
`Terminated`, `ecs:TaskStartFailed`, `stepfunctions:ExecutionStarted`, …) at a
coarser grain, so their EventBridge bridge does not duplicate onto the
internal bus a second time.

That list is short because real AWS emits far more service-originated events
than Overcast does. A rule matching one of those will never fire here. The
extension point exists (`events.BusPublisher`), so this is a gap rather than a
limitation; tracked as [#758](https://github.com/overcast-sh/overcast/issues/758)
(EC2, ECS and Step Functions closed the gap for those three services across
[#1225](https://github.com/overcast-sh/overcast/pull/1225) and
[#1221](https://github.com/overcast-sh/overcast/issues/1221)).

---

## 9. CloudFormation and CDK

CloudFormation is where the emulator has to act as a client of itself.

**`CreateStack`** parses the template, evaluates conditions and mappings, works
out resource ordering from both explicit `DependsOn` and implicit references
discovered by scanning properties for `Ref` and `Fn::GetAtt`, then provisions
in dependency order.

**Each resource type has a handler** — currently over 130 of them — and the
handler's job is deliberately thin: translate template properties into a call
on the underlying service's own API, then capture the physical ID and the
`Ref`/`GetAtt` values. It does this by **dispatching an internal HTTP request
through Overcast's own router**, exactly as an SDK client would.

That indirection is the important design decision. Validation, defaulting,
persistence, error shapes and ARN formats are defined once, in the service, and
CloudFormation gets them for free. A handler that reached into a service's
store directly would drift from the behaviour of the same resource created via
the SDK.

**Physical IDs and attributes must be AWS-shaped** — real ARNs, real ID
prefixes — because they are routinely fed straight into *another* service by
another handler in the same stack. URL-valued attributes are additionally
minted so they are actually reachable, and re-originated per caller when read
back.

**Updates** compare a hash of resolved properties and skip unchanged resources
entirely. A changed resource is updated in place if its handler supports it,
otherwise replaced — new resource created before the old one is deleted, so a
failed replacement can roll back. Deletes run in reverse order, and only a
resource that actively *refuses* deletion fails the stack.

**`cdk deploy`** is: `GetCallerIdentity`, `AssumeRole`, upload assets to the
bootstrap S3 bucket, `CreateChangeSet`, `ExecuteChangeSet`, then poll
`DescribeStacks`. All of it ordinary API traffic.

Resource types without a handler still deploy: they get a synthetic physical ID
and a `warn` log line rather than an error. That is a deliberate trade — failing
the whole stack on one unhandled type would make CDK unusable for anyone whose
template contains a single unsupported resource, which is most templates.

The cost is that a stack can report `CREATE_COMPLETE` while part of it has no
real backing state. The warning is visible at the default log level, but it
appears in Overcast's log rather than in the deploy output, which is where
someone running `cdk deploy` is actually looking. Worth knowing before trusting
a green deploy.

Making that visible — in the deploy output and in the console's CloudFormation
view — is tracked as [#760](https://github.com/overcast-sh/overcast/issues/760).

**Further reading:** [docs/cdk.md](../cdk.md);
[CONTRIBUTING.md § CloudFormation integration](../../CONTRIBUTING.md#cloudformation-integration).

---

## 10. What Overcast adds on top of AWS

Real AWS gives you the API. Overcast gives you the API plus a set of
local-development affordances AWS does not have and could not have.

**The reserved namespace.** Everything Overcast-specific lives under `/_…`.
This is safe precisely because it is a path shape no AWS operation uses and no
legal S3 bucket name can produce. The rule for contributors is absolute: never
add a non-AWS endpoint or a custom response field to the AWS surface — the SDK
must work unmodified — and the `_` namespace is where anything Overcast-only
goes instead.

The main additives:

- **Request tracing** (`/_overcast/debug/trace/{requestId}`). Run with
  `OVERCAST_DEBUG=true` and every request is captured with headers, bodies,
  and — most usefully — every
  *internal service-to-service hop* it triggered. A CDK deploy fans out into
  hundreds of hops under one trace. Nothing in AWS gives you this.
- **A live event stream** (`/_overcast/events`), server-sent, backing the console's
  activity feed.
- **A topology graph** (`/_overcast/topology`) of cross-service resource relationships.
- **The web console** on port 4567: per-service browsers and editors, the
  activity feed, the topology map, state inspection.
- **The Inbox — capture for everything outbound.** Anything Overcast would
  otherwise send out into the world is intercepted and stored instead: email
  from SES, SNS and Cognito, SMS, deliveries to SNS `http`/`https` webhook
  subscriptions, and mobile push. Four channels, one store, browsable in the
  console — so you can check what your application *would* have sent without
  standing up a mail service, an SMS provider or a webhook receiver. It removes
  the need for a third-party mail catcher alongside Overcast, and covers three
  channels a mail catcher would not. Email is the one channel you can redirect:
  point `OVERCAST_SMTP_HOST` at an SMTP server and Overcast relays to it instead
  of capturing locally — useful if your team already runs a shared capture
  instance. The other three are always captured.
- **Lambda hot reload.** Tag a function with a directory on your machine and
  Overcast mounts it into the execution environment, so code edits take effect
  without a redeploy.
- **One-command HTTPS.** `overcast https enable` mints a local CA, installs it
  in the OS trust store, and serves both ports over TLS. This is more useful
  than it sounds: browsers cap HTTP/1.1 at six connections per origin, and the
  console's event stream plus progress streams exhaust that, making the UI feel
  frozen. HTTP/2 over TLS fixes it.
- **Split-horizon DNS.** Public wildcard domains resolving to `127.0.0.1`, so
  one URL works from both the host and inside containers — a problem AWS
  structurally never has.
- **An MCP server** at `/_overcast/mcp`, exposing instance state and a bounded set of
  actions to AI agents.
- **Honesty surfaces**: per-service tiers on `/_overcast/health`, the
  `x-emulator-unsupported` header, and `/_overcast/debug/config` with secrets redacted.

---

## 11. How we know it behaves like AWS

The obvious question to ask of any emulator. There are three test layers and a
documented methodology, and they answer different parts of it.

### Three layers

**Unit tests**, beside the code, covering a single function with no HTTP.

**Integration tests** (`tests/integration/`), which spin up a real server and
speak **raw HTTP** to it. Raw, deliberately: they assert on exact wire format,
and they are fast — the whole suite is expected to run in seconds.

**The compat harness** (`compat/`), which is the interesting one. It runs
**real AWS SDKs and IaC tools** — Node, Python, Go, Java, .NET, Rust, the AWS
CLI, CDK, Terraform, Pulumi — against a live Overcast instance, using them
exactly as production code would. It has zero code-level coupling to the
emulator; it only knows an endpoint URL.

This is what integration tests structurally cannot do. A raw-HTTP test asserts
that Overcast returns what *we believe* an SDK expects. A compat test asserts
that the SDK actually works. Those differ precisely where our belief is wrong,
which is the case that matters.

The harness enforces two independent gates: results may never regress against a
recorded baseline, **and** any failing test fails the build outright regardless
of the baseline. The second exists because the baseline file itself once went
stale for weeks. Flaky tests are quarantined only with explicit reviewer
approval, and [compat/AGENTS.md](../../compat/AGENTS.md) records that every
flake found so far has turned out to be a real emulator race rather than a test
bug.

### The fidelity methodology

When behaviour is unclear, there is an escalation order: AWS's own
documentation first; then existing Overcast code and tests; then other
emulators as cross-reference; and only with explicit permission, a real AWS
account. There is also a structured per-service review process tracking, at
operation granularity, what has been checked against what.

Separately, Overcast pins a revision of **AWS's own published service models**
and generates an operation manifest from them. This is an independent source of
truth for operation names, protocols and shapes — it catches drift no test
would, such as a capability claiming support for an operation that does not
exist in the model.

### The honest limit

Parity rests on AWS documentation, the pinned models, and observed SDK
behaviour through the compat harness. **It does not rest on recorded real-AWS
traffic** — no such captures exist in the repository. Worth knowing before
assuming any given edge case has been verified against the real thing.

**Further reading:** [compat/AGENTS.md](../../compat/AGENTS.md);
[tests/AGENTS.md](../../tests/AGENTS.md);
[docs/dev/compatibility/](./compatibility/).

---

## 12. Keeping 50 services consistent

With fifty services implementing dozens of operations each, the obvious failure
mode is fifty divergent copy-pastes. Two things prevent it.

**A shared library that everything uses.** `internal/serviceutil` and
`internal/protocol` are imported by every single service package. Between them
they own request parsing, pagination, validation, tag handling, region-scoped
keys, structured logging, lazy initialisation, error formatting and
client-facing URL minting.

The URL-minting helper is the instructive one. Before it existed, the
repository had four different base-URL precedences and two of them disagreed
about the port — which was exactly the bug. Centralising it did not tidy the
code; it eliminated a class of defect.

**Standards, with uneven enforcement.** This is worth being clear about,
because it affects how much you can trust a rule to have been followed.

Anything touching *generated artefacts* — capability tables, docs, the AWS
manifest, the changelog — is a hard CI gate. It cannot drift.

Anything that is an Overcast-specific *architecture rule* — use `clock.Clock`
rather than `time.Now()`, never call `os.Getenv` in service code, use
`serviceutil` rather than reimplementing, keep packages flat — is enforced by
review discipline alone. No linter checks any of them, which is why they are
restated across `CONTRIBUTING.md`, `AGENTS.md` and five skill files.

The difference between the two is measurable. `os.Getenv` in service code: no
violations. `time.Now()`: nine call sites across five files, some of which are
probably legitimate and none of which anything flagged. A gate holds a rule
always; discipline holds it most of the time.

The web side reached this conclusion already, and ships a custom ESLint plugin
plus ratchet-style tests that permit existing violations while forbidding new
ones. Two of the Go rules are checkable with a linter golangci-lint already
ships. Tracked as [#762](https://github.com/overcast-sh/overcast/issues/762).

**Further reading:**
[CONTRIBUTING.md § Shared utilities](../../CONTRIBUTING.md#shared-utilities--use-serviceutil-never-duplicate).

---

## 13. Performance

### The startup budget

Overcast aims to start in well under a tenth of a second even with fifty
services registered. That is only achievable because service construction is
forbidden from doing real work: no store reads, no network I/O, no synchronous
schema creation, no background workers (goroutines — Go's lightweight threads)
that do work before their first tick. Anything expensive hides behind a
run-once guard and happens on first use.

This is the single design rule most likely to be violated by accident when
adding a service, and the reason it exists is cumulative: fifty services each
"just" reading one key at startup is fifty round trips before the first
request.

### Where time actually goes

Per-request overhead is small and mostly middleware. Debug tracing compiles to
a no-op when disabled. Logging has a dedicated `TRACE` level below `DEBUG`
specifically so that health probes and console polling do not drown real
request logs.

Container-backed services are different: a Lambda cold start is dominated by
Docker's own container create and start, which is inherent rather than
something Overcast can optimise away.

### Perceived slowness — the important part

A `cdk deploy` measured at roughly 45 seconds wall-clock had **every Overcast
response completing in under 200ms**, with total emulator processing under two
seconds. The rest was CDK CLI startup, `cdk synth` (during which no request
reaches Overcast at all), and — the interesting part — **AWS SDK waiters**.

A waiter checks immediately after the call returns, sees `CREATE_IN_PROGRESS`,
then sleeps five seconds before checking again. A stack that finishes
provisioning in 184 milliseconds still costs a full waiter cycle. Overcast's
answer is to hold the response briefly (`OVERCAST_CFN_SYNC_WAIT_MS`, one second
by default) so the waiter's *first* check already sees the terminal status.

*(Measurement conditions: Docker Desktop on Windows 11 with WSL2, hybrid
storage backend, 15 services registered, measured 2026-07-19. Per the project's
own rule, no performance number should be quoted without its conditions — and
note that the startup baselines currently in the docs were taken on different
operating systems and Go versions and have not been reconciled.)*

### The one place Overcast refuses work

Given "not a performance testing tool", it is worth being precise about the
exception. Lambda concurrency is refused in two forms:

- **Reserved concurrency**, set per function through the real AWS API, throttles
  immediately with AWS's exact error. This is faithful emulation of an AWS
  quota, and applications are written against it — retry policies, dead-letter
  queues, the "set it to zero to disable a function" idiom.
- **Instance limits** are *host protection*, not quota emulation. An invocation
  that cannot get a container reclaims an idle one, then queues, and only
  throttles if still queued when the function times out. If you hit this, raise
  `LAMBDA_MAX_INSTANCES` — it is not AWS behaviour and should not be treated as
  a signal about how your code would behave on AWS.

Asynchronous invocations are never throttled back to the caller: they were
already answered `202`, so a throttle is retried internally, as on AWS.

**Further reading:** [docs/performance.md](../performance.md) for tuning;
[docs/dev/performance.md](./performance.md) for the rules and benchmarking.

---

## 14. Where to go next

| Question | Document |
|---|---|
| How do I point my SDK at this? | [docs/sdk-cli.md](../sdk-cli.md) |
| What does this service support? | [docs/services/](../services/) |
| How do wire protocols get identified and dispatched? | [docs/dev/smithy.md](./smithy.md) |
| How do the storage backends actually work? | [docs/dev/storage-backends.md](./storage-backends.md) |
| Why can't my Lambda reach my database? | [docs/dev/container-networking.md](./container-networking.md) |
| Which host and port will a returned URL carry? | [docs/networking.md](../networking.md) |
| How do I test an endpoint change properly? | [docs/dev/manual-testing.md](./manual-testing.md) |
| How do I add a service or an endpoint? | [CONTRIBUTING.md](../../CONTRIBUTING.md#how-to-add-a-service) |
| What are the rules for agents working here? | [AGENTS.md](../../AGENTS.md) |
| How does the compat harness work? | [compat/AGENTS.md](../../compat/AGENTS.md) |
| What's implemented, and what's next? | [STATUS.md](../../STATUS.md) |

### A note on trusting these documents

An audit in August 2026 found meaningful drift across the documentation set:
stale counts, a wrong port, a migration guide listing implemented services as
missing, and several design documents whose status headers contradicted shipped
code. Corrections are tracked in the issue tracker.

The general lesson, worth carrying: **generated documentation in this
repository is reliable; hand-maintained enumerations are not.** Capability
tables, the service index and operation counts are generated from code and
checked by CI. A number typed into prose is a snapshot of the day someone typed
it. When this document and the code disagree, the code is right — and the
document is a bug.
