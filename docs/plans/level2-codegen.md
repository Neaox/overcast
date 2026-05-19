# Level 2 — Code Generation from Smithy Models

> Status: proposal. Owner: TBD.
> Depends on: [smithy.md](./smithy.md) Level 1 complete (✅).
> Related: [code-as-source-of-truth.md](./code-as-source-of-truth.md),
> [wire-byte-goldens.md](./wire-byte-goldens.md).

## 1. Problem

Today every Overcast service declares its operations **manually** — hand-written
Go structs for `In`/`Out` types, hand-written `Operations()` slices, and
hand-written `SupportedProtocols()` lists. When AWS ships a model update
(adds an operation, deprecates a field, announces `@rpcv2Cbor`), a human must:

1. Read the published Smithy model
2. Hand-transcribe the shape into Go structs with `json`/`cbor` tags
3. Add the operation to `typed_ops.go`
4. Update `SupportedProtocols()`
5. Update `docs/services/<svc>.md`

This is error-prone and doesn't scale. Overcast has 40+ services to maintain.

## 2. Goals

1. **Smithy model → Go code, in one `go generate` invocation.** A CLI tool
   reads the JSON AST of an AWS-published Smithy 2.0 model and emits:
   - `internal/services/<svc>/model_generated.go` — Input/Output structs
     with `json` and `cbor` tags
   - `internal/services/<svc>/manifest_generated.go` — `Operations()` slice
     with stub registrations for unimplemented ops
   - `internal/services/<svc>/protocols_generated.go` —
     `SupportedProtocols()` derived from the service's protocol traits
2. **Implemented operations stay in human-written files.** The generator
   emits stubs (501) for ALL operations, then an "override" map lets service
   authors replace stubs with real implementations one at a time.
3. **Regeneration is safe and reviewable.** Generated files are checked in.
   A CI job regenerates and diffs — if the diff is non-empty, a human
   reviews the AWS model change before merging the generated code.
4. **When AWS adds `@rpcv2Cbor` to a service, Overcast picks it up
   automatically on next regeneration.** No manual intervention needed.
5. **The stub-report tool (`cmd/stub-report`) drives the gap analysis.**
   Combined with the generator, it reports "declared, unimplemented" (stubs)
   vs "not declared, but in AWS model" (holes to fill).

## 3. Non-goals

- **Not a full Smithy IDL parser.** We consume only the **JSON AST**
  (pre-parsed Smithy model in JSON format) — no `.smithy` file parsing.
- **Not runtime code generation.** Structs are generated at build time and
  compiled in. No `reflect`-based dynamic dispatch.
- **Not a client generator.** Only server-side operation manifests and types.
- **Not replacing human-written business logic.** The generator produces
  stub registrations. Human `Handler` methods remain hand-written.

## 4. Input format — Smithy JSON AST

AWS publishes Smithy 2.0 models for every service as part of the
`aws-sdk-go-v2` release cycle. They live in:

```
aws-sdk-go-v2/codegen/sdk-codegen/aws-models/<svc>.json
```

