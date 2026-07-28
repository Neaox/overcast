# AWS API operation coverage and S3 fallthrough prevention

> Status: proposal, 2026-07-28. Owner: TBD.
> Related: [Level 2 codegen](./level2-codegen.md) Track 3, [Smithy wire protocols](../dev/smithy.md), and [wire-byte goldens](./wire-byte-goldens.md).

## 1. Objective

For every public AWS API operation known to the emulator's pinned AWS model snapshot, an SDK, CLI, or CDK request must either reach an implementation (including inert/metadata-only implementations) or receive a protocol-correct `501 NotImplemented` response. It must never receive an S3 response unless it is an actual S3 request.

This is a routing and compatibility guarantee, not a claim that Overcast implements every AWS operation.

## 2. Audit findings

S3 deliberately registers broad `/{bucket}` and `/{bucket}/*` routes after all other services. That is correct for S3 because its REST-XML API has no reliable distinguishing header or fixed root prefix. It also means any AWS request not positively claimed by another service can be read as a bucket or object request.

Current protections are useful but incomplete:

- `TargetDispatcher` claims only currently registered AWS JSON target prefixes.
- `QueryDispatcher`, `QueryVersionOwner`, and `QueryActionOwner` claim only some Query traffic.
- `PathPrefixService` protects only manually declared disabled REST roots.
- Existing service-local stubs provide correct 501s only after the request reaches the correct service.

The ownership gaps are:

- Valid non-S3 REST paths without explicit chi handlers can match S3's wildcard.
- Lambda owns several versioned API roots but declares only `/2015-03-31` for disabled-service protection.
- `GET /?Action=...` with no claiming Query dispatcher falls through to S3's root route.
- Target prefixes, Query versions, operation lists, and REST bindings are hand-maintained and only cover currently implemented services.
- `cmd/stub-report` inventories typed Overcast operations; it is not an AWS operation inventory. Capability tables describe Overcast status, not AWS ownership.

## 3. Source of truth and scope

Use AWS's public [`aws/api-models-aws`](https://github.com/aws/api-models-aws) repository as the source of truth. AWS publishes its public service models daily in Smithy JSON AST form and describes them as definitive public API definitions. The model supplies service identity, operations, protocol traits, and HTTP bindings; it does not dictate Overcast's implementation status.

Vendor a pinned snapshot under `models/aws/` with a `VERSION` file recording upstream commit, model date, source URL, and license provenance. Runtime code must never parse model files or contact AWS/GitHub.

This covers public AWS management-plane/service API operations used by SDKs, CLI, and CDK. It excludes emulator-only `/_*` endpoints, service data/runtime endpoints with intentionally arbitrary user paths (such as API Gateway execution), and CLI conveniences such as waiters. Waiters are covered indirectly through their API operations.

## 4. Design

### 4.1 Generated operation manifest

`cmd/awsmodelgen` reads the pinned JSON AST and generates checked-in `internal/awsapi/manifest.gen.go`. Generate routing metadata only:

- canonical service name, SDK ID, API version, and source provenance;
- operation name and protocol traits;
- AWS JSON target-prefix data;
- AWS Query API-version/action ownership;
- REST method and URI template;
- protocol/error-envelope family; and
- S3 designation or an ambiguous root requiring stronger service evidence.

Do not generate clients, every request/response type, or a handler per operation. Types remain an allowlisted, implementation-only concern in Level 2 Track 3.

### 4.2 Registry

`internal/awsapi.Registry` consumes generated tables and only determines ownership:

```go
type Claim struct {
    Service   string
    Operation string
    Protocol  Protocol
}

func (r *Registry) Claim(req *http.Request) (Claim, bool)
```

It does not decode bodies, persist data, implement business logic, or replace service routers. It follows Smithy protocol precision:

1. RPC v2 CBOR marker and request path;
2. AWS JSON content type plus `X-Amz-Target`;
3. AWS Query `Version`/`Action`, with SigV4 credential service as a tiebreaker; and
4. REST method plus generated URI-template match, using SigV4 service for genuinely ambiguous roots.

The registry never claims `s3` or `/_*`. If evidence is insufficient to distinguish a request from S3, preserve S3 behavior. An exact known non-S3 REST binding is sufficient for unsigned local test traffic.

### 4.3 Router fallback

Existing exact chi routes and target/query dispatchers retain ownership of implemented operations. Add one fallback before S3's wildcard registration:

1. existing implementation or service stub handles the request;
2. otherwise, the generated registry claims a known non-S3 AWS operation;
3. one shared fallback writes a protocol-correct 501;
4. only an unclaimed request proceeds to S3.

This avoids a generated chi route for every operation, keeps startup and registration small, and makes adding an implementation a normal handler-registration change. Existing route/dispatcher precedence automatically supersedes the fallback.

The shared fallback selects `protocol.NotImplementedJSON`, `protocol.NotImplementedQueryXML`, `protocol.NotImplementedEC2QueryXML`, or `protocol.NotImplementedXML` from generated metadata. Keep a tiny explicit error-profile override table only where a Smithy protocol trait cannot select the real AWS envelope.

