# Pagination fidelity — audit & remediation plan

> **Status:** planned — no items started. Audit completed 2026-07-24 (agent sweep of every paginating operation across all supported services; evidence file:line below).
> **Scope:** the **wire contract** of pagination vs real AWS — limit params honored with AWS's defaults/caps, continuation tokens that actually resume, truthful truncation flags, and AWS-shaped errors on invalid tokens. This matters more than usual for an emulator: people specifically test their pagination loops against local stacks, so an ignored `Limit` or a lying `IsTruncated` breaks exactly the client code Overcast exists to exercise.
> **Relationship to other plans:** [storage-access-plan.md](./storage-access-plan.md) owns the *storage shape* behind pagination (cursors at the storage boundary, range reads); this plan owns what the client observes. For bounded, resource-count namespaces (most `List*` ops), **in-memory slice pagination over a full scan is the correct implementation** — this plan deliberately does not require storage cursors for them. Items that need both (logs) say so and land together.
> **Audience:** any contributor or agent; [CONTRIBUTING.md](../../CONTRIBUTING.md)/[AGENTS.md](../../AGENTS.md) rules apply (failing test first, wire-format changes are the compatibility contract, prefer AWS SDK clients in tests).

---

## The contract checklist (what "faithful" means, per operation)

1. **Limit honored** with AWS's documented default and cap for that operation (cite the AWS doc page in the PR; defaults differ per op — 1000 for S3 MaxKeys, 60 for Cognito, 50 for many `MaxResults` ops…).
2. **Token resumes**: walking pages yields every item exactly once, in the operation's documented order, and terminates (empty/absent token — or, for logs `GetLogEvents`, AWS's *same-token-when-exhausted* convention).
3. **Truncation fields truthful** and correctly named for the protocol family (`IsTruncated`/`NextMarker` for Query-protocol XML like IAM; `NextToken` for JSON; `NextContinuationToken`+`KeyCount` for S3 v2; `LastEvaluatedKey`/`LastEvaluatedTableName` for DynamoDB; `position` for API Gateway).
4. **Invalid token** → the service's AWS error (`ValidationException`/`InvalidParameterException`/`InvalidToken`…), never a silent restart from page 1 — silent restart is this codebase's most common divergence and causes **duplicate delivery**, the worst failure mode for client pagination loops.
5. **SDK paginator test**: where the Go SDK ships a paginator for the op, the integration test drives it and asserts termination + exactly-once (house rule: prefer SDK clients).

## Canonical machinery (the DRY decision)

The audit found **four** coexisting pagination idioms:

| Idiom | Users | Invalid-token behavior |
|---|---|---|
| `serviceutil.Paginate[T]` ([pagination.go](../../internal/serviceutil/pagination.go)) | CloudFormation `DescribeStackEvents`; CloudFront ×4; SSM ×3 | ❌ **silent restart from page 1** (`decodeToken` returns `(0,false)`, caller treats as no-token) |
| Cognito `pageBounds` ([store.go:189-282](../../internal/services/cognito/store.go)) | in-pool ListUsers/ListGroups/ListUsersInGroup | ✅ `InvalidParameterException` |
| AppSync `paginateList[T]` ([handler.go:138-185](../../internal/services/appsync/handler.go)) | ~9 AppSync List ops | ✅ `badRequest` on bad token/limit |
| Inline bespoke | ElastiCache (✅ errors), S3 object listing (❌ silent fallback on bad token) | mixed |

**Decision:**
- **H1 — make `serviceutil.Paginate` the single canonical helper, upgraded to the Cognito/AppSync quality bar.** Change `decodeToken`'s caller contract to return an explicit invalid-token error (caller maps it to its service's AWS error type and code); add per-call default+cap limit options. This **fixes the invalid-token divergence for CloudFormation, CloudFront, and SSM in one place** — three services repaired by one helper change. Storage-access-plan's M1 (cursor-in-token codec for unbounded data) extends this same helper rather than being a second thing.
- **Migration policy (rule against consolidation-for-its-own-sake):** every op *fixed or added* by this plan must use H1. Cognito's and AppSync's already-correct helpers may stay until their services are next touched — they are behaviorally right, and rewriting working code purely for uniformity is churn. Mark them `// pagination: local helper, behaviorally canonical — fold into serviceutil.Paginate when next touched`.
- **H2 — shared paginator-contract test helper** (`tests/helpers`): seed N items > one page, walk to termination via the SDK paginator where available (raw token loop otherwise), assert exactly-once + order + terminal condition + invalid-token error. Every item below uses it; it is the regression net that keeps class-A ops class A.

---

