# Wire protocols — claim-first dispatch, single-implementation ops, and model-driven codegen (v2)

> Status: proposal v2, 2026-07-24 — rewrites the original "Level 2 codegen" proposal (kept below as inspiration where still valid; superseded where it conflicts). Owner: TBD.
> Level 1 (the codec/op architecture) is live in code — design doc: [docs/smithy.md](../smithy.md) (note: its link to `plans/smithy.md` is stale; fix alongside this plan).
> Inputs: the 2026-07-24 protocol/codec architecture audit (census embedded below); AWS's April-2026 wire-protocol policy and the "reactive protocol identification" guidance it points at (see e.g. floci-io/floci#156 for the same problem in a sibling emulator); [wire-byte-goldens.md](./wire-byte-goldens.md) as the codec safety net.

## 1. Why this plan exists (the forcing function)

Since **April 2026**, AWS SDKs may add or switch a service's wire protocol **without advance notice** (the earlier CloudWatch protocol switch was the precedent). AWS's sanctioned defense for emulators is **reactive protocol identification**: each Smithy protocol defines "identification for claiming" rules (`Smithy-Protocol: rpc-v2-cbor`, `Content-Type: application/x-amz-json-1.x` + `X-Amz-Target`, form-encoded `Action=`, …) — a request *tells you* its protocol; hardcoded per-service protocol assumptions are the thing that breaks.

The goal state, in one sentence: **adopting a new wire format costs one codec + one identifier, with zero per-service edits — because every operation has exactly one implementation and protocol handling is entirely the codec layer's job.**

## 2. Current state (verified 2026-07-24)

**What's right.** `internal/protocol/codec` (Codec interface, `JSON10/JSON11/QueryXML/RESTXML/RPCv2CBOR`, precision-ordered `DefaultIdentifiers()`, `middleware.Protocol` stashing `(codec, opName)` in context) and `internal/protocol/op` (`Typed[In,Out]`, `TypedAny`, `NewRaw`) are sound, well-documented, byte-stability-disciplined. Error envelopes are fully centralized (`protocol.Write{JSON,XML,QueryXML,EC2QueryXML}Error`); no hand-rolled AWS error envelopes exist in service code. Nine services are cleanly typed-only (appconfig, appconfigdata, appregistry, appsync, bedrock, eks, msk, opensearch, scheduler).

**Debt D1 — the dead Query typed path (architecture gap, worst debt).** The Query codec never surfaces the resolved `Action` to dispatch (`identify.go:108-111` defers to a "Phase 6 query decoder" that was never built), so the typed registries of all ~11 Query-protocol services (iam 61 ops, ec2 64, rds 33, cloudformation, sts, sns, ses, autoscaling, elasticache, elbv2, route53) are **unreachable for real SDK traffic** — a complete second copy of every operation with no live traffic to catch rot. No per-service migration fixes this; only the codec can.