Each file is a **Smithy JSON AST** document (per
[smithy.io/2.0/spec/json-ast.html](https://smithy.io/2.0/spec/json-ast.html)).
Example structure:

```json
{
  "smithy": "2.0",
  "metadata": {
    "suppressions": [...]
  },
  "shapes": {
    "com.amazonaws.sqs#CreateQueue": {
      "type": "operation",
      "input": { "target": "com.amazonaws.sqs#CreateQueueRequest" },
      "output": { "target": "com.amazonaws.sqs#CreateQueueResult" },
      "traits": {
        "aws.protocols#awsJson1_0": {},
        "smithy.api#http": { "method": "POST", "uri": "/", "code": 200 }
      }
    },
    "com.amazonaws.sqs#CreateQueueRequest": {
      "type": "structure",
      "members": {
        "QueueName": {
          "target": "smithy.api#String",
          "traits": { "smithy.api#xmlName": {} }
        },
        "Attributes": {
          "target": "com.amazonaws.sqs#QueueAttributeMap"
        }
      }
    }
  }
}
```

Key fields we consume:

| JSON AST path | What we extract |
|---|---|
| `shapes.* where type=operation` | Operation name, input/output targets |
| `shapes.* where type=structure` | Input/output/error structs, member names, member types |
| `shapes.* where type=service` | Service-wide protocol traits (`awsJson1_0`, `rpcv2Cbor`, `awsQuery`, etc.) |
| `traits.aws.protocols#awsJson1_0` | Service uses JSON 1.0 |
| `traits.aws.protocols#awsJson1_1` | Service uses JSON 1.1 |
| `traits.smithy.protocols#rpcv2Cbor` | Service supports CBOR |
| `traits.aws.protocols#awsQuery` | Service uses Query protocol |
| `traits.smithy.api#http` | Operation-level HTTP binding (method, URI, status code) |
| `traits.smithy.api#httpError` | Error HTTP status code |

## 5. Generator tool — `cmd/codegen`

### 5.1 CLI

```
go run ./cmd/codegen \
    -model aws-sdk-go-v2/codegen/sdk-codegen/aws-models/sqs.json \
    -service sqs \
    -out internal/services/sqs/
```

Flags:
- `-model` — path to Smithy JSON AST file
- `-service` — target Overcast service directory name
- `-out` — output directory for generated files (default: `internal/services/<service>/`)
- `-dry-run` — print generated code to stdout instead of writing files

### 5.2 Generated files

For each service, the generator writes three files:

#### `model_generated.go`

```go
// Code generated by cmd/codegen. DO NOT EDIT.
package sqs

// CreateQueueInput is the input for CreateQueue.
type CreateQueueInput struct {
    QueueName  string            `json:"QueueName" cbor:"QueueName"`
    Attributes map[string]string `json:"Attributes,omitempty" cbor:"Attributes,omitempty"`
    Tags       map[string]string `json:"tags,omitempty" cbor:"tags,omitempty"`
}

// CreateQueueOutput is the output for CreateQueue.
type CreateQueueOutput struct {
    QueueUrl string `json:"QueueUrl" cbor:"QueueUrl"`
}

// ... one pair per operation ...
```

Rules:
- Name conversion: `CreateQueue` → `CreateQueueInput` / `CreateQueueOutput`
- `omitempty` on optional members (not marked `@required`)
- Member types use Go `map[string]string` for Smithy `map<String, String>`
- Nested structs emitted as named types if referenced by multiple operations
- `@httpHeader`, `@httpLabel`, `@httpQuery` members get `header:"..."` / `uri:"..."` / `query:"..."` struct tags
- Timestamps use `time.Time` (CBOR codec handles this natively; JSON codec uses `epoch-seconds`)
- Blobs use `[]byte`
- Documents use `any`

#### `manifest_generated.go`

```go
// Code generated by cmd/codegen. DO NOT EDIT.
package sqs

import "github.com/Neaox/overcast/internal/protocol/op"

// GeneratedOps returns all AWS operations for this service.
// Unimplemented operations are registered as stubs returning 501.
// Override entries in hand-written typed_ops.go take precedence via
// a merger in typed_ops.go.
func GeneratedOps() map[string]op.Operation {
    return map[string]op.Operation{
        "CreateQueue":        op.NewTyped[CreateQueueInput, CreateQueueOutput]("CreateQueue", nil),
        "GetQueueUrl":        op.NewTyped[GetQueueUrlInput, GetQueueUrlOutput]("GetQueueUrl", nil),
        "DeleteQueue":        op.NewTyped[DeleteQueueInput, struct{}]("DeleteQueue", nil),
        "AddPermission":      op.NewTyped[AddPermissionInput, struct{}]("AddPermission", nil),
        // ... all operations from the model ...
    }
}
```

When `Fn` is `nil`, the typed dispatcher returns `501 NotImplemented`.
When a handler is implemented, the hand-written `typed_ops.go` registration
(with a real `Fn`) is merged in and takes precedence.

#### `protocols_generated.go`

```go
// Code generated by cmd/codegen. DO NOT EDIT.
package sqs

import "github.com/Neaox/overcast/internal/protocol/codec"

// GeneratedProtocols returns the wire protocols advertised by this
// service's AWS Smithy model.
func GeneratedProtocols() []codec.Codec {
    return []codec.Codec{
        codec.JSON10,    // from aws.protocols#awsJson1_0
        codec.JSON11,    // (emulator: also accept 1.1)
        codec.RPCv2CBOR,  // from smithy.protocols#rpcv2Cbor
        codec.QueryXML,   // from aws.protocols#awsQuery
    }
}
```

### 5.3 Merging with hand-written code

In `typed_ops.go`, the service's final operation list merges generated stubs
with hand-written overrides:

```go
func (h *Handler) typedOps() map[string]op.Operation {
    // Start from generated base (all AWS ops as stubs)
    ops := GeneratedOps()

    // Override implemented operations with real handlers
    ops["CreateQueue"] = op.NewTyped[CreateQueueInput, CreateQueueOutput](
        "CreateQueue", h.createQueueTyped,
    )
    ops["GetQueueUrl"] = op.NewTyped[GetQueueUrlInput, GetQueueUrlOutput](
        "GetQueueUrl", h.getQueueUrlTyped,
    )
    // ... only implemented ops override ...

    return ops
}
```

Similarly for protocols:

```go
func (s *Service) SupportedProtocols() []codec.Codec {
    return GeneratedProtocols() // auto-updates when model adds @rpcv2Cbor
}
```

### 5.4 Smithy type → Go type mapping

| Smithy type | Go type | Notes |
|---|---|---|
| `blob` | `[]byte` | |
| `boolean` | `bool` | |
| `byte` | `int8` | |
| `short` | `int16` | |
| `integer` | `int` | |
| `long` | `int64` | |
| `float` | `float32` | NaN/Inf as strings per protocol |
| `double` | `float64` | NaN/Inf as strings per protocol |
| `string` | `string` | |
| `timestamp` | `time.Time` | |
| `document` | `any` | |
| `list<T>` | `[]T` | |
| `set<T>` | `[]T` | (unique values enforced at runtime) |
| `map<K,V>` | `map[K]V` | K must be string |
| `structure` | named `struct` | |
| `union` | named `struct` with one non-nil field | |
| `enum` | `string` | (constraint trait for validation) |
| `@required` | no `omitempty` | Go zero-value for missing fields |

### 5.5 HTTP binding traits

For REST-JSON services (Phase 5 #23-25), the generator also emits HTTP binding
metadata. These are `http.Handler`-level concerns that don't affect typed
dispatch, so they're emitted as comments or constants:

```go
// Operation "GetFoo" has HTTP binding: GET /applications/{applicationId}
//   applicationId → uri label → input field ApplicationId
```

## 6. Integration with existing tooling

### 6.1 `cmd/stub-report`

The Phase 7 stub-report tool (`cmd/stub-report`) compares the generated
operation set against the currently-registered set. Its output changes from
"what's in typed_ops.go" to:

```
gap analysis for sqs:
  generated (model):  25 ops
  implemented:        14 ops
  stubs (501):        11 ops
  missing from model: 0 ops  (all model ops have at least stub registration)
```

### 6.2 CI regeneration check

```yaml
# .github/workflows/codegen.yml
- name: Check generated code is up to date
  run: |
    go run ./cmd/codegen -all
    git diff --exit-code -- 'internal/services/*/model_generated.go' \
                              'internal/services/*/manifest_generated.go' \
                              'internal/services/*/protocols_generated.go'
```

If the diff is non-empty, the CI fails — a human must review the Regeneration
PR that adds/removes operations, changes types, or updates protocols.

### 6.3 docs/services update

The generator can also emit a partial docs table by reading the model's
`@documentation` trait and mapping operations to their implementation status.
Combined with the stub-report gap analysis, this drives automatic updates
to `docs/services/<svc>.md`.

## 7. Phased rollout

### Phase L2.0 — model ingestion (1 PR)

- [ ] Add `cmd/codegen` CLI skeleton with `-model` / `-service` / `-out` flags
- [ ] Implement Smithy JSON AST parser: operation discovery, structure member
      extraction, protocol trait detection
- [ ] Implement type mapper: Smithy → Go with json/cbor struct tags
- [ ] Generate `model_generated.go` for **one** service (SQS) as proof-of-concept
- [ ] Verify generated types match existing hand-written `typed_logic.go` types

### Phase L2.1 — stub manifest + protocol generation (1 PR)

- [ ] Generate `manifest_generated.go` with full operation → stub registration
- [ ] Generate `protocols_generated.go` from service protocol traits
- [ ] Wire SQS's `typed_ops.go` and `SupportedProtocols()` to use generated base
- [ ] Hand-written overrides merge with generated stubs
- [ ] All SQS tests pass with generated types

### Phase L2.2 — bulk generation (1 PR)

- [ ] Run codegen for all 40+ services
- [ ] Drop hand-written type definitions in favor of generated ones
- [ ] Migrate `SupportedProtocols()` to generated
- [ ] All integration tests pass
- [ ] CI regeneration check added

### Phase L2.3 — docs automation (1 PR)

- [ ] Codegen emits `capabilities_generated.go` matching existing capabilities
- [ ] `cmd/stub-report` updated to compare generated vs. implemented

### Phase L2.4 — continuous regeneration (ongoing)

- [ ] CI job tracks model changes in aws-sdk-go-v2 dependency bumps
- [ ] Regeneration creates auto-PR when new model operations appear
- [ ] `@rpcv2Cbor` additions auto-propagate to `SupportedProtocols()`

## 8. Risks & mitigations

| Risk | Mitigation |
|------|-----------|
| Model changes break existing types | Generated types are in a separate file; human-written overrides in typed_ops.go take precedence. Tests catch regression. |
| Generated types add compile-time cost | Go's incremental build caches unchanged packages. Generated files only cause recompilation when they change. |
| AWS model format drifts | CI regeneration check catches format changes immediately. Pinned dependency on aws-sdk-go-v2 version. |
| Hand-written and generated types diverge | The merge pattern (generated stubs overridden) is one-way: human code always wins for implemented ops. Stubs stay in sync with model. |
| Nested type explosion | Flatten deeply-nested shapes into named Go types referenced by multiple operations where possible. |
| Too many stubs at first | Acceptable — stubs are 501 NotImplemented, which is the current behavior. Implementation is incremental. |

## 9. Dependencies

- **aws-sdk-go-v2** — already a dev dependency (used in compat suites). Models
  live at `aws-sdk-go-v2/codegen/sdk-codegen/aws-models/`.
- **Smithy JSON AST spec** — [smithy.io/2.0/spec/json-ast.html](https://smithy.io/2.0/spec/json-ast.html).
  Single page, well-defined, stable.
- **Smithy 2.0 model reference** — [smithy.io/2.0/spec/model.html](https://smithy.io/2.0/spec/model.html).
  Understanding of shapes, traits, and service closures.

## 10. Acceptance criteria (Level 2 complete)

- [ ] `cmd/codegen` tool reads a Smithy JSON AST and emits valid Go that compiles
- [ ] All 40+ services have generated `model_generated.go`,
      `manifest_generated.go`, and `protocols_generated.go`
- [ ] Hand-written `typed_ops.go` merges with generated stubs; all tests pass
- [ ] `SupportedProtocols()` is auto-derived from service protocol traits
- [ ] CI regeneration check gates model→code mismatch
- [ ] When a model adds `@rpcv2Cbor`, the regeneration auto-adds it to
      `SupportedProtocols()` — no manual intervention
- [ ] `cmd/stub-report` gap analysis uses generated operation set as baseline
