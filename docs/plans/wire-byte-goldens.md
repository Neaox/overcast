# Wire-Byte Golden Test Infrastructure

> Status: §5.1 priority services complete (as of 2026-08-23, #1204). The
> harness ([tests/helpers/golden.go](../../tests/helpers/golden.go)) and pilot
> goldens for **ssm, backup, and kinesis** landed in #306. The implementation
> diverges from §4.2 in one deliberate way: it does not stand up a separate
> "legacy" server — it records whatever path currently serves the request
> (today the legacy dispatch path for the hybrid services, and the only path
> for services — STS/IAM/EC2 — with no typed/legacy split at all), and relies
> on `helpers.WithMockClock()` for determinism. All five §5.1 priority
> services now have goldens: **SQS** (the only one of the five serving both
> JSON and Query/XML — golden'd on both), **DynamoDB**, **STS**, **IAM**, and
> **EC2**. §5.3's per-service CI step was evaluated and deliberately NOT
> added — see "CI wiring" below. P6+ (goldens for the remaining Phase 5/6
> services) remains the long one-PR-per-service tail described in §5.1's
> table; not part of #1204's scope.
>
> CI wiring: every golden test in this repo (ssm/backup/kinesis from #306,
> plus sqs/dynamodb/sts/iam/ec2 from #1204) already runs as part of
> `.github/workflows/test.yml`'s `coverage` job (`go test -race
> -coverprofile=coverage.out ./...`, line ~162) and the three-way `build-tags`
> matrix (`go test -tags ${{ matrix.tags }} ./...`, line ~213) — both required
> checks on every PR. A wire-format regression in any of these eight services'
> goldens already fails CI today; no dedicated `go test -run TestGolden`
> step was added on top, since that would only re-run a subset of coverage
> those two jobs already provide on every push, and this repository's CI
> deliberately avoids that kind of redundant job (see test.yml's own comments
> on `vet`/`coverage` dropping unneeded dependencies for the same reason). The
> `sqs-protocol-dispatch` job is not a counterexample — it sets
> `OVERCAST_PROTOCOL_DISPATCH=1`, exercising a code path the default sweep
> does not, which is why it earns a dedicated step.
>
> Non-determinism found and normalized (§4.4) while implementing #1204, for
> future services to check for up front rather than discover mid-implementation:
> - **SQS**: QueueUrl embeds the httptest-assigned ephemeral port; Query/XML
>   embeds the request ID in the body (JSON only puts it in the excluded
>   header) — both regex-normalized.
> - **STS**: every operation except GetCallerIdentity mints
>   crypto/rand-backed AccessKeyId/SecretAccessKey/SessionToken (and
>   AssumeRole's role-session ID) on every call, not just between record and
>   assert — normalized. Query/XML's body-embedded request ID is pinned via a
>   caller-supplied `x-amzn-requestid` header instead of a regex.
> - **IAM**: CreateRole/CreatePolicy/CreateUser mint crypto/rand resource IDs
>   (RoleId/PolicyId/UserId) on every call — normalized. Same body-embedded
>   request ID as STS, on every operation (not just the ID-minting ones).
> - **EC2**: every resource ID (instance/VPC/subnet/security-group/ENI/
>   reservation/CIDR-association/DHCP-options) is minted by `shortID()`
>   (8 random hex chars) on every call, including the default VPC seeded on
>   first read — normalized uniformly by prefix. `RunInstances`/
>   `DescribeInstances`' PrivateIPAddress is derived from a package-level
>   `atomic.Uint32` (`syntheticIPCounter`, handler.go) shared by every EC2
>   operation in the test binary and never reset between test servers — its
>   value depends on test execution order across the whole package, so it is
>   normalized too rather than relied upon to stay stable under an unfiltered
>   `go test ./tests/integration/ec2/...`. Because every Describe* op here
>   also reads from an ID-ordered store scan, each Describe* golden filters to
>   the one resource the test just created (by ID) rather than trying to
>   normalize cross-resource ordering.
> - **DynamoDB** needed no normalization at all — no `crypto/rand`,
>   `math/rand`, or `uuid.New` anywhere in its response path.
>
> AWS-divergences noticed while recording (not enshrined as correct — flagged
> in PR #1204's description for a RICE-scored follow-up per
> [backlog-rice.md](backlog-rice.md) §2, not fixed here):
> - DynamoDB's CreateTable/DescribeTable response has no `TableId` field at
>   all (real AWS always includes one).
> - SQS's Query/XML protocol emits an empty `<XResult></XResult>` wrapper for
>   every no-output operation (SetQueueAttributes, TagQueue, UntagQueue,
>   PurgeQueue, DeleteQueue); real AWS omits the wrapper entirely when there's
>   no output shape.
> - STS's `AssumeRole`/`AssumeRoleWithWebIdentity` build `AssumedRoleUser.Arn`
>   as `arn:aws:sts::<account>:assumed-role/<RoleSessionName>/<RoleSessionName>`
>   — repeating the session name in both path segments instead of using the
>   role name parsed from `RoleArn`.
> - EC2's `RunInstances`/`DescribeInstances` hardcode `ownerId
>   123456789012`, while `DescribeSecurityGroups` hardcodes a different
>   literal, `000000000000` — neither is derived from `cfg.AccountID`.
> - EC2's `CreateVpc` mints a real `dopt-XXXXXXXX` DhcpOptionsId, but a
>   subsequent `DescribeVpcs` for the same VPC reports it empty.
>
> Related: [smithy.md](../dev/smithy.md) — moved from docs/plans to docs/dev
> in #286; its former §8 testing-strategy section no longer exists.

## 1. Problem

Every Phase 5/6 service migration keeps the **legacy dispatch path** for its
native JSON or XML protocol — only CBOR goes through the typed dispatcher.
Before a service can route its native protocol through typed operations and
eventually **delete** the legacy handlers, we must prove the typed path emits
**identical wire bytes** to the legacy path.

Without goldens, a field-ordering change, an empty-vs-null discrepancy, or a
missing XML namespace silently breaks a client and nobody notices until the
compat suite fails — often in CI on a different PR.

## 2. Goals

1. **One golden test file per migrated service.** Captures the exact HTTP
   response body, status code, and key headers for every operation.
2. **Byte-identical comparison.** The golden test asserts `got == want` at the
   byte level — not structural comparison, not "acceptable variation."
3. **Self-contained recording.** A single test binary flag (`-record`) writes
   goldens from the legacy path. Without the flag, it asserts them against the
   typed path. This means the golden files ARE the test data.
4. **CI-enforceable.** Any PR that touches a service's handler or typed logic
   runs the golden test; regressions block the PR.
5. **Deterministic.** Golden tests cover only operations with deterministic
   output (no UUIDs, timestamps, random IDs). Operations with randomness get
   a separate "normalize" function or are excluded with justification.

## 3. Non-goals

- **Not per-operation structural comparison.** Golden tests compare raw HTTP
  response bytes, not parsed JSON/XML — this catches ALL differences including
  whitespace, header ordering, and unnoticed serialization changes.
- **Not message-level coverage for SQS/SNS.** Message APIs (SendMessage,
  ReceiveMessage, Publish) produce UUIDs and timestamps and are excluded from
  byte-level goldens. They get behavioral integration tests instead.
- **Not a replacement for integration tests.** Goldens sit ALONGSIDE existing
  tests — they're an additional safety net, not a substitute.

## 4. Infrastructure design

### 4.1 Golden file format

One `.golden` directory per service:

```
tests/integration/<service>/goldens/
    CreateQueue.json
    GetQueueUrl.json
    ListQueues.json
    ...
```

Each file contains a JSON document:

```json
{
  "status": 200,
  "headers": {
    "Content-Type": "application/x-amz-json-1.0"
  },
  "body": "{\"QueueUrl\":\"http://...\"}"
}
```

The `headers` map includes only headers that are deterministic and relevant:
- `Content-Type` (protocol indicator)
- `x-amzn-requestid` is **excluded** (changes per request)
- Service-specific headers included if deterministic

### 4.2 Golden test helper

A shared helper in `tests/helpers/golden.go`:

```go
package helpers

// GoldenTest compares the typed-dispatch response bytes for an action
// against a recorded golden file.  Use -record to write goldens.
//
//	srv := helpers.NewTestServer(t)
//	helpers.GoldenTest(t, srv, "sqs", "CreateQueue",
//	    map[string]any{"QueueName": "test"}, legacyCall)
func GoldenTest(t *testing.T, srv *TestServer, goldenDir, action string,
    body map[string]any, legacyCall func(*TestServer, string, map[string]any) *http.Response)
```

Workflow:
1. Creates a "legacy" server (protocol dispatch disabled for recording) IF
   the `-record` flag is passed. Otherwise, uses the passed-in (typed) server.
2. Calls the operation via `legacyCall`.
3. Captures status, headers, body.
4. If `-record`: writes golden file.
5. If not `-record`: reads golden file, byte-compare.

### 4.3 `-record` flag

```go
// In golden.go
var recordFlag = flag.Bool("record", false, "write golden response files")
```

Usage:
```sh
# Record goldens for SQS
go test -run TestGolden ./tests/integration/sqs/ -record

# Assert goldens
go test -run TestGolden ./tests/integration/sqs/
```

### 4.4 Normalization for non-deterministic responses

Some operations return deterministic structure but non-deterministic values
(e.g., timestamps in `CreatedAt`, process-bound ARNs). A `normalize` function
redacts these before comparison:

```go
func normalizeSQSResponse(service string, body []byte) []byte {
    // Replace generated QueueUrl host:port with canonical placeholder
    body = regexp.MustCompile(
        `http://[\d.]+:\d+/(\d+/[\w-]+)`).ReplaceAll(body, []byte("http://localhost:0/$1"))
    return body
}
```

Each service defines its own `normalize*Response` if needed. The normalized
output is what gets compared.

## 5. Per-service implementation

### 5.1 Priority order

Golden tests are implemented in Phase 5/6 migration order, biggest impact first:

| Priority | Service      | Ops  | Deterministic ops | Rationale                                             | Status |
| -------- | ------------ | ---- | ----------------- | ----------------------------------------------------- | ------ |
| P1       | SQS          | 20   | ~14               | Bellwether; already has golden test pattern           | Done (#1204) — 11 ops × JSON+Query + 1 error |
| P2       | DynamoDB     | 17   | ~12               | Highest traffic; all typed                            | Done (#1204) — 9 ops + 1 error |
| P3       | STS          | 5    | 5                 | Small, correct response format is critical for auth   | Done (#1204) — all 5 ops + 1 error |
| P4       | IAM          | 61   | ~50               | Large surface; central to CloudFormation              | Done (#1204) — 9 highest-value ops + 1 error |
| P5       | EC2          | 64   | ~40               | Largest API; high downstream impact                   | Done (#1204) — 8 highest-value ops + 1 error |
| P6+      | All others   | —    | —                 | One PR per service; batch small services              | Not started |

### 5.2 Template for a new service golden test

```
tests/integration/<svc>/golden_test.go:

func TestGolden_CreateFoo(t *testing.T) {
    srv := helpers.NewTestServer(t)
    helpers.GoldenTest(t, srv, "mysvc", "CreateFoo",
        map[string]any{"Name": "test-foo"},
        func(s *helpers.TestServer, action string, body map[string]any) *http.Response {
            return mySvcCall(t, s, action, body)  // existing legacy call helper
        })
}
```

### 5.3 CI wiring

**Decision (#1204): no dedicated per-service step was added.** The design
this section originally sketched — one `go test -run TestGolden` step per
service — assumed golden tests needed their own step to be CI-enforced at
all. They don't: every golden test file (ssm/backup/kinesis since #306, plus
sqs/dynamodb/sts/iam/ec2 since #1204) is an ordinary `_test.go` file with no
build tag exclusions, so it already executes inside
`.github/workflows/test.yml`'s `coverage` job (`go test -race
-coverprofile=coverage.out ./...`) and its three-way `build-tags` matrix
(`go test -tags ${{ matrix.tags }} ./...`) — both required checks on every
push and pull request. A wire-format regression in any golden already fails
CI today. Adding eight redundant `-run TestGolden` steps on top would re-run
work those two jobs already do on every commit, which is exactly the
per-job-cost discipline `test.yml`'s own comments describe for `vet` and
`coverage` (see the file's header comments on why `vet` dropped its `web`
dependency, and why `build-tags` has no `-race`). The one existing per-service
step in this file, `sqs-protocol-dispatch`, is not a counterexample: it sets
`OVERCAST_PROTOCOL_DISPATCH=1`, exercising a route the default sweep never
does, which is what earns it a dedicated job.

Revisit this if a future service's golden tests need something the default
sweep can't provide (e.g. a special env var or a longer timeout) — at that
point a dedicated step is the same well-justified exception
`sqs-protocol-dispatch` already is, not scope creep.

## 6. Acceptance per service

Every golden test PR must:

- [ ] Golden test file exists at `tests/integration/<svc>/golden_test.go`
- [ ] Golden response files committed at `tests/integration/<svc>/goldens/`
- [ ] `go test -run TestGolden ./tests/integration/<svc>/` passes without `-record`
- [ ] Golden files were recorded from the legacy dispatch path
- [ ] Non-deterministic fields documented in the test file with normalization
  function
- [ ] Deterministic-only operations listed in the test file; excluded operations
  listed with reason (e.g. "message API uses UUIDs")

## 7. Relationship to removing legacy handlers

The golden test is the **final gate** before a service's legacy handlers can be
deleted. The sequence per service is:

1. Capture goldens from legacy path → commit
2. Route native JSON/XML through typed dispatch → golden test asserts
   byte-identical output
3. Delete legacy handlers → golden test still passes
4. Remove `dispatchLegacy` fallback → golden test still passes

Only after step 4 is the service fully on typed dispatch for ALL protocols.

## 8. Risks & mitigations

| Risk | Mitigation |
|------|-----------|
| Golden file churn from intentional changes | Golden files ARE the spec — changing them is a deliberate PR with review |
| Flaky tests from non-deterministic values | Normalization functions defined per service; excluded ops listed |
| Golden files too large to review | Only deterministic operations captured; review diff of `.golden` directory |
| Recording requires legacy path (which is deleted) | Record before deleting legacy handlers — golden test is the LAST safety net |
