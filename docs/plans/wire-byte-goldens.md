# Wire-Byte Golden Test Infrastructure

> Status: proposal. Owner: TBD. Related: [smithy.md §8](./smithy.md#8-testing-strategy).

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

| Priority | Service      | Ops  | Deterministic ops | Rationale                                             |
| -------- | ------------ | ---- | ----------------- | ----------------------------------------------------- |
| P1       | SQS          | 20   | ~14               | Bellwether; already has golden test pattern           |
| P2       | DynamoDB     | 17   | ~12               | Highest traffic; all typed                            |
| P3       | STS          | 5    | 5                 | Small, correct response format is critical for auth   |
| P4       | IAM          | 61   | ~50               | Large surface; central to CloudFormation              |
| P5       | EC2          | 64   | ~40               | Largest API; high downstream impact                   |
| P6+      | All others   | —    | —                 | One PR per service; batch small services              |

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

Add to `.github/workflows/test.yml`:

```yaml
- name: Golden tests (SQS)
  run: go test -count=1 ./tests/integration/sqs/ -run TestGolden
- name: Golden tests (DynamoDB)
  run: go test -count=1 ./tests/integration/dynamodb/ -run TestGolden
# ... one matrix entry per service with goldens
```

CI runs WITHOUT `-record` — it asserts. Recording is manual (developer writes
goldens once and commits them).

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
