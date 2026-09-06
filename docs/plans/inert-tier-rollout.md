# Inert-tier rollout — mass-producing Tier 1 across the AWS surface

> Status: proposal, 2026-08-03. Owner: TBD.
> Re-verified 2026-08-23: **Phases I0, I1 and I2 have landed** (issue #1114) —
> `internal/inert/conformance` turns §3 into executable, table-driven
> conformance tests, run against a deliberately naive stub to prove the suite
> bites; `models/aws/shapes/` holds the pruned shape snapshot with its
> `shapes-sha256`, offline check and size budget; and `internal/inert` is the
> shared runtime, hand-wired to `organizations` (whose policy resource took it
> from 1 to 9 `StatusInert` rows). See the phase table in §8, §4.6's
> measurement and §4.3's as-shipped API. Phases I3 onward are still not
> started — there are no `-inert-*` flags in `cmd/awsmodelgen` and none of the
> pilot services (servicediscovery, ELB Classic, batch) exists.
>
> **Two premises I3 rests on failed re-measurement and need settling before it
> starts.** §3.1's authoritative classifier: only 121 of 426 modeled services
> declare Smithy `resource` shapes at all, and none of the four snapshot
> services do, so the name-prefix fallback is the *only* path for the pilot
> and for ~72% of the fleet (#1369). §3.3's error selection: `batch` declares
> exactly two error shapes (`ClientException`/`ServerException`) across all 45
> operations so every selector finds nothing, no snapshot service models an
> invalid-token error at all, and the invalid-parameter pattern misses ELB's
> `InvalidConfigurationRequestException` outright (#1373). §2.2's census is
> re-derived as of this commit: capabilities now read 1,258 Supported / 153
> Unsupported / 28 Inert / 14 Partial / 0 WIP across 1,453 rows.
> `internal/capabilities/all.gen.go` is authoritative; re-derive §2.2 before
> budgeting any wave — the numbers drift every time a service graduates.
> Siblings: [compat coverage modelgen](./compat-coverage-modelgen.md) (generated compat groups),
> [services never emulated](./services-never-emulated.md) (the scope boundary this plan obeys),
> [full emulation priority](./full-emulation-priority.md) (what graduates to Tier 2 and in what order).
> Depends on: [Level 2 codegen](./level2-codegen.md) Track 3 (this plan is its mass-production consumer),
> [AWS API operation coverage](./aws-api-operation-coverage.md) (the manifest, registry, and model-refresh machinery).
> Safety nets: [wire-byte goldens](./wire-byte-goldens.md), [pagination plan](./pagination-plan.md),
> startup-metrics honesty (shipped in #252; methodology now lives in
> [docs/dev/performance.md](../dev/performance.md)).

## 0. Tier vocabulary

Shared across this plan and its siblings:

| Tier | Name | Meaning |
| --- | --- | --- |
| **Tier 0** | protocol-correct 501 | The operation is owned by the generated registry and returns the right `NotImplemented` envelope for its protocol family. No state, no shape. Delivered by [aws-api-operation-coverage.md](./aws-api-operation-coverage.md) A2–A4. |
| **Tier 1** | **inert** (this plan) | The operation is accepted, returns the correct status code and a protocol-correct response **shape**, and **all request metadata is stored and returned faithfully** — CRUD, tagging, pagination, ARNs, timestamps, not-found/conflict errors. Nothing beyond that: no data plane, no side effects, no cross-resource behaviour. `cdk deploy`/`cdk destroy` of a stack using the service succeeds end to end. |
| **Tier 2** | full emulation | The service actually *does* the thing (Lambda invokes, SQS delivers, DynamoDB queries). Prioritised in [full-emulation-priority.md](./full-emulation-priority.md). |

Tier 1 is a **memory**, not a **simulation**. The test of whether something belongs
in Tier 1 is: *can the response be derived purely from what the caller previously
told us, plus deterministic derivations (IDs, ARNs, timestamps, defaults from the
model)?* If yes, it is inert. If it requires inventing data the caller never
supplied (log lines, metric values, query results, invocation output), it is Tier 2
and stays at Tier 0 until then.

## 1. Objective

Bring every in-scope AWS service to Tier 1 quickly, cleanly, and reliably —
where "in-scope" is *every modeled service not on the never-emulated list*.

The measurable objective, per service:

1. No in-scope operation returns 501.
2. Every response body is shape-correct for the service's protocol family, verified
   by a real AWS SDK (compat suites) rather than by our own assertions.
3. Round-tripping is faithful: what `Create*` accepted, `Get*`/`Describe*`/`List*`
   return — including optional and nested fields, tags, and ARNs.
4. `cdk deploy` then `cdk destroy` of a representative stack using the service is
   green, with `Ref` and `Fn::GetAtt` resolving to real values.
5. Zero regressions in the compat baseline (currently zero failures — see
   [compat-baseline-and-uniformity.md](./compat-baseline-and-uniformity.md)).

The economic objective: **a service reaches Tier 1 for a small, reviewable,
mostly-generated diff plus one hand-written "seasoning" file**, not for the
~900–2400 lines it costs today (§2.4).

## 2. Current state (verified 2026-08-03)

> **STATUS.md is stale.** Its prose tables were checked against the code and the
> capgen-generated registry during this plan's research and disagree in several
> places. Corrections are inline below; STATUS.md should be fixed as a rider on
> Phase I0. The auto-generated op-count block at the bottom of STATUS.md
> (`<!-- BEGIN overcast:status -->`) *is* current.

### 2.1 The model universe

[`internal/awsapi/manifest.gen.go`](../../internal/awsapi/manifest.gen.go) is
**50,386 lines / 8.86 MB** of generated routing metadata covering **426 services
and 18,850 operations**, pinned at `models/aws/VERSION` revision
`66e973ca…`, model date 2026-07-27.

Protocol split across all 18,850 modeled ops: restJson1 9,562 · awsJson1_1 5,409 ·
awsJson1_0 1,524 · awsQuery 1,118 · ec2Query 772 · restXml 447 · rpcv2Cbor 18.

Subtracting the 52 modeled identities Overcast registers today (they collapse to
50 service keys via the alias table at
[internal/awsapi/registry_data.go:71-84](../../internal/awsapi/registry_data.go))
leaves **374 unimplemented identities / 14,410 operations**, dominated by
REST-JSON, then awsJson1_1 and awsJson1_0. The tail is long and
cheap: 46 of those services have ≤5 operations (144 ops total). The head is
expensive and mostly *not* CDK-relevant: sagemaker 403, connect 379, iot 272,
quicksight 269, datazone 190, lightsail 161, deadline 126, medialive 123,
pinpoint 122, gamelift 120. **Whether those are in scope is decided by
[services-never-emulated.md](./services-never-emulated.md), not here** — this plan
scopes to whatever that document leaves in. That document currently never-lists
**95** services and defers **24** third-party-bridge services (Connect, Chime,
Pinpoint, AppFlow, … — mockable in principle, bottom of the queue by owner
decision 2026-08-03), leaving **255 "inert-candidate" services**
(426 − 95 − 24 − 52) as this plan's default scope. Note the remaining
heavyweights (sagemaker, iot, quicksight, datazone …) are inert-*candidates*
under that rubric, not never-listed — what keeps them out of wave budgets is
wave selection (§6.5), not the scope boundary. Every raw figure quoted below is
the pre-exclusion 374/14,410 number unless stated otherwise; budget each wave
from the audited scope lists, never from the raw totals.

### 2.2 What "implemented" means today

> Re-derived 2026-08-23 for Phase I0 (this plan's own acceptance gate requires
> re-deriving this section from the code, not copying the previous draft — the
> previous numbers here (1,318 rows; 1,116/154/48/0/0) were themselves already
> stale twice over by the time I0 landed). Reproduce with:
> `go run -tags dev ./cmd/capgen --generate` (writes `all.gen.go`, prints the
> row count), then `grep -c 'Status<X>' internal/capabilities/all.gen.go` per
> status. `internal/capabilities/all.gen.go` is authoritative; re-derive this
> section again before budgeting any later wave, the same way.

[`internal/capabilities/all.gen.go`](../../internal/capabilities/all.gen.go)
declares **1,461 capability rows across 50 services**:
1,258 `StatusSupported`, 153 `StatusUnsupported`, 36 `StatusInert`,
14 `StatusPartial`, and **zero** `StatusWIP`. (Phase I2 moved this: it added
`organizations`' 8 policy operations, so `StatusInert` went 28 → 36 and the
row total 1,453 → 1,461. Every later phase will move it again — re-derive
rather than quoting this paragraph.)

The `StatusInert` rows are services that are *already entirely Tier 1*:
`transfer` (13), `cloudtrail` (12), `organizations` (9), `bedrock` (2). These,
not Route 53, are the closest existing analogues to what this plan
mass-produces — Route 53 is a *supported* service (see below, it now serves
real DNS). `backup` left the wholly-inert set (#815/#904 made it a real REST
implementation): its 6 remaining metadata-only rows are `StatusPartial`, not
`StatusInert` — the first real use of that status, alongside one row each in
`iam`/`kms`/`sns` and 5 in `s3`. `autoscaling` left the inert list earlier
still (#474; its 25 rows are `StatusSupported`). None of these promotions
change this plan's scope — they only move rows between statuses this plan
already accounted for.

Services carrying `StatusUnsupported` rows — i.e. the **inert-backfill** targets
inside already-implemented services (§7), re-derived the same way (`grep`
`Status: StatusSupported` / `StatusUnsupported` per service):

| Service | Supported | Unsupported |
| --- | --- | --- |
| cloudformation | 24 | 28 |
| ses | 27 | 18 |
| msk | 17 | 13 |
| eventbridge | 18 | 11 |
| rds | 24 | 10 |
| ecs, s3 | 40 each | 8 each |
| sns | 21 | 8 |
| dynamodb, ssm | 21 / 11 | 7 each |
| route53 | 19 | 6 |
| sts | 5 | 6 |
| cloudwatch-logs | 18 | 4 |
| efs, elbv2, secretsmanager | 28 / 18 / 19 | 3 each |
| apigateway, cloudwatch, elasticache, lambda, sqs | 104 / 15 / 22 / 57 / 19 | 2 each |

> STATUS.md corrections re-checked for I0: STATUS.md's prose (`STATUS.md`
> lines outside the `<!-- BEGIN overcast:status -->` block) already states
> Secrets Manager at **22** ops (19 `StatusSupported` + 3 `StatusUnsupported`,
> matching the registry) and Shield at **8** ops, all `StatusSupported` with
> real persisted state
> ([internal/services/shield/typed_logic.go](../../internal/services/shield/typed_logic.go))
> — both of this plan's originally-cited corrections had already been applied
> by the time I0 started; there was nothing left to fix. Route 53's STATUS.md
> line no longer says "inert (no DNS served)" — it documents that Overcast's
> resolver now answers real DNS queries from a zone's records (#1189), and its
> 19/6 Supported/Unsupported split above is what a `StatusInert` write-up would
> have described instead. The one real drift found: STATUS.md's CloudFormation
> row cited "132 resource types"; `resourceHandlers` in
> [provisioner.go](../../internal/services/cloudformation/provisioner.go) has
> **136** (counted directly from the map literal) — `docs/cdk.md` already said
> 136, only STATUS.md's prose was behind. Fixed in the same commit as this
> section.

### 2.3 The machinery this plan extends

- **Manifest generator.** [`cmd/awsmodelgen`](../../cmd/awsmodelgen) (1,494 lines)
  loads the Smithy JSON AST from a pinned local checkout, verifies the checkout's
  revision against `models/aws/VERSION`, and emits `manifest.gen.go` plus the
  router's immutable target/Query/REST-trie/RPC indexes. Its `shape` struct
  (`main.go:110-123`) **already parses Smithy `resource` shapes and their
  `create`/`put`/`read`/`update`/`delete`/`list` lifecycle bindings**
  (`main.go:684-703`) — it walks them to enumerate operations and then discards
  the binding information. That discarded data is exactly what drives generated
  CRUD (§4.1).
- **Typed dispatch.** [`internal/protocol/op`](../../internal/protocol/op/op.go)'s
  `Typed[In,Out]` is monomorphised per operation — decode, call, encode, no
  reflection on the hot path. [`internal/protocol/codec`](../../internal/protocol/codec)
  supplies JSON10/JSON11/QueryXML/RESTXML/RPCv2CBOR/RPCv2JSON with claim-first
  identification. Query dispatch is live since Level-2 P1.
- **State.** [`internal/state.Store`](../../internal/state/store.go) — a
  namespaced KV with `Get/Set/Delete/List/Scan/ScanPage`. Every service persists
  JSON-marshalled records into `"<svc>:<collection>"` namespaces. There is **no**
  generic resource store today; each service hand-rolls put/get/list/delete plus
  malformed-record skipping (see
  [route53/store.go](../../internal/services/route53/store.go), 362 lines of
  exactly that).
- **Shared helpers.** `serviceutil.Paginate` (opaque base64 index tokens,
  `ErrInvalidPageToken`), `serviceutil.ResourceName`/`NameRule` validation,
  `protocol.ARN` + ~15 per-service ARN builders, `protocol.Write{JSON,XML,QueryXML,EC2QueryXML}Error`,
  `protocol.NotImplemented{JSON,XML,QueryXML,EC2QueryXML}`.
- **Capabilities.** `capabilities_dev.go` per service, `//go:build dev`, rolled up
  by `cmd/capgen` into `all.gen.go` and into STATUS.md's generated block.

### 2.4 What Tier 1 costs by hand today

Line counts, verified:

| Service | Ops | Lines | Protocol | Notes |
| --- | --- | --- | --- | --- |
| organizations | 1 | 226 | JSON 1.1 | floor cost of *any* service |
| shield | 5 | 470 | JSON 1.1 | smallest complete typed-only service |
| backup | 9 | 862 | JSON 1.1 | fully `StatusInert` |
| transfer | 10 | 920 | JSON 1.1 | fully `StatusInert` |
| cloudtrail | 9 | 1,044 | JSON 1.1 | fully `StatusInert` |
| autoscaling | 19 | 1,922 | Query | was fully `StatusInert`; promoted out of the inert tier by #474 |
| **route53** | 25 | **2,373** | REST-XML | "inert done well", by hand |

That is **~90–100 lines per operation**, consistently, across protocol families.
At that rate the remaining 14,410 operations are ~1.4M lines — obviously not a
hand-written project.

Worse, hand-written inert services are **not faithful**. `transfer`'s
`createServerRequest` declares 2 fields (`EndpointType`, `IdentityProviderType`)
where the model declares ~15; everything else a caller sends is silently
discarded and absent from `DescribeServer`
([transfer/typed_logic.go:14-20](../../internal/services/transfer/typed_logic.go)).
This is the single most important thing generation fixes: **the model knows every
field, and a human writing 90 lines per op does not type them all in.**

### 2.5 CloudFormation / CDK today

[`internal/services/cloudformation/provisioner.go`](../../internal/services/cloudformation/provisioner.go)
(3,296 lines, plus ~7,700 more across `provisioner_*.go`) drives an async
provisioner that dispatches **internal HTTP requests through the emulator's own
router**, so each CFN resource is created via its service's real API — no direct
coupling. `resourceHandlers` (`provisioner.go:1646`) maps **132 CloudFormation
resource types** (STATUS.md says "~55" — stale) to handlers implementing:

```go
type resourceHandler interface {
    Create(ctx, router, cfg, props, rCtx) (physicalID string, attrs map[string]string, err error)
    Delete(ctx, router, cfg, physicalID, rCtx) error
}
type resourceUpdater interface { // optional
    Update(ctx, router, cfg, physicalID, props, oldProps, rCtx) (newPhysicalID string, attrs map[string]string, err error)
}
```

11 of the 132 use `stubResourceHandler` (fake physical ID, no-op delete). The rest
are hand-written and **strikingly mechanical**: read a few `props`, build a request
body map, `internalJSON`/`internalXML` to a target, unmarshal, derive an ARN as
fallback, return `(physicalID, attrs)`. `backupBackupVaultHandler` and
`acmCertificateHandler` ([provisioner_json_coverage.go:15-95, 328-395](../../internal/services/cloudformation/provisioner_json_coverage.go))
are representative at ~60–100 lines per resource type including `Update`.

**Critical behaviour to know:** an unrecognised resource type does **not** fail
the stack — `provisionResource` logs a warning and returns a synthetic
`"<stack>-<logicalId>-stub"` physical ID (`provisioner.go:630-640`). So a CDK
deploy touching an unmodeled service *appears* to succeed while `Ref` yields a
fake string and `Fn::GetAtt` yields nothing. **"CDK works end to end" is therefore
not free at Tier 1** — it is a distinct deliverable: every in-scope service's CFN
resource types must be registered with real create/update/delete and real
ref/GetAtt attributes, or the deploy is a lie that fails at the first `GetAtt`.

The CDK compat suite ([compat/suites/cdk](../../compat/suites/cdk)) exists and
runs a single multi-resource `CdkCompatStack` with one `lifecycle` group. It has
no per-service stack decomposition yet; this plan needs one (§5.3).

## 3. The inert contract

This section is normative. A generated operation that cannot satisfy its class's
contract must not be generated — it stays Tier 0 and is recorded as an explicit
exclusion.

### 3.1 Operation classes

Classification is derived from the Smithy `resource` lifecycle bindings first
(authoritative), falling back to a name-prefix heuristic that must be
**materialised as reviewed data**, never inferred at runtime.

| Class | Smithy binding | Semantics |
| --- | --- | --- |
| **Create** | `create` / `put` | Validate required fields. Derive ID + ARN. Stamp `CreationTime`. Persist the **entire** decoded input plus derivations. Return the modeled output projection. Duplicate identifier → the service's modeled *already-exists* error. |
| **Read** | `read` | Load by identifier; project into the modeled output. Missing → the service's modeled *not-found* error (§3.3). |
| **Update** | `update` | Load, merge non-nil input fields over the stored record, refresh `LastModifiedTime`, persist, return the modeled output. Absent → not-found. Create-only fields (per the CFN schema's `createOnlyProperties`, §5.2) are rejected with the modeled *invalid-parameter* error. |
| **Delete** | `delete` | Load, delete, return the modeled (usually empty) output. Absent → not-found. Idempotent-delete services (per model docs) return success instead; that is a per-service seasoning override, defaulting to not-found. |
| **List** | `list` / `collectionOperations` | Scan the collection namespace, stable-sort by identifier, paginate via `serviceutil.Paginate` with the operation's modeled token/limit member names. |
| **Tag** | `TagResource`/`UntagResource`/`ListTagsForResource` and per-service spellings | Tags live in one shared tag namespace keyed by ARN. Create/Update inputs carrying a `Tags` member write through to the same store, so `ListTagsForResource` and `Describe*.Tags` never disagree. |
| **Verb** | none | See §3.6. |

### 3.2 Fidelity: store everything, invent nothing

The persisted record is the **generated input struct for the create operation,
unioned with every field the update operations can set**, plus generated
derivations (`Id`, `Arn`, `CreationTime`, `LastModifiedTime`, `Status`). Responses
are a **projection** of that record onto the modeled output struct, field by
generated field. Consequences:

- A field the caller sent that the output shape does not carry is stored but not
  returned — matching AWS.
- A field the output shape carries that the caller never sent is emitted as the
  model's `@default`, or omitted when the member is optional. **Never fabricated.**
- Enum-typed status fields are set to the model's steady-state value where the
  model or AWS docs make it unambiguous (`ACTIVE`, `AVAILABLE`, `ENABLED`); where
  ambiguous, the generator emits it into the seasoning file's TODO block rather
  than guessing. Guessing a status is the fastest way to break a CDK waiter.

### 3.3 Errors: generated, not guessed

Smithy declares each operation's `errors: [...]` with `@error`, `@httpError`, and
the member shape. The generator selects, per resource:

- **not-found**: the operation's declared error whose shape name matches
  `(NotFound|NoSuch|DoesNotExist)` — e.g. `ResourceNotFoundException` (JSON),
  `NoSuchHostedZone` (REST-XML), `LoadBalancerNotFound` (Query). Status from
  `@httpError`, defaulting to 404 for JSON families and 400 for Query.
- **already-exists**: matching `(AlreadyExists|Duplicate|Exists)`.
- **invalid-parameter**: matching `(InvalidParameter|Validation|InvalidInput|MalformedInput)`.
- **invalid-token**: matching `(InvalidNextToken|InvalidPaginationToken|InvalidMarker)`;
  when absent, fall back to invalid-parameter. `serviceutil.ErrInvalidPageToken`
  must be mapped — never silently restarting at page 1 (see
  [pagination-plan.md](./pagination-plan.md) H1/G3).

When no candidate exists, the generator **fails loudly** and the service's
seasoning file must name the error explicitly. A silent generic fallback here is
how emulators end up returning `InternalError` for a missing resource and hanging
every SDK waiter.

Envelope selection is unchanged: it comes from the codec, which comes from
claim-first identification ([level2-codegen.md](./level2-codegen.md) Track 1).

### 3.4 Validation depth: minimal but faithful

Generate exactly the checks the model states, and nothing else:

- `@required` member presence.
- `@length` (string/list/map), `@range` (numeric), `@pattern`, `enum` membership.
- Nothing cross-field. Nothing referential (a `RoleArn` naming a nonexistent role
  is accepted — enforcing that is Tier 2, and IAM has no enforcement anyway).

Rationale: model-derived validation is free, deterministic, and refreshes with the
model. Hand-written validation is where fidelity claims rot. Where AWS validates
something the model does not express (Route 53's zone-name normalisation, S3's
bucket-name grammar), that is seasoning, and it is the *only* validation a human
writes.

### 3.5 Identifiers, ARNs, timestamps, idempotency

- **IDs** come from a generated per-resource pattern: caller-supplied name when the
  create input has a name member; otherwise a deterministic prefixed ID whose
  prefix comes from the CFN schema's `primaryIdentifier` shape or the seasoning
  file (`s-%08d`, `wg-…`, `arn:…`). Never a bare UUID when AWS uses a formatted ID
  — CDK constructs and users' regexes both care.
- **ARNs** come from a generated template per resource:
  `arn:aws:{sigv4-service-name}:{region}:{account}:{resource-path}`. The
  `{resource-path}` segment is the one field this plan expects to hand-correct
  most often (`table/{name}` vs `{name}` vs `function:{name}`), so it lives in the
  seasoning file with the generated template as the default. `protocol.ARN` grows
  a generic `ARNFromTemplate` and the existing per-service helpers stay.
- **Timestamps** come from `clock.Clock` — never `time.Now()` — so tests and
  goldens stay deterministic.
- **Idempotency**: `CallerReference`/`ClientToken` members, where modeled, are
  stored and *echoed*; a repeat create with the same token returns the original
  record rather than a conflict. That is the behaviour CFN rollback and CDK retry
  depend on, and it is generated from the member's presence, not judgement.
- **Region scoping**: global services (per the model's `@aws.api#service` endpoint
  data) store without a region key; everything else uses
  `serviceutil.RegionKey`.

### 3.6 Non-CRUD verb operations — the default rule

`Start*`, `Stop*`, `Enable*`, `Disable*`, `Associate*`, `Publish*`, `Invoke*`,
`Execute*`, `Query*`, `Put*Records`…

**Default: stay Tier 0 (protocol-correct 501).** Tier 1 promises stored metadata,
and a verb op that returns a shape-correct success while doing nothing is a *lie
that passes tests* — the worst possible failure mode for an emulator, because the
caller proceeds as though the effect happened.

**Two generated exceptions**, both narrow and both mechanically detectable:

1. **State-transition verbs bound to a resource.** The op's input identifies a
   resource this service stores, and its effect is fully expressible as a field
   update on that record (`StartLogging`/`StopLogging` → `IsLogging: true/false`;
   `Enable*`/`Disable*` → a modeled enum member). Generated as an update. This is
   what `cloudtrail` already does by hand and it is genuinely faithful at Tier 1.
2. **Empty-effect queries over stored metadata.** The op reads only what we
   already store (`GetXCount`, `List*ByY`). Generated as a read/list.

Everything else — anything that must *produce* data (`Invoke`, `Publish`,
`GetRecords`, `StartQuery`, `SendMessage`) — is explicitly excluded, stays 501,
and is listed in the service's generated `tier0.txt` with a reason. That list is
the input queue for [full-emulation-priority.md](./full-emulation-priority.md).

Per-op judgement is permitted only by *adding* an op to exception class 1 in the
seasoning file with a one-line justification. The default never flips.

### 3.7 Explicit non-goals of Tier 1

No data planes. No cross-service side effects (an inert `AWS::Events::Rule`-alike
does not fire). No eventual-consistency simulation — every create is immediately
readable, and every asynchronous status reaches its terminal value at once
(Route 53's documented `INSYNC`-immediately divergence is the precedent, and it is
the right one: CDK waiters converge).

## 4. Design — generation

### 4.1 One generator, three outputs

**Recommendation: extend `cmd/awsmodelgen`; do not add a third generator binary.**
Level-2 Track 3's prospective `cmd/codegen` becomes this same binary. Rationale:
one model-loading path, one revision-pin check, one CI regen-and-diff gate, and
the A5 refresh workflow ([aws-api-operation-coverage.md §8](./aws-api-operation-coverage.md))
automatically covers inert code the day it lands — a second generator would need
its own copy of all four.

New flags: `-inert-services <file>` (the reviewed in-scope list) and
`-inert-out internal/services`. Outputs, per in-scope service:

| File | Contents |
| --- | --- |
| `internal/services/<svc>/inert_types.gen.go` | Typed `In`/`Out` structs for the service's Tier 1 ops, with `json`/`cbor`/`xml` tags derived from the model's member names and protocol traits. |
| `internal/services/<svc>/inert_ops.gen.go` | The op table: a **sorted static `[]inert.Binding` slice** (not a map — §6), each entry naming operation, class, resource, and the generated handler closure. Plus generated `SupportedProtocols()`. |
| `internal/services/<svc>/inert_resources.gen.go` | Per-resource record struct (create-input ∪ update-inputs ∪ derivations), namespace constants, ID/ARN templates, error selections, pagination member names. |
| `internal/services/<svc>/capabilities_inert.gen.go` | `//go:build dev` capability rows at `StatusInert`, so capgen/STATUS.md/docs stay true automatically. |
| `internal/services/<svc>/tier0.gen.txt` | Ops deliberately left at 501, with reasons. Consumed by `stub-report` and by the compat generator. |
| `internal/services/<svc>/seasoning.go` | **Not generated.** Created once by hand, never overwritten. See §4.4. |

Everything generated is checked in and gated by a regen-and-diff CI job, exactly
as `manifest.gen.go` is today.

### 4.2 Typed records, not attribute bags — and why

An "arbitrary attribute bag" (`map[string]any` keyed by service/type/id) is
tempting and wrong for this codebase:

- **Response shape fidelity is the whole point of Tier 1.** The Query and REST-XML
  codecs serialise via struct tags — element nesting (`ChildHealthChecks>ChildHealthCheck`),
  flattened lists, XML namespaces, and JSON key casing all come from tags. A bag
  cannot produce them.
- **`op.Typed[In,Out]` is generic and monomorphised.** A bag would force
  `TypedAny` everywhere and reintroduce reflection on the hot path that
  [op.go](../../internal/protocol/op/op.go) exists to avoid.
- **Wire-byte goldens need stable field order**, which structs give and maps do not.

So: typed records, marshalled to JSON for persistence in the existing
`state.Store` — the pattern every current service uses, so no new storage
subsystem, no migration, and `/_overcast/debug/state` keeps working.

### 4.3 The shared inert runtime — `internal/inert` — **landed (I2)**

A *small* package holding the behaviour that is identical for every generated
resource, so it is written and tested once rather than emitted 400 times. As
shipped:

```go
package inert

// Config is the per-service wiring. One per service, handed to every store —
// so a generated service emits the wiring once rather than once per resource.
type Config struct {
    Store  state.Store
    Clock  clock.Clock                     // §3.5: never time.Now()
    Logger *serviceutil.ServiceLogger
    Region func(ctx context.Context) string // nil ⇒ global service, unscoped keys
}

// Store is the generic metadata store: one namespace per service+collection,
// records keyed by region-scoped identifier, persisted as JSON in state.Store.
type Store[T any] struct{ /* Config + namespace */ }

func NewStore[T any](cfg Config, namespace string) *Store[T]

func (s *Store[T]) Put(ctx context.Context, id string, rec *T) error
func (s *Store[T]) Get(ctx context.Context, id string) (*T, bool, error)   // malformed record ⇒ (nil,false,nil) + warn
func (s *Store[T]) Delete(ctx context.Context, id string) error
func (s *Store[T]) List(ctx context.Context) ([]*T, error)                  // stable-sorted, skips malformed
func (s *Store[T]) Page(ctx context.Context, token string, limit int, opts serviceutil.PaginateOptions) (serviceutil.Page[*T], error)
func (s *Store[T]) Now() time.Time                                         // the injected clock, §3.5

// StorageError and PageError map a store failure onto the wire envelope, so
// the mapping lives here rather than in the generator's templates. PageError
// turns serviceutil.ErrInvalidPageToken into the service's own modeled
// invalid-token error — never a silent restart at page 1.
func StorageError(err error) *protocol.AWSError
func PageError(err error, invalidToken *protocol.AWSError) *protocol.AWSError

// Tags is one shared ARN-keyed tag store used by every generated service, so
// Create-time tags and TagResource write to the same place (§7.3). It wraps
// serviceutil.NSStore rather than reimplementing it.
type Tags struct{ /* serviceutil.NSStore */ }

func NewTags(cfg Config, namespace string) *Tags
func (t *Tags) Load(ctx context.Context, key string) (map[string]string, *protocol.AWSError)
func (t *Tags) Apply(ctx context.Context, key string, incoming map[string]string, cfg serviceutil.TagValidationConfig) (map[string]string, *protocol.AWSError)
func (t *Tags) Remove(ctx context.Context, key string, keys []string) (map[string]string, *protocol.AWSError)
func (t *Tags) Delete(ctx context.Context, key string) *protocol.AWSError

// Binding is one generated operation's declarative description.
type Binding struct {
    Op       string
    Class    Class      // Create|Read|Update|Delete|List|Tag|Untag|ListTags|Transition
    Resource string
    Invoke   op.Operation
}

// Lookup binary-searches a sorted []Binding. Zero-alloc, mirroring awsapi's
// generated indexes: the search is hand-rolled rather than sort.Search,
// because a closure over the name escapes and puts an allocation on the
// dispatch hot path.
func Lookup(bindings []Binding, name string) (Binding, bool)

// Sorted is Lookup's precondition, asserted per service so a generator
// emitting bindings out of order fails loudly instead of resolving some
// operations to the wrong handler.
func Sorted(bindings []Binding) bool

// Collisions returns every binding a hand-written operation shadows and that
// the service's inert_overrides.txt does not declare — §4.5(3).
func Collisions(bindings []Binding, handWritten, allowed []string) []string
```

Measured on the eight-operation table the pilot ships (AMD Ryzen 9 5900X,
`go test -bench Lookup -benchmem -count=3`): `Lookup` at **13.1–13.7 ns/op,
0 B/op, 0 allocs/op**. The map this replaces looks up marginally faster
(9.6–10.3 ns/op) and that is not the trade being made: building it costs
**265–279 ns and 688 B in 2 allocations per service, on every process start**,
which is the §6.1 cost a static sorted slice removes entirely.

Two divergences from this section's original sketch, both recorded rather than
silent. `Config` names the dependencies the sketch wrote inline on `Store[T]`,
so the wiring is emitted once per service instead of once per resource, and it
carries the `Region` hook §3.5's global/regional split needs. `StorageError` /
`PageError` exist because the store methods return a bare `error` — which is
right, since `Page` has to be able to say "that token is garbage"
distinguishably — but every generated handler would otherwise repeat the same
mapping.

This is where route53/store.go's 362 lines of put/get/list/scan/skip-malformed
collapses into one generic implementation. It replaces nothing that exists — the
hand-written services keep their own stores until and unless someone chooses to
migrate them (explicitly out of scope here).

### 4.4 What must stay hand-written — and the cap

The per-service `seasoning.go` is the **only** hand-written file, and the plan's
health metric is its size. Budget: **≤150 lines for a typical service**; anything
larger is a signal that the generator is missing a capability, and the reviewer
should push back rather than accept the file.

Legitimate seasoning:

- ID/name derivation quirks and prefix choices the model does not express.
- ARN `{resource-path}` overrides.
- Steady-state enum values the model leaves ambiguous.
- Defaults AWS applies that the model does not carry as `@default`.
- Idempotent-delete overrides.
- Adding an op to verb-exception class 1 (§3.6), with justification.
- Odd protocols: a service whose real wire behaviour diverges from its model
  (documented, with evidence, per repo discipline).

Illegitimate seasoning (fix the generator instead): retyping fields, rewriting
CRUD, hand-rolling pagination, hand-writing error envelopes.

### 4.5 Precedence — hand-written always wins

**Rule: a hand-written implementation of an operation always overrides the
generated inert one. There is no configuration and no exception.**

Mechanically, and consistent with Level-2 Track 2's typed-first semantics:

1. The service's existing `typedOps()` map (hand-written) is consulted first.
2. Only on a miss does dispatch fall through to `inert.Lookup(generatedBindings, op)`.
3. A **build-time test** asserts that every generated binding colliding with a
   hand-written op is listed in a checked-in `inert_overrides.txt` for that
   service. Collisions are reviewed data, never silent.
4. The generator refuses to emit a record type for a resource whose collection
   namespace collides with a hand-written service namespace, unless the seasoning
   file explicitly points the generated resource at the hand-written store.

This is what makes §7's backfill of partially-implemented services safe: adding
generated inert ops to `secretsmanager` can add the 7 missing operations without
being able to reach the 14 implemented ones.

### 4.6 The raw-AST vendoring question — reconciled explicitly

[aws-api-operation-coverage.md §3](./aws-api-operation-coverage.md) states the
policy: the complete raw Smithy snapshot is **deliberately not vendored** (it is
large, only the generated manifest is needed at runtime); `models/aws/VERSION`
pins provenance; regeneration requires a local `api-models-aws` checkout at the
pinned revision; runtime code must never parse model files or contact the network.

Inert-tier codegen needs **shapes** (members, types, traits, errors, resource
lifecycles), which the manifest deliberately omits. Three options were considered:

| Option | Verdict |
| --- | --- |
| **A.** Vendor the full raw Smithy AST | **Rejected.** Directly contradicts §3, adds hundreds of MB to the repo, and 95% of it (documentation traits, examples, out-of-scope services) is never read. |
| **B.** Change nothing; require the pinned checkout for all regeneration | **Insufficient.** Correct today because the manifest is regenerated rarely by automation. With per-service inert code, regeneration becomes a routine contributor action; a hard external-checkout dependency makes ordinary PRs unreviewable offline and breaks the `aws-models-check` no-network guarantee for the new artifacts. |
| **C.** Vendor a **pruned, derived shape snapshot** | **Recommended.** |

**Recommendation (C):** commit `models/aws/shapes/<service>.json` containing, for
**in-scope services only**, only the shapes reachable from their operations
(inputs, outputs, errors, resources, and transitively referenced structures/enums/
lists/maps), with traits filtered to an allowlist. That allowlist is
`shapeTraitAllowlist` in [cmd/awsmodelgen/shapes.go](../../cmd/awsmodelgen/shapes.go),
which is where it is read rather than restated: **46 traits** today, grouped by
what needs them — structure and member semantics, errors, HTTP bindings,
serialisation names, pagination, service identity and protocol family, and the
two `aws.api#` resource traits. It is wider than what ships reads: the
2026-09-06 audit (#1795) found 12 of the 46 consumed by anything shipped, the
rest held for the inert generator this plan builds. A resource shape's
lifecycle links are not traits at all — they are fields of the shape, and the
pruner keeps them with it. Documentation, examples, waiters, smoke tests, and
every out-of-scope service are pruned.

Why this satisfies §3 rather than contradicting it:

- It is **not** the raw AST — it is a generated, reviewed, byte-deterministic
  derivative, produced by the same pinned generator, with its own
  `shapes-sha256` recorded in `models/aws/VERSION`.
- **Runtime still never reads it.** It is generator input only, same status as the
  upstream checkout, just checked in.
- It **preserves offline `aws-models-check`**: a PR can regenerate and diff the
  inert output with no network and no external checkout, closing the same gap
  `manifest-sha256` closes for the manifest today.
- The A5 refresh workflow regenerates the pruned snapshot from the verified
  upstream mirror in the same PR that bumps the revision, so drift is impossible.
- **The snapshot is shared, not private to this plan.**
  [compat-coverage-modelgen.md](./compat-coverage-modelgen.md) §3.7's
  `cmd/compatgen` consumes the same `models/aws/shapes/` files (scoped to the
  union of both plans' allowlists) — one pruner, one `shapes-sha256`, two
  consumers. Neither plan builds a second distillation.

**Gate:** a hard size budget, enforced in CI. Ship the pruner *before* Phase I1,
measure the snapshot for the pilot's three services, and set the fleet budget from
that measurement — if the projected in-scope snapshot exceeds the budget, prune
harder (drop optional members' documentation-only traits; consider a compact
binary encoding) before widening scope. Do not widen scope on an unmeasured
budget. §3 of the coverage plan should gain a one-paragraph amendment pointing
here.

#### The measurement — I1, landed

The pruner is `cmd/awsmodelgen -shapes-out -shapes-services`; the snapshot is
`models/aws/shapes/<service>.json`, digested as `shapes-sha256` in
`models/aws/VERSION` and verified offline — and held to its size budget — by
`internal/awsapi/shapes_provenance_test.go`. Committed for the I4 pilot trio plus
I2's smoke-test service, against revision `06544fdc`:

| Service | Protocol | Ops | Shapes | Pruned | Upstream | Bytes/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| `batch` | REST-JSON | 45 | 404 | 98,273 | 709,779 | 2,184 |
| `organizations` | JSON 1.1 | 63 | 339 | 95,345 | 594,121 | 1,513 |
| `servicediscovery` | JSON 1.1 | 30 | 206 | 39,316 | 303,048 | 1,311 |
| `elastic-load-balancing` | Query | 29 | 212 | 35,614 | 222,952 | 1,228 |
| **Total** | | **167** | **1,161** | **268,548** | **1,829,900** | **1,608** |

Pruning keeps **14.7%** of the upstream bytes. The allowlisted traits are only
~6% of the pruned file; the remainder is structure (shape names, member names,
targets), so pruning *traits* harder buys almost nothing — any further saving has
to come from the encoding. Namespace-relative shape references already take 37%
off, and are in.

**Fleet projection**, against [compat-coverage-modelgen.md](./compat-coverage-modelgen.md)
§3.9's 14,410 unregistered-identity operations — the pre-never-list upper bound:

| Basis | Rate | Projected |
| --- | --- | ---: |
| Blended measured | 1,608 B/op | **22.1 MiB** |
| Worst family measured (`batch`, REST-JSON — also I7, the largest wave) | 2,184 B/op | 30.0 MiB |
| Per service (255 services × ~67 KB) | — | 16.3 MiB |
| Realistic, once the never-list removes "the majority" of I7 (~8,500 ops) | 1,608 B/op | 13.0 MiB |

**Fleet budget: 24 MiB.** Set from the blended measurement plus headroom, and
calibrated against what this repo already commits: `internal/awsapi/manifest.gen.go`
is 8.5 MiB of generated output today, so the snapshot at full scope is roughly
2.5× an artifact the tree already carries. A real cost, not a disqualifying one.
Two consequences, both binding:

- The worst-family rate **exceeds** that budget at full pre-exclusion scope. I7
  may not widen to its full 8,707 operations before the
  [services-never-emulated.md](./services-never-emulated.md) exclusions land;
  this plan already expects them to remove the majority, and the 13.0 MiB row is
  what the budget is sized for.
- **Re-measure per wave.** A wave whose measured bytes/op exceeds 1,608 spends
  budget faster than projected; if the running total projects past 24 MiB, prune
  harder or move to a compact encoding *before* the wave, per this gate.

CI enforcement is `maxShapeSnapshotBytes` in
`internal/awsapi/shapes_provenance_test.go`, currently 336 KiB — the committed
268,548 bytes plus ~25% headroom, enough to absorb an upstream refresh on the
current service set without a reviewer touching it. Raising it is a reviewer's
decision about how much fleet budget a wave spends, never an automatic
consequence of adding a line to `models/aws/shapes-services.txt`.

#### 2026-09-07 — wave 2 raise

`secrets-manager`, `sns`, `kms` and `iam` were added to
`models/aws/shapes-services.txt` for compat-coverage-modelgen G4 wave 2
(#1883), selected by implemented-operations-per-byte rather than by operation
count. The committed snapshot went 307,535 → 650,833 bytes across 9 services,
2.6% of the 24 MiB fleet ceiling, and `maxShapeSnapshotBytes` was raised to
800 KiB — ~1.26× the new total, the same headroom factor used above for
336 KiB.

The measurement behind the selection (#1883) surfaced two findings for this
plan, neither a blocker for wave 2: **s3** is the only service measured so far
over the 1,608 B/op gate (2,200 B/op), so it needs structural pruning or a
compact encoding before any wave can include it; and the ten smallest Tier-0
JSON-family services (4–19 ops) **all** exceed the 1,608 B/op gate, several by
30–90%, because fixed per-service shape overhead dominates at tiny op counts.
Smallest-op-count is the wrong selector for an inert-tier wave —
implemented-ops-per-byte is the one that worked.

## 5. The CloudFormation / CDK path

Tier 1 is not achieved by the API alone. `cdk deploy` → CloudFormation → the
provisioner, and the provisioner must know how to map each `AWS::Service::Resource`
onto the service's API (§2.5).

### 5.1 Vendor CloudFormation resource schemas as a second snapshot

**Recommendation: yes.** AWS publishes CloudFormation resource provider schemas
publicly (per-region schema bundles; also reachable via `DescribeType`). They
supply, per resource type, precisely the four things a provisioner handler needs
and Smithy does not:

| Schema field | Provisioner use |
| --- | --- |
| `primaryIdentifier` | the physical ID |
| `readOnlyProperties` | the legal `Fn::GetAtt` attribute set, and their names |
| `createOnlyProperties` | when `Update` must return `errReplacementRequired` |
| `required`, `properties`, `definitions` | property names/types for the request body, and required-property validation |
| `handlers` | which of create/read/update/delete/list the type supports |

Vendor it as `models/cfn/` with its own `VERSION` (source URL, revision/date,
sha256, license) and the same pruning discipline as §4.6 — in-scope resource types
only, documentation stripped. Same rules: generator input only, never read at
runtime, refreshed by an automation PR.

Honest limitation, stated up front: **the CFN schema does not say which API
operation to call.** It gives shape, not binding.

### 5.2 The binding table is reviewed data, not a heuristic

Generate `internal/services/cloudformation/bindings.gen.go` from a **checked-in
`bindings.yaml`**: CFN resource type → Overcast service key + create/read/update/
delete operation names + property→member renames. The generator seeds proposals
by name-matching (`AWS::Batch::JobQueue` → `batch` + `CreateJobQueue`/
`DescribeJobQueues`/`UpdateJobQueue`/`DeleteJobQueue`) and emits **proposals for
human review**, never auto-adopting them. This mirrors the awsapi alias table's
discipline: nine alias mappings are "tested as explicit data, never inferred from a
naming rule" ([aws-api-operation-coverage.md §4.2](./aws-api-operation-coverage.md)).

### 5.3 One table-driven handler, not 400 generated structs

Add a single runtime type:

```go
type genericResourceHandler struct{ b binding } // implements resourceHandler + resourceUpdater
```

It reads `b` (from the generated table), maps props → request body, dispatches
through the existing `internalJSON`/`internalXML` helpers, extracts the physical
ID via `primaryIdentifier`, and builds `attrs` from `readOnlyProperties`. Registration
is a loop over the generated table into `resourceHandlers`, run **after** the
hand-written literal map so that hand-written entries win (with a test asserting
the overlap set is exactly the reviewed list). Binary-size impact is one type plus
a static table, not 400 method sets.

`stubResourceHandler`'s "unknown type silently succeeds" behaviour stays for
genuinely unknown types, but gains a counter surfaced in `/_overcast/debug/metrics` and a
compat assertion: **a stack whose deploy used any stub handler fails the wave's
CDK acceptance gate.** Silent stubbing is exactly what makes a green deploy
meaningless.

### 5.4 CDK acceptance

Decompose `compat/suites/cdk` from one monolithic `CdkCompatStack` into
`stacks/<service>.ts` plus the existing shared stack, and register one CDK group
per wave in `compat/suites/registry.json`. A wave's stack must, per service:
create ≥2 resource types with a dependency between them (so `Ref`/`GetAtt` are
exercised), assert the deployed outputs via the SDK, then `cdk destroy` clean with
no leaked resources (the leak detection added for ElastiCache in #459 is the
precedent). See [compat/suites/cdk/AGENTS.md](../../compat/suites/cdk/AGENTS.md).

## 6. Performance and footprint

Repo values are explicit here: startup honesty
(shipped in #252 — see [docs/dev/performance.md](../dev/performance.md)), the ≤1 ms
handler-overhead guardrail, and zero-allocation generated lookups
([aws-api-operation-coverage.md §5](./aws-api-operation-coverage.md), where
registry lookups measure 79–217 ns/op at `0 B/op, 0 allocs/op`, router
construction 2.75–3.28 ms/op and 1.68 MB/op).

Design decisions that follow:

1. **Static sorted slices + binary search, not init-time maps.** Today every
   service builds its `typedOps()` `map[string]op.Operation` in `New()`. At 50
   services that is fine; at 300 services × dozens of ops it is thousands of map
   inserts and interface allocations on every startup. Generated bindings are
   `var bindings = []inert.Binding{…}` in sorted order — compile-time data, zero
   init cost, zero allocation on lookup. This mirrors `awsapi`'s indexes.
2. **Lazy service construction.** Generated inert services get no eager `New()`
   in `router.go`'s registry; they register their route/target/action *ownership*
   (cheap, static) and construct their store lazily on first request via
   `sync.Once` — the same pattern the RPC dispatcher already uses
   ([aws-api-operation-coverage.md](./aws-api-operation-coverage.md) A3/A4). The
   `prof.mark("  new: <svc>")` line per service in
   [router.go](../../internal/router/router.go) stays, so any regression is visible
   in the startup timeline rather than hidden in an aggregate.
3. **The 501 fallback stays untouched and fast.** Never-emulated services get no
   generated code at all, so their requests keep taking the existing
   registry-claim → shared-fallback path with no added work.
4. **Per-wave budget, enforced, measured.** Every wave PR records: Δ stripped
   binary size, Δ `go build ./...` wall time from clean cache, Δ
   `BenchmarkRouterConstruction`, Δ `startup_duration_ms`, and the generated
   line count. Thresholds: no measurable change to `startup_duration_ms`
   (per the <50 ms budget's intent), router construction within noise, and
   binary growth declared and justified. A wave that moves startup does not merge;
   it gets a lazier registration or a narrower scope. Measurement discipline per
   [storage-test-plan.md](./storage-test-plan.md) — command, environment,
   before/after, three runs.
5. **Scope is the primary lever.** 14,410 remaining operations at even 30
   generated lines each is ~430k lines. Two mechanisms keep that bounded:
   [services-never-emulated.md](./services-never-emulated.md)'s never + deferred
   buckets remove 95 + 24 services outright (connect 379 and pinpoint 122 fall out
   here), and **wave selection** keeps in-scope-but-unscheduled heavyweights
   (sagemaker 403, iot 272, quicksight 269, datazone 190, …) out of budgets
   until [full-emulation-priority.md](./full-emulation-priority.md) or a
   concrete user need pulls them. Being an inert-candidate grants eligibility,
   not a place in a wave.

## 7. Interplay with existing services (inert backfill)

The same machinery fills the `StatusUnsupported` gaps inside implemented services
(§2.2 table: **153 rows across 21 services**, re-derived 2026-08-23). The rules:

1. **Precedence (§4.5) is absolute** — generated code cannot reach an implemented
   operation.
2. **Record goldens first.** Before a backfill PR registers generated bindings in
   a service, capture wire-byte goldens
   ([wire-byte-goldens.md](./wire-byte-goldens.md)) for that service's existing
   operations. The backfill must be byte-identical on every pre-existing op.
3. **Shared state, declared.** Where a backfilled op reads a resource the
   hand-written code owns (e.g. Secrets Manager's remaining 7 ops over existing
   secrets), the seasoning file points the generated resource at the hand-written
   store rather than creating a parallel namespace. Two stores for one resource is
   the failure mode to design against.
4. **Capability status becomes `StatusInert`**, so `/capabilities`, the web UI, and
   STATUS.md tell the truth about which operations are memory-only. The zero
   `StatusPartial` / zero `StatusWIP` counts today mean these two statuses are
   effectively unused; backfill makes the Supported/Inert distinction load-bearing,
   which is the honest outcome. (That claim has since expired: `StatusPartial`
   is in real use — 14 rows across `backup`, `s3`, `iam`, `kms` and `sns` — so
   §10's open question 5 about `StatusPartial` vs `StatusInert` is live, not
   hypothetical.)

Backfill order follows CDK/user demand, not row count: `cloudformation` (28) and
`ses` (18) are the largest, but `sts` (6), `sqs` (2) and `lambda` (2) are the
cheapest wins and should ride along with the pilot. `dynamodb` is no longer one
of them — it grew from 1 unsupported row to 7 as its modeled surface widened,
which is the general pattern: backfill targets get *more* expensive while they
wait, so re-derive this list at wave-planning time rather than trusting it.

## 8. Phasing

Failing-test-first throughout; this document is updated in the **same commit** as
the work it describes (per repo discipline). Every phase ships as small
independently reviewable PRs, and no phase is shippable with a known regression,
stale generated output, or a red required check.

| Phase | Contents | Effort | Acceptance gate |
| --- | --- | --- | --- |
| **I0** — contract & truth ✅ **done** | Write §3 as executable conformance tests in `internal/inert/conformance` (table-driven: per class, per protocol family). Fix STATUS.md's stale prose (§2.2 corrections). Add the `Tier 0/1/2` vocabulary to CONTRIBUTING. | S | Conformance suite exists and **fails** against a deliberately naive stub; STATUS.md matches the capability registry. Landed in [PR #1360](https://github.com/overcast-sh/overcast/pull/1360): `internal/inert/conformance.Check`/`Run` cover all 15 clauses named above; `TestNaiveStub_ViolatesExpectedClauses` runs the suite against a naive in-memory stub over both JSON 1.1 and the AWS Query protocol and pins the exact 10-clause violation set. STATUS.md's only remaining drift (CloudFormation's resource-type count) is fixed; the two corrections §2.2 originally cited had already been applied. |
| **I1** — shape snapshot — **landed** | Build the pruner in `cmd/awsmodelgen` (`-shapes-out`); commit `models/aws/shapes/` for the pilot's three services; add `shapes-sha256` to `models/aws/VERSION`; extend `make aws-models-check` and the A5 workflow. Amend [aws-api-operation-coverage.md §3](./aws-api-operation-coverage.md) with the §4.6 reconciliation. Shipped with `organizations` as a fourth service, for I2. | M | **Met.** Snapshot is byte-deterministic (regen-and-diff, plus a repeat-run test); offline `aws-models-check` validates `shapes-sha256` and the size budget with no network and no model checkout; a test proves no runtime package reads the snapshot. Measured 268,548 bytes / 167 ops / 1,608 B per op and fleet budget 24 MiB — see §4.6's measurement. |
| **I2** — inert runtime — **landed** | `internal/inert`: `Config`, `Store[T]`, `Tags`, `Binding`, zero-alloc `Lookup`, plus `Sorted`/`Collisions` for the two invariants a generator can break silently (§4.3 records the API as shipped and its two divergences from the original sketch). `organizations` rewritten against it end to end, and its **policy** resource brought to Tier 1: `CreatePolicy`/`DescribePolicy`/`UpdatePolicy`/`DeletePolicy`/`ListPolicies` plus `TagResource`/`UntagResource`/`ListTagsForResource` — a full CRUD-plus-tag surface rather than a second static getter, so the runtime is exercised rather than merely linked. `AttachPolicy` stays Tier 0 per §3.6. | M | **Met.** `conformance.Check` returns zero violations for a real `organizations` Fixture over the service's own `Dispatch`; 13 of the 15 clauses run, and the two that skip do so because the model declares no member for them — `3.5/timestamps` (Organizations models no timestamp on `Policy`/`PolicySummary`; the rule is held instead by `TestPolicyTimestampsComeFromTheClock` and `TestStore_NowComesFromTheInjectedClock` against the stored record) and `3.5/idempotency` (no `ClientToken`/`CallerReference` member). `Lookup` benchmarks at 13.1–13.7 ns/op, **0 allocs/op**, against 265–279 ns and 688 B/2 allocs to build the map it replaces. 8 new `organizations` operations at `StatusInert` (1 → 9), reachable at their modeled `X-Amz-Target` bindings per `TestAllDeclaredCapabilitiesAreReachable`. **Compat suites are deliberately not part of this phase**: §8.1's seven-suite gate applies to waves (I4+), and [compat-coverage-modelgen.md](./compat-coverage-modelgen.md) / #1113 is the prerequisite that makes per-operation compat coverage affordable — the new surface is covered by Go tests instead. |
| **I3** — generator | `cmd/awsmodelgen -inert-*`: types, ops, resources, capabilities, `tier0` list. Error-selection, ID/ARN templates, pagination member detection, verb classification (§3.6). Regen-and-diff CI job. | L | Generated output for the pilot compiles, passes conformance, and regen-diff is green. |
| **I4** — pilot wave (3 services, 104 ops) | **JSON 1.1:** `servicediscovery` / Cloud Map (30 ops) — CFN `AWS::ServiceDiscovery::{PrivateDnsNamespace,PublicDnsNamespace,HttpNamespace,Service,Instance}`. **Query:** `elastic-load-balancing` / ELB Classic (29 ops) — CFN `AWS::ElasticLoadBalancing::LoadBalancer`; exercises Query `Marker`/`PageSize` pagination and XML list flattening. **REST-JSON:** `batch` (45 ops) — CFN `AWS::Batch::{ComputeEnvironment,JobQueue,JobDefinition,SchedulingPolicy}`; exercises REST bindings and the awsapi REST-trie/S3-safety boundary. Plus cheap backfills: `sts`, `sqs`, `lambda`, `dynamodb`. | L | Per §8.1 below. |
| **I5** — CFN mass-production | Vendor `models/cfn/`; `bindings.yaml` + `bindings.gen.go`; `genericResourceHandler`; stub-handler counter + compat assertion; decompose the CDK suite into per-service stacks. | L | Pilot services' CFN types deploy/destroy through generated handlers with zero stub-handler hits; existing 132 hand-written types unchanged and still winning precedence. |
| **I6** — JSON families in bulk | awsJson1_0 + awsJson1_1 in-scope services (≈130 services / ≈5,350 ops before never-list exclusions), batched ~10 services per PR. | L×n, parallelisable | Per-batch gate = §8.1. |
| **I7** — REST-JSON in bulk | The big one: ≈238 services / ≈8,707 ops before exclusions. Expect the never-list to remove the majority. | L×n | Per-batch gate = §8.1. |
| **I8** — Query + REST-XML remainder & backfill sweep | Remaining Query services; `s3-control` (restXml); the full `StatusUnsupported` backfill table (§7). | M | Goldens byte-identical on every pre-existing op; §8.1. |
| **I9** — convergence | `stub-report` and `capgen` read generated tier data; STATUS.md tier tables become generated; `stub-report`'s output reports Tier 0/1/2 per op. | M | No hand-maintained tier inventory remains; STATUS.md drift becomes structurally impossible. |

### 8.1 The standing per-wave acceptance gate

Every wave batch, without exception:

1. **Generated compat tests pass** for every op the wave brings to Tier 1, in
   every SDK/CLI suite, per [compat/AGENTS.md § "When a new Overcast service is
   implemented"](../../compat/AGENTS.md) — registry entry plus a group in all
   seven suites (node/python/go/cli/java/dotnet/rust), `na` with a reason where an
   SDK has no API, `go run ./cmd/compat --check-parity` green. This is the
   deliverable that [compat-coverage-modelgen.md](./compat-coverage-modelgen.md)
   makes affordable — **that plan is a hard prerequisite for I6 onward**; hand-writing
   seven suite groups per service does not scale past the pilot.
2. **CDK deploy + destroy green** for the wave's per-service stack, with zero
   stub-handler hits and clean teardown.
3. **Zero compat baseline regressions.** The baseline is at zero failures and CI
   gates on it absolutely (#462, #463).
4. **Wire-byte goldens** recorded and green wherever a codec, a shared helper, or
   an existing service is touched.
5. **Performance budget** recorded per §6.4.
6. **Seasoning budget**: every service's `seasoning.go` ≤150 lines, or a written
   justification in the PR.
7. **This document updated in the same commit.**

## 9. What "done" means

- Every in-scope modeled operation is Tier 1 or better; every out-of-scope one is
  Tier 0 with a protocol-correct 501 and a recorded reason. No operation is
  undeclared.
- Bringing a *new* service to Tier 1 is: add it to the in-scope list, regenerate,
  write ≤150 lines of seasoning, add its `bindings.yaml` rows, generate its compat
  group, add its CDK stack. Days, not weeks — and the diff is mostly generated and
  mechanically verified.
- An upstream model refresh automatically widens the Tier 1 surface: the A5 bot PR
  brings new operations, regenerates types and inert handlers, and the reviewer
  sees an actionable diff instead of a silent 501.
- `cdk deploy`/`cdk destroy` works for every in-scope service, with `Ref` and
  `GetAtt` returning real values, and a stack that quietly used a stub handler
  fails CI.
- Fidelity is a property of the model, not of a contributor's patience:
  `transfer`'s 2-of-15-fields problem (§2.4) cannot recur, because nobody types
  the fields.

## 10. Open questions

1. **Never-emulated boundary.** [services-never-emulated.md](./services-never-emulated.md)
   currently proposes 95 never-listed + 24 deferred services (255
   inert-candidates remaining), but flags those counts as "family-grouped
   judgment calls, not a hand-audited census" — and the in-scope bucket still
   contains unscheduled heavyweights (§6.5). I6/I7 must be re-budgeted against
   the audited list before starting,
   and the wave order should be cross-checked against
   [full-emulation-priority.md](./full-emulation-priority.md) so a service headed
   for Tier 2 soon is not given a throwaway Tier 1 pass first.
2. **Pruned-snapshot size.** Unmeasured. I1's gate exists precisely to answer it;
   if the in-scope snapshot is uncomfortably large, the fallback is a compact
   binary encoding, or reverting to option B (external checkout) for shape-level
   regeneration only while keeping the manifest offline-checkable.
3. **CFN schema source and licence.** The exact publication channel to pin
   (regional S3 schema bundles vs. the `cloudformation-cli` repo vs. `DescribeType`
   output) and its licence terms need confirming before I5 vendors anything.
4. **Query/EC2Query output shapes.** Generated struct tags must reproduce
   `ResponseMetadata` wrappers and `…Result` envelopes exactly. ELB Classic in the
   pilot is deliberately chosen to surface this early; if it proves generator-hostile,
   Query moves behind REST-JSON in the wave order.
5. **`StatusPartial` vs `StatusInert` for backfilled services.** A service that is
   half supported and half inert has no single status today. Recommend keeping
   per-operation statuses authoritative and deriving any service-level label; needs
   a decision before the web UI's service tiles can show it honestly.
6. **Verb-op exception class 1's boundary.** `StartLogging`-style transitions are
   clearly right; `AssociateX`/`DisassociateX` sit closer to the line because a
   CDK template may depend on the association having an effect. Recommend
   excluding `Associate*` from generated transitions in the pilot and revisiting
   with evidence from the CDK suite.