## Items (ranked)

### G1 — CloudWatch Logs `GetLogEvents`: the one class-D (actually broken) contract

**Evidence.** [handler.go:422-492](../../internal/services/cloudwatch/logs/handler.go): `Limit`/`NextToken`/`StartFromHead` are parsed and never used; `nextForwardToken`/`nextBackwardToken` are synthesized from `len(allEvents)`/hardcoded `0`. A client polling with the returned token — the *standard* CloudWatch Logs tailing pattern — re-receives the full event set every call, forever. SDK paginators loop or desync.

**Change.** Real position tokens (AWS's `f/`+index / `b/`+index shape), `StartFromHead` direction semantics, `Limit` (AWS default 10 000 / 1 MB), and the **same-token-when-exhausted** termination convention (this op never returns a null token — the test must pin that). Implementable against today's in-memory reads first; [storage-access A4](./storage-access-plan.md) then makes it efficient — land as one unit if practical, this contract first if not.

**Accept when** the H2 walk terminates via same-token, a tail-loop (poll, get token, poll again) receives each event exactly once, and an invalid token errors.

### G2 — DynamoDB `Query`/`Scan`: cursor resolution by position, not item identity

**Evidence.** [handler.go:634-646, 888-900](../../internal/services/dynamodb/handler.go): `ExclusiveStartKey` is resolved by scanning for an item *equal* to the cursor; if that item was deleted between pages (or a sparse GSI omits it), the match silently fails and pagination **restarts from page 1** — duplicate delivery on AWS's most-tested pagination contract.

**Change.** Resolve the cursor by **key-order position** (first item strictly after `ExclusiveStartKey` in the operation's sort order) so a vanished cursor item degrades exactly as real DynamoDB does. Also add the page-size truncation AWS implies (1 MB accumulated / explicit `Limit`) so unbounded single-page responses stop. Coordinates with [storage-access A3](./storage-access-plan.md) (keyset paging wants the same by-position semantics); the semantic fix here must not wait for A3.

**Accept when** the failing-first test — page 1, delete the last-returned item, page 2 — yields no duplicates and no gap.

### G3 — `serviceutil.Paginate` invalid-token contract (H1's first payoff)

One change to [pagination.go:41,70-80](../../internal/serviceutil/pagination.go) plus three call-site error mappings repairs CloudFormation `DescribeStackEvents`, CloudFront ×4, and SSM ×3 — the silent-restart class. Each service maps to its own AWS error (`ValidationError` for CFN Query protocol, `InvalidArgument` for CloudFront, `InvalidNextToken` for SSM — verify each against AWS docs in the PR).

### G4 — S3 `ListParts` / `ListMultipartUploads`: response shape is missing truncation fields entirely

**Evidence.** [handler_multipart.go:56-84, 359-431](../../internal/services/s3/handler_multipart.go): no `MaxParts`/`PartNumberMarker`/`MaxUploads`/`KeyMarker` handling, and the XML response structs don't declare `IsTruncated`/`NextPartNumberMarker`/`NextKeyMarker` at all — a wire-shape gap, not just behavior (real AWS caps parts at 1000/page, 10 000/upload). Also fold in the v2/v1 object-listing invalid-token fix (garbage `ContinuationToken` currently silently means "from the start"; AWS returns `InvalidArgument`).

### G5 — DynamoDB `ListTables`: honor the params it already declares

**Evidence.** Request struct has `Limit`/`ExclusiveStartTableName`; the handler discards the request entirely ([handler.go:283-308](../../internal/services/dynamodb/handler.go)); no `LastEvaluatedTableName` in the response. AWS: default/cap 100, `LastEvaluatedTableName` echo. The service-doc caveat this audit exposed has already been corrected ([docs/services/dynamodb.md](../services/dynamodb.md)); remove it when this lands. Small, mechanical, high-visibility.

### G6 — `FilterLogEvents` limit + nextToken

Class B (params parsed, unused; no `nextToken` response field). Specified here, implemented together with [storage-access A4](./storage-access-plan.md)'s group-range query — the token wraps the (stream, ts, seq) cursor via H1/M1.

### G7 — EC2 `Describe*` family: zero pagination scaffolding

**Evidence.** No `NextToken` anywhere in `internal/services/ec2/` ([handler_instances.go:239-302](../../internal/services/ec2/handler_instances.go)). EC2 is one of the most-paginated real SDK surfaces. Data is bounded (emulated instances), so H1 in-memory paging is the correct fix — fidelity, not storage. Start with `DescribeInstances` (`MaxResults` default 1000, min 5 — AWS rejects `MaxResults < 5`, worth pinning), extend to the other `Describe*` ops mechanically.

### G8 — Mechanical H1 adoption across the all-class-B services  **[per-service PRs, low urgency each]**

IAM (ListUsers/Roles/Policies/Groups — 18 hardcoded `IsTruncated: false`, Query-protocol `Marker` semantics), Lambda (×3, `Marker`/`NextMarker`), SNS (×3), SQS `ListQueues` (`MaxResults`/`NextToken`), Kinesis `ListStreams` (`HasMoreStreams` hardcoded false) + `ListShards`, CloudWatch (ListMetrics/GetMetricData/DescribeAlarms), logs `DescribeLogGroups`/`DescribeLogStreams`, ECS ×2, EventBridge `ListRules` (params missing from request struct), Secrets Manager `ListSecrets` (request is `struct{}`), Step Functions `ListStateMachines`, MSK, API Gateway `GetRestApis` (`position` param; has a self-documented TODO at [handler_rest.go:140](../../internal/services/apigateway/handler_rest.go)), Cognito `ListUserPools` (AWS *requires* `MaxResults` — also add that validation). Each: H1 + H2 test + AWS default/cap citation. Bounded data throughout — in-memory paging is correct; no storage work.

### G9 — AppSync default-limit divergence  **[smallest item]**

[handler.go:138-185](../../internal/services/appsync/handler.go) already validates tokens and caps `maxResults` at 25 — but when the client omits it, default = everything. AWS default is 25. One-line default; keep their helper (see migration policy).

---

## Explicit won't-fix / defer register

- **DynamoDB Streams `DescribeStream`** shard paging — the emulator models exactly one synthetic shard; nothing to page. Class E, documented here.
- **CloudTrail `LookupEvents`** — hardcoded empty; the real gap is that nothing records lookupable events (a service-capability stub, not a pagination bug). Flag in the service doc as a stub; out of scope here.
- **ECR listings / CloudTrail `ListTrails`** — class B but datasets realistically never cross a page in local dev; fold into G8's tail or skip until someone hits it.
- **CloudFormation `DescribeStackEvents` fixed page size 20** — AWS documents no client-settable limit; current shape is acceptable once G3 fixes its token errors.

## Verification standard

Every item: failing test first (the broken behavior, e.g. token-loop duplication, pinned before the fix); H2 contract walk in the integration suite with the real SDK paginator where one exists; AWS doc citation for the op's default/cap/token names in the PR description; wire-format changes reviewed as compatibility changes per [CONTRIBUTING.md](../../CONTRIBUTING.md). CHANGELOG: one Pagination bullet extended per landing, per its inline rules.

## Proposed order of work

| Priority | Items | Why this order |
|---|---|---|
| **P0 — foundations** | **H1+G3** (canonical helper + invalid-token contract), **H2** (contract test helper) | One helper change repairs three services' silent-restart bug at once, and every later item builds on both; doing any G-item first would re-create per-service machinery this exists to prevent. |
| **P1 — broken contracts** | **G1** (GetLogEvents, the only class-D), **G2** (DynamoDB cursor duplicate-delivery) | These actively corrupt client pagination loops today (infinite re-fetch; silent duplicates) — worse than any ignored parameter. G1 can precede storage-access A4 (correctness first, efficiency after); G2's semantic fix must not wait for A3. |
| **P2 — small, high-visibility fidelity** | **G5** (DynamoDB ListTables), **G4** (S3 multipart response shapes + v2/v1 token errors), **G9** (AppSync default) | Cheap, mechanical, on surfaces users actually script against; each is an afternoon with H1/H2 in place. |
| **P3 — coordinated with storage-access-plan** | **G6** (FilterLogEvents) with **A4**; interleave with access-plan A1–A3/A5 per its own ordering | Single landing per surface: the logs work satisfies both plans at once rather than touching the same handler twice. |
| **P4 — breadth, per-service PRs** | **G7** (EC2 family first — biggest zero-scaffolding surface), then **G8** service by service (suggested within-G8 order: IAM → SQS/SNS/Lambda → Kinesis lists → CloudWatch/logs Describe* → the rest) | Mechanical H1 adoption; no dependencies between services, so parallelizable across agents and safe to schedule opportunistically. |
| **Explicitly last / never** | Won't-fix register above | Documented decisions, revisit only on user demand. |

Cross-plan note: if a single work stream executes both plans, the merged order is **H1+G3+H2 → G1+G2 → access-plan A1/A2 (correctness hazard) → G5/G4/G9 → A3+A5 → G6+A4 → A6 → G7 → G8 → A7 (design-gated)**.