**Debt D2 — live dual-path duplication (~28 JSON/CBOR hybrid services).** JSON 1.x traffic runs hand-written untyped handlers; CBOR runs the typed twin; business logic is maintained twice per op. Confirmed-identical worst cases (audit): Kinesis `GetRecords`/`PutRecord` (~100 dup lines each — since fixed by delegation in PR #272, which is the **pilot for the pattern**), SSM's paginated ops (the class PR #271 fixed once and nothing prevents diverging again), and the switch-based five (backup, cognito, eventbridge, organizations, transfer). Two *incompatible* migration semantics coexist: map-style hybrids route JSON to untyped even when a typed impl exists; SQS routes typed-first for any codec.

**Debt D3 — tiers outside the architecture.** REST-XML/REST-JSON services (s3, cloudfront, apigateway, lambda, pipes) use chi routing only — no codec/op involvement (acceptable: HTTP-binding protocols are route-shaped; see non-goals). `cloudwatch` (metrics) bypasses `middleware.Protocol` entirely with bespoke `GraniteServiceVersion…` prefix parsing — not acceptable, just drift.

**Small rot:** three coexisting legacy-dispatch idioms (map literal / `dispatchLegacy` switch / inline switch); 9+ private clones of `decodeJSON`/`writeJSON` (appsync, backup, cloudtrail, ecs ×2, kinesis, kms, ssm, stepfunctions, transfer); `cmd/stub-report` misses nested service dirs (`cloudwatch/logs` absent from `docs/operation-manifest.md`) and hardcodes a container path; dynamodb's migrated-in-place `rawOps()` base is dead weight (every entry overridden).

## 3. The approach — three decoupled tracks

The v1 plan led with generating types for everything. That inverts the value order: the April-2026 exposure is **dispatch-level**, the DRY debt is **implementation-count-level**, and neither needs type generation. Codegen comes last and smaller.

### Track 1 — Claim-first protocol agility (no codegen required)

1. **Build the missing Query operation-name resolution** (audit rec #1): parse `Action` early (bounded form read; the body is re-readable for the codec's full decode) so `(codec, opName)` context works for Query exactly as for JSON/CBOR — this single change makes ~250 already-written typed registrations reachable. Acceptance: an IAM/CFN SDK request dispatches through `typedOps` with the legacy switch as fallback only.
2. **Claimed-but-undeclared policy:** when identification claims a protocol a service's `SupportedProtocols()` doesn't declare, attempt the decode anyway and log a loud `protocol drift` warning (service, op, claimed protocol). This is the reactive posture: a silent SDK protocol switch becomes a working request plus a signal, not a 415/501 mystery. (Gate behind a config flag if fidelity-strictness is preferred in CI: `OVERCAST_PROTOCOL_STRICT`.)
3. **New-format adoption recipe (documented in CONTRIBUTING):** implement one `Codec`, register one identifier in precision order, add golden-byte fixtures ([wire-byte-goldens.md](./wire-byte-goldens.md)) — done. No service edits. This is the deliverable the whole plan is named for.
4. **Bring `cloudwatch` (metrics) onto `middleware.Protocol`/`codec.FromContext`** like every other JSON service; delete its bespoke prefix parsing.
5. **REST tier stance (explicit):** s3/apigateway/lambda/pipes/cloudfront stay chi-routed — `restXml`/`restJson1` are HTTP-binding protocols where the route *is* the operation identity; forcing them through the target-header codec layer would be shoehorning. Identification may still *label* them (logging/metrics) cheaply.

### Track 2 — One implementation per operation (no codegen required)

1. **One migration semantic, everywhere:** typed-first for *any* codec once an op has a typed impl (SQS's semantic; the map-style "JSON prefers untyped" rule is what keeps duplicates alive). One legacy idiom: the map literal; the five switch-based services convert when touched.
2. **Delegate, don't duplicate — fleet-wide:** untyped handlers become decode-shims calling the typed implementation (the PR #272 Kinesis pattern, proven byte-identical including empty-body response conventions). Order by the audit census: confirmed-identical pairs first (mechanical), then the long tail per-service. Diverged pairs, if any turn up during migration, are **bugs** — fix with failing tests, not silent unification.
3. **Query services after Track 1.1 lands:** their typed registries go live; verify per-service parity (goldens + integration suites), then shrink legacy switches to fallback-only, then delete.
4. **Cleanups that ride along:** replace the 9+ `decodeJSON`/`writeJSON` clones with the shared helpers; drop dynamodb's dead `rawOps()` base; fix `cmd/stub-report` recursion + hardcoded path and regenerate the manifest.
5. **CONTRIBUTING rule:** business logic for an operation lives in exactly one function; wire-path files may only decode/delegate/encode. Review checklist item.

### Track 3 — Model-driven codegen (v1's idea, re-scoped smaller)

Keep v1's mechanism (consume AWS's published **Smithy JSON AST**; generated files checked in; CI regen-and-diff gate; stubs overridable by hand-written registrations) — it was sound. Change the scope:

1. **Generate the operation *manifest* + protocol traits for every service** — op names, protocol list (`SupportedProtocols()` becomes generated), HTTP bindings. This is tiny per service, drives correct 501 stubs for every unimplemented op, feeds `cmd/stub-report` gap analysis, and is what auto-adopts a model's new `@rpcv2Cbor`/future trait on regeneration.
2. **Generate typed Input/Output structs only for *implemented* operations** (the override allowlist). v1 generated full model types for everything — for EC2-sized models that's an enormous checked-in surface serving 501 stubs that need only an op name. Types-on-demand keeps diffs reviewable and compile times sane.
3. **Model snapshot vendored** under `models/` with a VERSION stamp and a refresh script; regeneration diffs are reviewed PRs (v1's goal 3, kept verbatim).
4. **Convergence, not new registries:** the generated manifest becomes the single declared-ops source that `capgen` capability tables and `stub-report` both read, ending the parallel hand-maintained lists.
5. v1's non-goals stand: JSON AST only (no `.smithy` parsing), no runtime codegen, no client generation, business logic stays hand-written.

## 4. Phasing

| Phase | Contents | Effort | Gate/acceptance |
|---|---|---|---|
| P0 quick wins | stub-report recursion+path fix, `docs/smithy.md` stale link, decodeJSON clone sweep | S | manifest includes cloudwatch/logs; clones gone |
| P1 | Track 1.1 Query `Action` resolution + 1.2 drift policy + 1.4 cloudwatch metrics | M | IAM/CFN SDK traffic hits typed ops; drift warning covered by tests; goldens green |
| P2 | Track 2 delegation sweep (worst-5 first, then per-service; Query services post-P1) | M×n, mechanical, parallelizable per service | per service: legacy path is decode-shim or deleted; parity pinned by goldens + existing suites |
| P3 | Track 3 generator (`cmd/codegen`): manifests+traits fleet-wide, allowlisted types; pilot sqs + scheduler | M | regen-diff CI job green; SupportedProtocols generated for pilots |
| P4 | Fleet regen; delete legacy dispatch where shims are total; capgen/stub-report converge on generated manifest | L, mechanical | one implementation per op fleet-wide; new-protocol drill (add a fake codec in a test) costs zero service edits |

Every phase: failing/pinning tests first; wire-byte goldens are the codec-change safety net; benchmark discipline per [storage-test-plan.md](./storage-test-plan.md) where perf is claimed.

## 5. What "done" means

A new AWS wire format announced tomorrow is adopted by: vendoring the refreshed models (P3 machinery flags which services gained the trait), implementing one codec + one identifier, adding goldens. No service files change. No duplicated business logic exists for any operation, so the Kinesis-class bug (fix lands on one wire path, stays live on the other) is structurally impossible.
