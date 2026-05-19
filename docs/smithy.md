# Wire Protocols

> Describes the Smithy-aligned wire protocols Overcast supports, how they are
> detected, and how they are added to a service.

Overcast implements the same **wire protocol identification and dispatching**
that AWS SDKs use — the server inspects HTTP headers to determine which
protocol a request uses, then delegates to the matching codec. This mirrors
the Smithy [wire-protocol-selection
guide](https://smithy.io/2.0/guides/wire-protocol-selection.html).

---

## Supported protocols

| Protocol | Smithy shape ID | Content-Type / marker | Response format | Services |
|----------|----------------|----------------------|-----------------|----------|
| **AWS JSON 1.0** | `aws.protocols#awsJson1_0` | `application/x-amz-json-1.0` + `X-Amz-Target` | JSON | DynamoDB, SQS, Step Functions, DynamoDB Streams |
| **AWS JSON 1.1** | `aws.protocols#awsJson1_1` | `application/x-amz-json-1.1` + `X-Amz-Target` | JSON | Most services (Kinesis, CW Logs, EventBridge, SSM, KMS, ECS, ECR, Cognito, etc.) |
| **Smithy RPC v2 CBOR** | `smithy.protocols#rpcv2Cbor` | `application/cbor` + `Smithy-Protocol: rpc-v2-cbor` + `/service/{S}/operation/{Op}` | CBOR | All Phase 5 JSON services |
| **AWS Query** | `aws.protocols#awsQuery` | `application/x-www-form-urlencoded` + `Action=` | XML | STS, SNS, IAM, EC2, CloudFormation, RDS, SES, etc. |
| **AWS REST XML** | `aws.protocols#restXml` | HTTP method + path (S3 virtual-host routing) | XML | S3, CloudFront |
| **AWS REST JSON 1** | `aws.protocols#restJson1` | HTTP method + path | JSON | API Gateway, Lambda control plane |

The emulator also accepts both JSON 1.0 and 1.1 for any JSON-tier service
regardless of which variant the real AWS service uses — they are
functionally identical in the emulator.

---

## Protocol identification (precision order)

When a request arrives, the protocol middleware walks identifier rules in
**precision order** (most specific first). The first match wins.

| Priority | Protocol | Identification rule |
|----------|----------|-------------------|
| 1 (highest) | `rpcv2Cbor` | `Smithy-Protocol: rpc-v2-cbor` header AND path `/service/{S}/operation/{Op}` |
| 2 | `awsJson1_0` | `Content-Type: application/x-amz-json-1.0` AND `X-Amz-Target` header |
| 3 | `awsJson1_1` | `Content-Type: application/x-amz-json-1.1` AND `X-Amz-Target` header |
| 4 (lowest) | `awsQuery` | `Content-Type: application/x-www-form-urlencoded` AND `Action=` param |

REST XML and REST JSON 1 are **not** in the identifier walk — they are dispatched
by chi route matching (S3's wildcard, API Gateway's path-based routing).

This ordering matches the Smithy spec's [AWS service protocol
precision](https://smithy.io/2.0/guides/wire-protocol-selection.html#aws-service-protocol-precision).

---

## How a request flows through the dispatcher

```
Request arrives
    │
    ▼
middleware.Protocol (precision-ordered identifier walk)
    │
    ├── Match found → stash (codec, operation) in request context
    │
    ▼
Service.Dispatch (or DispatchQuery)
    │
    ├── codec.FromContext(ctx) != nil ?
    │   ├── Yes → supportsCodec?
    │   │   ├── Yes → typedOp[op].Invoke(w, r, codec)   ← typed path (CBOR, etc.)
    │   │   └── No  → 415 UnsupportedProtocol
    │   └── No  → legacy dispatch (form-parse, switch/map)  ← existing path (JSON 1.x, Query)
    │
    ▼
Response (JSON, XML, or CBOR)
```

The legacy dispatch path handles JSON 1.0, JSON 1.1, and Query requests using
the existing handler code. CBOR requests go through the **typed dispatcher**
via `codec.FromContext`. This dual-path design keeps every existing client
working while adding CBOR support.

---

## Codec interface

Every protocol is implemented as a `codec.Codec`:

```go
type Codec interface {
    Name() string                                           // e.g. "aws.protocols#awsJson1_0"
    Decode(r *http.Request, into any) *protocol.AWSError    // body → typed struct
    WriteResponse(w, r, status int, v any)                  // typed struct → body
    WriteError(w, r, e *protocol.AWSError)                  // error → body
}
```

Codecs are **pure (de)serialisers** — they never inspect the operation, never
know which service is on the other side, and never touch business logic.

## Typed operation interface

Service handlers are converted to codec-agnostic functions:

```go
type Operation interface {
    Name() string
    Invoke(w http.ResponseWriter, r *http.Request, codec Codec)
}
```

A generic `Typed[In, Out]` wrapper handles decode/encode:

```go
func (t Typed[In, Out]) Invoke(w, r, codec) {
    var in In
    codec.Decode(r, &in)          // codec handles wire → struct
    out, err := t.Fn(ctx, &in)    // business logic (codec-agnostic)
    codec.WriteResponse(w, r, 200, out) // struct → wire
}
```

Adding a new wire protocol means writing **one** codec (80–150 LOC) and
adding **one** identifier rule. All opted-in services get it for free.

## Adding a new protocol

1. Implement `codec.Codec` in `internal/protocol/codec/` (e.g. `newproto.go`)
2. Add an `Identifier` that claims matching requests
3. Insert the identifier in `DefaultIdentifiers()` at the correct precision
   position
4. Services opt in by adding the codec to `SupportedProtocols()`

No handler changes required — the typed dispatcher handles the rest.

---

## Typed handler pattern

Every migrated operation follows this signature. The handler receives decoded
request structs and returns typed responses — it never touches HTTP:

```go
// Before (legacy — HTTP-coupled)
func (h *Handler) CreateQueue(w http.ResponseWriter, r *http.Request) {
    var req createQueueRequest
    serviceutil.DecodeJSON(w, r, &req)  // codec choice
    // ... business logic ...
    protocol.WriteJSON(w, r, 200, &resp) // codec choice
}

// After (typed — codec-agnostic)
func (h *Handler) createQueueTyped(ctx context.Context, req *CreateQueueInput) (*CreateQueueOutput, *protocol.AWSError) {
    // ... same business logic ...
    return &CreateQueueOutput{QueueUrl: url}, nil
}
```

---

## Service manifest

Every service declares its operations and supported protocols via the
`router.ProtocolService` interface:

```go
type ProtocolService interface {
    Operations() []op.Operation          // all registered operations
    SupportedProtocols() []codec.Codec   // e.g. {JSON10, JSON11, RPCv2CBOR}
}
```

The `cmd/stub-report` tool walks all services and produces the complete
operation manifest at `docs/operation-manifest.md` — 795 operations across
42 services.

---

## Reference

| Document | Purpose |
|----------|---------|
| [plans/smithy.md](plans/smithy.md) | Full architecture plan (phases, services, status) |
| [plans/wire-byte-goldens.md](plans/wire-byte-goldens.md) | Golden test infrastructure |
| [plans/level2-codegen.md](plans/level2-codegen.md) | Code generation from Smithy models |
| [perf-baselines/README.md](perf-baselines/README.md) | Performance budgets and baselines |
| [Smithy wire protocol selection](https://smithy.io/2.0/guides/wire-protocol-selection.html) | Upstream spec this architecture follows |
| [Smithy RPC v2 CBOR](https://smithy.io/2.0/additional-specs/protocols/smithy-rpc-v2.html) | CBOR protocol spec |
| [AWS JSON 1.0](https://smithy.io/2.0/aws/protocols/aws-json-1_0-protocol.html) / [1.1](https://smithy.io/2.0/aws/protocols/aws-json-1_1-protocol.html) | JSON protocol specs |
| [AWS Query](https://smithy.io/2.0/aws/protocols/aws-query-protocol.html) | Query protocol spec |
| [AWS restJson1](https://smithy.io/2.0/aws/protocols/aws-restjson1-protocol.html) / [restXml](https://smithy.io/2.0/aws/protocols/aws-restxml-protocol.html) | REST protocol specs |