### 4.4 Status stays separate

The manifest declares **what AWS exposes**. `capabilities_dev.go` declares **what Overcast does**: unsupported, inert, partial, WIP, or supported.

Make `capgen` reject every non-`DocOnly` capability that does not map to a manifest operation, while retaining an explicit allowance for documented synthetic rows. Make `stub-report` consume the manifest rather than scrape typed source; it becomes a coverage report for model operations, Overcast status, implementation registration, and fallback ownership.

## 5. Performance requirements

Models are fetched, parsed, and generated at build/update time only. The binary contains compact generated maps and a precompiled REST-template trie.

- AWS JSON and Query: bounded header/form read plus map lookup.
- REST: path-segment trie walk, independent of total operation count.
- No per-request/startup disk I/O, model parsing, network I/O, or allocations proportional to model size.
- Implemented calls retain existing handlers; S3 incurs only a cheap non-S3 claim check.

Before landing, benchmark router construction and representative S3, AWS JSON, Query, and REST-fallback requests. Do not materially move the startup budget or <=1 ms handler-overhead target. Record command, environment, operation, and before/after figures for every published claim.

## 6. Delivery phases

| Phase | Work | Acceptance gate |
| --- | --- | --- |
| A0 | Write failing router integration tests for Lambda alternate versions, unknown API Gateway REST paths, Query `GET /?Action=`, AWS JSON unknown services, disabled services, and legitimate S3 controls. | Every non-S3 fixture returns the correct 501 envelope, not an S3 response; S3 behavior remains unchanged. |
| A1 | Vendor a pinned model snapshot; add refresh script, validator, and generator pilot for SQS, STS, Lambda, API Gateway, and S3. | Manifest is deterministic and records source provenance. |
| A2 | Implement `awsapi.Registry`, shared fallback, and error-profile selection. | Pilot operations route correctly without changing implementation behavior. |
| A3 | Generate all public operation metadata and REST trie; migrate `stub-report`; validate capabilities against manifest. | Every generated operation has ownership or an explicit ambiguity/exclusion. |
| A4 | Add generated route-coverage tests, collision reports, performance guardrails, and CI generation checks. | No modeled non-S3 request falls through to S3. |
| A5 | Add upstream refresh automation and contributor documentation. | One bot-owned PR is updated for model changes with an actionable diff report. |

Each phase begins with failing tests. Any correction to modeled protocol, error format, or REST binding uses AWS API docs/Smithy evidence and updates the compatibility tracker.

### Shipping and branch strategy

Ship this work as small, independently reviewable PRs; do not keep it on one
long-lived feature branch. Each phase should leave main internally consistent,
fully tested, and ready for the next phase:

| PR | Shippable scope | Required condition |
| --- | --- | --- |
| A0 | Regression tests that demonstrate current fallthrough cases. | Tests describe the desired 501 contract without weakening existing S3 coverage. |
| A1 | Pinned-model tooling and a small generated-manifest pilot. | No runtime routing behavior changes. |
| A2 | Registry and fallback for the pilot services/protocols. | Include positive S3 controls and route-precedence tests; do not merge a generic fallback that could classify a genuine S3 request incorrectly. |
| A3 | Full-model manifest and capability/stub-report convergence. | Model and implementation inventories agree, with explicit exclusions. |
| A4 | Generated corpus, collision detection, and performance gates. | Corpus proves no modeled non-S3 operation reaches S3. |
| A5 | Scheduled refresh workflow and bot PR lifecycle. | The workflow updates only its dedicated automation branch and cannot silently merge. |

A2 is the first user-visible milestone and should be intentionally narrow: it
can eliminate the highest-impact REST and Query fallthroughs without waiting
for every AWS model/service migration. Every later PR widens coverage without
changing this safety rule.

No phase is shippable with a known regression, a failing or skipped required
check, an introduced compiler/vet/problem-list error, stale generated output,
or reduced S3 positive coverage. A failed acceptance gate blocks the PR; it is
not deferred to the next phase. Regression fixes use a reproducing test first
and are merged with the affected phase or as a separately green corrective PR.

## 7. Testing strategy

### Focused contracts

Test through `router.New`, not direct handlers. Assert status, content type, request ID, `x-emulator-unsupported`, and JSON/XML/Query error envelope. Maintain positive S3 coverage for bucket, object, virtual-host, root list, and subresources.

### Generated corpus

Generate a safe discriminator request for every manifest operation: target header, Query version/action, or REST method/template placeholders. Assert it reaches an owner or returns a non-S3 501; it need not have valid business input.

Generator validation must prove deterministic REST-route ownership or require a checked-in resolver declaration. Route collisions must never silently choose a service.

### Regression and performance

Run router/protocol integration tests, generator unit tests, manifest determinism tests, and generated corpus tests. Keep wire-byte goldens for implementation migrations; fallback tests cover ownership and error envelopes, not successful operation semantics.

## 8. Model refresh automation

Add a scheduled GitHub Actions workflow, weekly by default and manually dispatchable. AWS publishes daily, but weekly keeps review diffs manageable. It:

1. compares upstream `aws/api-models-aws` with `models/aws/VERSION`;
2. exits when current;
3. fetches the new revision and runs generation and all model/route checks;
4. summarizes service, operation, trait, binding, collision, and fallback-coverage changes; and
5. creates or updates one bot-owned PR.

Use a stable branch such as `automation/aws-api-models`. If an open PR exists from that branch, update it rather than creating a duplicate. Use a workflow concurrency group and permit force-with-lease only for this dedicated automation branch. Never update contributor branches or merge automatically.

Every normal PR runs a no-network `aws-models-check` target verifying the snapshot, generated diff, model validity, capability alignment, route collisions, and generated no-S3-fallthrough suite.

## 9. Relationship to Level 2 codegen

This plan is the routing-completeness foundation of Level 2 Track 3; it does not replace it.

- Track 1 identifies protocol and operation.
- Track 2 provides one implementation per implemented operation.
- This plan supplies Track 3's complete AWS operation/protocol/binding manifest and makes an operation without an implementation safely owned and rejected.
- Track 3 keeps generated input/output types allowlisted to implemented operations; unsupported ones require only the manifest and generic fallback.
- `capgen` and `stub-report` converge on the manifest, removing parallel hand-maintained operation inventories.

Deliver A1-A4 before or alongside Track 3's SQS/Scheduler generator pilot. A5 keeps Track 3 current as AWS adds services, operations, or protocol traits.

## 10. Done definition

Done means: a pinned model snapshot defines the operation universe; a generated runtime registry claims every non-S3 operation with sufficient evidence; unsupported calls receive the correct 501 envelope; S3 remains fallback only for actual/indistinguishable S3 traffic; and CI plus the scheduled update PR keep that guarantee current.

## 11. What next: converging on operation management

Yes: eventually, all existing AWS-facing routes should be represented in the
generated manifest. That does **not** mean replacing every service handler with
a generated router, nor moving business logic into a central registry. The
target architecture has three deliberately separate layers:

| Layer | Owner | Responsibility |
| --- | --- | --- |
| AWS model manifest | Generated from pinned Smithy models | What operations exist and how requests identify them. |
| Operation registration | Small hand-written service declaration | Whether Overcast implements an operation and which handler owns it. |
| Service implementation | Existing focused service packages | Parsing/validation, business behavior, state, and AWS-shaped responses. |

This separation keeps the design DRY: model metadata is defined once and shared
by router fallback, capabilities, stub reporting, tests, and automation. It
keeps it SOLID: the registry classifies; service packages implement; code
generation translates model data; none needs to know another layer's internals.
It remains idiomatic Go: static generated data, small interfaces, explicit
registrations, and ordinary handlers rather than reflection or a runtime
framework.

### Migration order

1. **Adopt the manifest for ownership first.** Complete A1-A4 so every
   operation is classified and unsupported requests cannot reach S3.
2. **Reconcile existing services.** For each service, compare manifest
   operations with capability declarations and actual handlers. Add an explicit
   operation registration for every implementation, inert stub, and deliberate
   501. Do this service-by-service; do not perform a risky fleet rewrite.
3. **Make registrations the implementation index.** Replace source scraping in
   `capgen` and `stub-report` with generated manifest plus service
   registration. CI rejects an implementation that is undocumented or an
   implemented capability without a handler owner.
4. **Converge protocols onto one operation implementation.** Follow Level 2
   Track 2: legacy HTTP paths become thin decode/delegate/encode shims, while
   one operation function owns behavior. REST services may retain their chi
   bindings because HTTP path matching is their natural protocol boundary.
5. **Generate only repetitive plumbing.** Generate operation constants,
   protocol declarations, ownership tables, and optional registration
   scaffolding. Keep validation, lifecycle, state, errors, and all meaningful
   business behavior hand-written and test-first.
6. **Manage the long tail cheaply.** A model-refresh PR automatically adds new
   operations to the fallback universe. An unimplemented operation therefore
   has zero service boilerplate yet returns a correct 501; implementing it is
   an intentional small registration plus handler change.

### Permanent guardrails

- No AWS operation may be added outside the manifest; model refresh is the
  entry point for new public API surface.
- No manifest operation may silently route to S3; generated corpus tests enforce
  this.
- Every operation has one status and one implementation owner, even when that
  owner is the generic unsupported fallback.
- Generated tables are immutable at runtime and built once, so operation
  management has no unbounded memory growth, no model parsing at startup, and
  no per-request linear scans.
- A new service implementation begins by registering selected manifest
  operations, rather than introducing another hand-maintained prefix/action
  list.

The resulting steady state is easy to manage: AWS publishes the inventory,
the generator turns it into reviewed static data, the registry guarantees
ownership, and service packages remain small, explicit, and independently
testable.
