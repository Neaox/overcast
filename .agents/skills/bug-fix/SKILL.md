---
name: bug-fix
description: "Fix bugs in the Overcast codebase with TDD rigor, AWS fidelity, and full verification. Use when: fixing a bug, triaging a reported issue, investigating unexpected behaviour, or resolving a test failure."
compatibility: opencode
metadata:
  audience: contributors
  workflow: tdd
  languages: "go,typescript"
argument-hint: "Bug description, issue number, or failing test path"
license: MIT
---

# Bug Fix — Overcast

Fix bugs with a strict TDD workflow that guarantees the fix is correct, complete, and does not regress. Every fix starts with a reproducing test and ends with full verification including docs, CloudFormation, and web UI.

The project's core contract is **AWS wire compatibility** — the SDK must work unmodified. Every bug fix must honour that contract. When in doubt about real AWS behaviour, follow the [escalation strategy](#aws-behaviour-verification--escalation-strategy) — don't guess, and don't jump straight to real AWS without exhausting free sources first.

All coding standards are in [CONTRIBUTING.md](../../../CONTRIBUTING.md). Agent guardrails are in [AGENTS.md](../../../AGENTS.md). Test conventions are in [tests/AGENTS.md](../../../tests/AGENTS.md). Read all three before starting.

---

## Check Your Assumptions — Verify Before Acting

**Your knowledge may be stale.** AWS APIs evolve, dependencies update, and the codebase moves fast. Never assume you know the current state of anything without checking. Every assumption you don't verify is a potential wrong fix.

### Mandatory verification before any code change

| Assumption                                         | How to verify — escalate by cost (prefer free/fast sources first)                                                                                                                               |
| -------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| "I know what AWS returns for this operation/error" | **Escalation strategy (see below).** Never guess — but don't jump straight to real AWS either.                                                                                                  |
| "This library/utility is available"                | Search imports in the same package and sibling services. Check `go.mod` for Go, `package.json` for TypeScript. Never add a dependency without confirming it's not already there.                |
| "This is how the existing code works"              | Read the actual code — don't rely on memory or summary tool output from earlier in the session. Code may have changed, or you may have misremembered.                                           |
| "The store interface supports this"                | Read `internal/state/store.go` for the interface. Read both `memory.go` and `sqlite.go` implementations. They must stay in sync — never update just one.                                        |
| "The route is registered correctly"                | Check `RegisterRoutes` in the service's `service.go`, and `detectService` in `internal/middleware/logger.go`. Confirm with a real request or a test that hits the router.                       |
| "This is the latest version of a dependency"       | Check `go.mod`, `package.json`, or the official source. Don't recommend API patterns from an outdated version.                                                                                  |
| "The CloudFormation handler already exists"        | Search `resourceHandlers` in `internal/services/cloudformation/provisioner.go`. Check for the exact resource type string.                                                                       |
| "Make docs will update this table"                 | Verify the sentinel markers (`<!-- BEGIN overcast:capabilities -->` / `<!-- END overcast:capabilities -->`) are present in the doc file. If they're missing, `make docs` silently does nothing. |
| "The web UI uses this field"                       | Search the field name in `web/src/`. Check if components, data hooks, or SSE invalidation reference it.                                                                                         |

### AWS behaviour verification — escalation strategy

When you need to confirm how AWS behaves, escalate through these sources in order. Each tier costs progressively more in time, money, or effort. Stop as soon as you have a definitive answer.

> **Hard rule: Tier 4 (real AWS) requires explicit user permission.** Never spin up real AWS resources without asking first — even in autopilot mode. The user must explicitly consent. This is a cost and safety boundary. If you reach tier 4 and the user hasn't pre-approved it, **stop and ask.**

| Tier                                         | Source                                                        | Cost                   | When to use                                                              | What you get                                                                                                                                                                                                                                                  |
| -------------------------------------------- | ------------------------------------------------------------- | ---------------------- | ------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **1. AWS docs**                              | Official AWS API Reference, service developer guides          | Free, seconds          | Always start here                                                        | Request/response schemas, error codes, field descriptions, validation rules. Many docs include example responses with real field names and casing.                                                                                                            |
| **2. Existing Overcast code**                | Handler implementations, integration tests, compat suites     | Free, seconds          | When the operation is already partially implemented                      | Production request/response shapes already tested against real AWS. `tests/integration/<service>/` and `compat/suites/` are validated wire-level expectations. **Trust but verify — cross-reference with AWS docs (tier 1). Existing code can be wrong too.** |
| **3. Other emulators**                       | LocalStack, Floci (Java), Moto (Python), MinStack, Flick (Go) | Free, minutes          | When docs are ambiguous or missing edge cases                            | These projects have already done the research. Their test fixtures and handler logic encode real AWS behaviour. Cross-reference, don't blindly copy.                                                                                                          |
| **4. Real AWS** _(requires user permission)_ | Spin up a real resource, test with `aws --debug`              | ~$0.01–$1.00, 2–10 min | Last resort — when all cheaper tiers are exhausted and the user consents | Definitive wire-level truth. **Annotate the code with a comment linking to the evidence so future agents can skip re-researching (see below).**                                                                                                               |

**Decision rule:** If tier N gives a clear, unambiguous answer from an authoritative source, stop — don't escalate further. Only escalate when:

- Docs are silent or contradictory on the specific edge case
- Existing Overcast code doesn't cover this scenario
- Emulators disagree with each other (or all implement it differently from what seems right)
- You're implementing a brand-new operation with no prior art anywhere
- **AND (for tier 4) the user has explicitly said yes**

### Annotate verified behaviour — shortcircuit future research

When you definitively verify how AWS behaves (especially via tier 4, but also when docs are unambiguous), **leave a comment in the code** recording the source. This means the next agent (or you, in 6 months) can skip straight to implementation without re-researching.

```go
// Verified against real AWS (2026-05-04, aws-cli 2.15.x):
//   aws sqs receive-message --queue-url ... --debug
// Expected: 200 with empty <ReceiveMessageResult/> when queue is empty.
// No error response — just an empty result set.
```

Or for doc-verified behaviour:

```go
// Per AWS docs (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ReceiveMessage.html):
// Returns one or more messages (up to 10), or an empty result if no messages are available.
// Never returns an error for an empty queue.
```

**When to annotate:**

- Any behaviour that was surprising or counterintuitive
- Edge cases where AWS docs are ambiguous but real AWS confirmed the answer
- Error codes and status codes that differ from what seems reasonable
- Response shapes with non-obvious field names or nesting

**Format:** `// Verified against <source> (<date>): <concrete evidence>`. Include enough detail that a reader can trust the comment without repeating the verification.

### Other research protocols

1. **For third-party facts (versions, APIs, protocols):** The codebase is **not** the source of truth. Check the official source online — `go.mod` / `package.json` may be stale, and existing imports may reflect an outdated version. Look up the current stable release of any dependency before recommending it. For AWS API behaviour, use the escalation strategy above.
2. **For codebase conventions:** Read the nearest sibling service that implements a similar operation. Copy its structure, not your memory of it. The codebase _is_ the source of truth for how this project does things.
3. **For dependencies:** `grep` the import in the codebase _and_ check the current version online. Never add a new dependency without confirming it doesn't already exist under a different path.
4. **For store operations:** Read both `MemoryStore` and `SQLiteStore`. If they've diverged, flag it before making them diverge further.
5. **For tool output:** Treat tool results as the current truth, not cached knowledge. If a tool result conflicts with what you "know," the tool is right.

---

## When to Use

- A bug is reported or discovered (incorrect error code, wrong response shape, missing validation, broken state transition)
- A test is failing unexpectedly
- Behaviour diverges from real AWS in a way that breaks SDK clients
- A regression is suspected after a recent change

If the bug is an AWS compatibility issue, also follow `docs/compatibility/README.md` and the service tracker in `docs/compatibility/services/<service>.yaml`. Examples include wrong wire shape, wrong status code, wrong error code, missing documented validation, wrong identifier/ARN/URL format, incorrect pagination or idempotency, state transition mismatch, or behavior that only fails under a documented resource configuration such as Cognito `UsernameAttributes` or SQS long polling.

Do NOT use this for:

- Adding new endpoints or features (use the endpoint checklist in CONTRIBUTING.md)
- Refactoring without behaviour change (use the code-review skill)
- Performance optimisation without a bug (use `make bench` and profile)

---

## Bug Fix Workflow

### Phase 1 — Triage & Understand

Before writing any code, understand the bug:

1. **Reproduce it.** Get a concrete reproduction — a curl command, an SDK snippet, or a test that fails. If you can't reproduce it, you can't fix it.
2. **Verify expected behaviour.** Use the [escalation strategy](#aws-behaviour-verification--escalation-strategy) above. Start with AWS docs, then existing Overcast tests, then other emulators. Real AWS is the last resort — only when cheaper sources are silent or conflicting. Every header, status code, error code, and response field must match the authoritative source.
3. **Identify the service and protocol.** Which service? Which protocol (Query/XML, JSON 1.1, REST JSON, REST XML)? Check the error format table:

   | Service                    | Format | Helper                                  |
   | -------------------------- | ------ | --------------------------------------- |
   | S3                         | XML    | `protocol.WriteXMLError(w, r, aerr)`    |
   | SQS, SNS, DynamoDB, Lambda | JSON   | `protocol.WriteJSONError(w, r, aerr)`   |
   | Unimplemented              | —      | `protocol.NotImplementedXML/JSON(w, r)` |

4. **Check the docs.** Look at `docs/services/<service>.md` — is the affected endpoint marked as supported? If it's marked ❌, the "bug" may be an unimplemented endpoint returning 501, which is correct behaviour.
5. **For compatibility bugs, check compatibility tracking.** Read `docs/compatibility/README.md`, `docs/compatibility/matrix.yaml`, and `docs/compatibility/services/<service>.yaml`. Identify the affected operation and scenario, including documented resource configuration variants. If no scenario exists yet, add it before finishing.
6. **Check real AWS behaviour only with permission.** If docs and cheaper sources are insufficient, ask the user before using real AWS. Guessing leads to drift. The wire format is the compatibility contract — every status code, header, field name, casing, default value, and error code must match the authoritative source.

### Compatibility Bug Addendum

For AWS compatibility bugs, the fix is not complete until the service compatibility tracker is updated.

Before writing tests:

1. Read the AWS API Reference and Developer Guide sections for the affected operation.
2. Extract documented callouts and resource-configuration scenarios. Do not test only the default resource shape.
3. Update or add the affected operation/scenario in `docs/compatibility/services/<service>.yaml` with `status: implementation_mismatch` or `tests_missing` as appropriate.

Common scenario dimensions to consider:

- Default/minimal resource configuration.
- Common production-style configuration.
- Mutually exclusive modes and feature flags.
- Resource lifecycle state.
- Identifier variants: name, ARN, URL, generated ID, alias, case variants.
- Duplicate, missing, stale, deleted, expired, or in-flight resources.
- Cross-resource relationships.
- Boundary values and invalid enum values.
- All supported protocol variants.

After the fix:

1. Mark fixed scenarios as `fixed` or `reviewed` in the per-service YAML.
2. Add evidence: AWS docs URLs, test paths, and the exact behavior now covered.
3. Update remaining gaps and `next_action` so another agent can continue the review.
4. Update the global `docs/compatibility/matrix.yaml` summary only if service-level status, priority, or next action changed.

### Phase 2 — Write a Reproducing Test

Tests follow the **Given/When/Then** pattern. See [tests/AGENTS.md](../../../tests/AGENTS.md) for full conventions.

1. **Location:** Integration tests go in `tests/integration/<service>/<service>_test.go`. Unit tests go alongside the code in `internal/services/<service>/`.
2. **Naming:** `Test<Subject>_<scenario>` — the scenario describes the state or condition, not the outcome.
3. **Form:** Mark each section explicitly:

   ```go
   func TestReceiveMessage_emptyQueueReturnsEmpty(t *testing.T) {
       // Given: an empty queue
       srv := helpers.NewTestServer(t)
       queueURL := createQueue(t, srv, "test-queue")

       // When: we receive messages
       resp := receiveMessage(t, srv, queueURL)
       defer resp.Body.Close()

       // Then: the response is empty, not an error
       helpers.AssertStatus(t, resp, http.StatusOK)
       // ... assertions specific to the bug
   }
   ```

4. **The test MUST fail before the fix.** Run it and confirm it fails for the expected reason. A test that passes before the fix is not testing the bug.
5. **Assert the right things:**
   - HTTP status code (`helpers.AssertStatus`)
   - Error code in body (`helpers.AssertXMLError` / `helpers.AssertJSONError`)
   - Request ID header present (`helpers.AssertRequestID`)
   - Response shape, field names, casing, defaults match real AWS
   - Never just assert the error message — error codes are stable, messages change

### Phase 3 — Implement the Fix

**Before writing any code, verify your assumptions:**

1. Read the actual handler/stub you're modifying — don't rely on memory.
2. Search for similar patterns in sibling services — copy the established approach.
3. Check `go.mod` / `package.json` — never add a dependency without confirming it's not already available.
4. Read both `MemoryStore` and `SQLiteStore` if the fix touches state — confirm they're in sync.

Then proceed:

1. **Locate the right file.** Use the service package structure:

   | File               | Contains                                                                  |
   | ------------------ | ------------------------------------------------------------------------- |
   | `service.go`       | `Service` struct, `New`, route registration, `Dispatch`/`DispatchQuery`   |
   | `typed_ops.go`     | `typedOps()` map, `Operations()`, `SupportedProtocols()` (typed services) |
   | `typed_logic.go`   | Codec-agnostic handlers + request/response types (typed services)         |
   | `handler.go`       | Dispatcher methods + **implemented** handlers only (legacy services)      |
   | `handler_stubs.go` | `NotImplemented*` stubs only (legacy services)                            |
   | `store.go`         | State access, JSON serialisation                                          |

2. **Mirror existing patterns.** Find a similar working handler in the same service (or a sibling service with the same protocol) and copy its structure. Consistency matters more than cleverness.
3. **Apply all coding standards:**
   - **Error handling:** Wrap with `fmt.Errorf("scope: %w", err)`. Use `protocol.Wrap()` for AWS errors with underlying causes. Use `protocol.WriteXMLError` / `protocol.WriteJSONError` for HTTP responses — never `http.Error`.
   - **Clock:** Use `clock.Clock` — never `time.Now()`.
   - **State:** All through `state.Store`. Update both `MemoryStore` and `SQLiteStore` if the interface changes.
   - **Config:** Use `*config.Config` — never `os.Getenv`.
   - **Logging:** Structured with `zap`. DEBUG for per-request detail, INFO for lifecycle, WARN for handled anomalies, ERROR for 5xx/panics. Never log credentials or PII.
   - **Shared utilities:** Use `serviceutil` — never duplicate. Check `serviceutil/` before writing a helper.
   - **Performance:** Pre-size slices, stream large data, use `strings.Builder` and `strconv`, avoid unnecessary allocations.
   - **501 for unimplemented:** Never return bare `404`. Use `protocol.NotImplementedXML` / `protocol.NotImplementedJSON`.
4. **Consider all affected code paths.** A bug fix in one handler may need corresponding changes in:
   - CloudFormation resource handlers (`internal/services/cloudformation/provisioner.go`)
   - The store layer (`store.go`)
   - The logger's `detectService` function (`internal/middleware/logger.go`) — if the fix changes routing
   - Web UI (`web/src/features/<service>/`) — if the fix changes response shapes visible to the UI
   - SSE event cache invalidation (`web/src/hooks/use-event-stream.ts`) — if the fix changes resource lifecycle

### Phase 4 — Verify the Fix

1. **Run the reproducing test** — it must pass: `go test -count=1 -run TestMyFix ./tests/integration/<service>/`, it must pass reliably, and it must test the bug, not just a symptom. If it doesn't fail before the fix, or if it still fails after the fix, you don't have a valid reproducing test. Stop and write a proper one before proceeding.
2. **Run the full test suite for that service:** `go test -count=1 ./internal/services/<service>/... ./tests/integration/<service>/...`
3. **Format and vet the changed packages:**
   ```bash
   gofmt -w ./internal/services/<service>/
   go vet ./internal/services/<service>/...
   ```
4. **Check for editor problems** — use `get_errors` to confirm zero new diagnostics in the changed files.

### Phase 5 — Update Documentation & Metadata

Documentation MUST stay in sync with behaviour. Skipping this creates drift that confuses contributors.

1. **`capabilities_dev.go`:** If the fix changes the implementation status of any operation (e.g., a stubbed handler is now implemented), update the `Status` field. Then regenerate:
   ```bash
   make generate-caps   # regenerates internal/capabilities/all.gen.go
   make docs            # rewrites capability tables in docs/services/<service>.md
   make check-caps      # verifies dispatcher entries have matching capabilities
   ```
2. **Service docs prose:** If there are behaviour notes or caveats outside the `<!-- BEGIN/END overcast:capabilities -->` sentinel markers in `docs/services/<service>.md`, update them. Never edit anything between the sentinel markers — those are auto-generated.
3. **Compatibility tracker:** If the bug was an AWS compatibility issue, update `docs/compatibility/services/<service>.yaml` with scenarios, evidence, tests, gaps, and handoff. Update `docs/compatibility/matrix.yaml` if the service-level next action or status changed.
4. **`CHANGELOG.md`:** Add an entry under `[Unreleased]`:

   ```markdown
   ### Fixed

   - SQS: `ReceiveMessage` now correctly returns empty result for empty queues (#123)
   ```

5. **`STATUS.md`:** Regenerated automatically by `make docs`. Do not edit by hand.

### Phase 6 — Web UI Check

Web UI must not be an afterthought. A bug fix may affect it:

- **Does the fix change response shapes?** If the UI renders fields that changed format, casing, or structure, update the corresponding components in `web/src/features/<service>/` and query options in `data.ts`.
- **Does the fix change resource lifecycle?** If create/delete timings or state transitions changed, update topology nodes in `internal/router/topology.go` and SSE cache invalidation in `web/src/hooks/use-event-stream.ts`.
- **Does the fix affect a service's home screen?** Every service list page must include `ServiceDocsButton` in its `PageHeader` actions. Confirm the page is not broken.
- **Does the fix affect global search?** If resource identifiers or fetch logic changed, update the search contributor in `web/src/lib/search-contributors/<service>.ts`.
- Run `npx tsc --noEmit` in `web/` to confirm no TypeScript regressions.

### Phase 7 — Final Verification

1. **Widen the build:**
   ```bash
   go build ./...
   go vet ./...
   ```
2. **Run the full test suite with race detector:**
   ```bash
   make test
   ```
3. **Run lint:**
   ```bash
   make lint
   ```
4. **Confirm zero new problems** in the workspace diagnostics (`get_errors`).

---

## Common Bug Categories & Diagnostic Patterns

### Wrong error code / wrong HTTP status

- **Symptom:** AWS SDK throws an unexpected exception type.
- **Diagnosis:** Use the [escalation strategy](#aws-behaviour-verification--escalation-strategy) to find the correct error code. Start with AWS docs (tier 1), then check Overcast's own integration tests. The error code in the body (`Code` field for XML, `__type` for JSON) and the HTTP status code must match the authoritative source.
- **Common causes:** Returning `404` instead of `501` for unimplemented ops; using a generic error when AWS returns a specific one; incorrect status mapping.

### Routing fallthrough to S3

- **Symptom:** Logs show `service=s3` for a request that should go to another service.
- **Diagnosis:** S3 is the catch-all handler. Check:
  - Is the route registered in the service's `RegisterRoutes`?
  - Is the path prefix in `detectService` (`internal/middleware/logger.go`)?
  - Is there a typo in a route path or `chi.URLParam` name?
  - Is middleware mutating the URL?
- **Fix:** Fix the actual routing, then add the path prefix to `detectService`. Never patch only `detectService` without confirming the route is correct.

### Missing CloudFormation handler

- **Symptom:** `cdk deploy` fails for a resource type that the service supports.
- **Diagnosis:** Check `resourceHandlers` in `internal/services/cloudformation/provisioner.go`. Every resource-creating endpoint needs a handler entry.
- **Fix:** Add a handler. If the service can't be fully provisioned yet, use `&stubResourceHandler{}` as a minimum.

### State store inconsistency

- **Symptom:** Behaviour differs between `memory` and `hybrid`/`persistent` state backends.
- **Diagnosis:** One of `MemoryStore` or `SQLiteStore` was updated without the other. Both implementations must stay in sync.
- **Fix:** Apply the same logic to both. Share test suites where possible.

### Time-dependent behaviour

- **Symptom:** Test passes sometimes, fails other times; behaviour depends on wall-clock time.
- **Diagnosis:** `time.Now()` used directly instead of injected `clock.Clock`.
- **Fix:** Use `clock.Clock` interface. In tests, use `helpers.WithMockClock()` and advance via `srv.Clock.Add(d)`.

### Store not using clock

- **Symptom:** Timestamps in stored data don't match test expectations.
- **Diagnosis:** The store or handler bypasses the injected clock.
- **Fix:** Pass `clock.Clock` to store methods that need timestamps. Store structs should accept a clock in their constructor.

### Wire format incompatibility

- **Symptom:** AWS SDK fails to parse the response.
- **Diagnosis:** Response field names, casing, nesting, or XML namespace doesn't match real AWS.
- **Fix:** Use the [escalation strategy](#aws-behaviour-verification--escalation-strategy) to find the correct response shape. Start with AWS API docs (tier 1) — they often include example responses. Then check Overcast's integration tests and compat suites. If escalation reaches tier 4 and the user consents, capture the real AWS response with `aws --debug`. Every field matters. Use protocol writers (`protocol.WriteXML`, `protocol.WriteJSON`, `protocol.WriteAWSJSON`) — never ad-hoc `json.Marshal` + header writing.

### Missing validation

- **Symptom:** Invalid input accepted silently, creating resources with bad names or invalid parameters.
- **Diagnosis:** AWS rejects this input with a specific error. Our handler doesn't validate.
- **Fix:** Add validation matching real AWS rules. Use `serviceutil` helpers (`RequireString`, `BucketName`, etc.) where applicable. Return the same error code AWS returns.

---

## Quick Reference

```bash
# Reproduce the bug (failing test)
go test -count=1 -run TestMyFix ./tests/integration/<service>/

# Fix, then run scoped tests
go test -count=1 ./internal/services/<service>/... ./tests/integration/<service>/...

# Format and vet
gofmt -w ./internal/services/<service>/
go vet ./internal/services/<service>/...

# Check for workspace problems
# (use get_errors tool)

# Regenerate docs if capabilities changed
make generate-caps && make docs && make check-caps

# Web UI typecheck (if UI touched)
npx tsc --noEmit -p web/

# Final verification
make test
make lint
```

## What Agents Must NOT Do During Bug Fixes

- Never fix a bug without a reproducing test first — TDD is mandatory
- Never spin up real AWS resources without explicit user permission — even in autopilot mode. Stop and ask at tier 4 of the escalation strategy. The user must consent.
- Never return bare `404` — unimplemented operations must return `501`
- Never change wire formats without tests — request/response shapes are the compatibility contract
- Never update only one store implementation — `MemoryStore` and `SQLiteStore` must stay in sync
- Never call `os.Getenv` in service code — use `*config.Config`
- Never use `time.Now()` — use `clock.Clock`
- Never bypass `serviceutil` / duplicate helper logic — DRY across services
- Never manually edit auto-generated doc tables — use `make docs`
- Never edit `web/src/routeTree.gen.ts` — it is auto-generated
- Never leave the workspace in a broken state — run `go build ./...` and `go vet ./...` before finishing
- Never skip CloudFormation consideration — if a resource-creating endpoint was affected, check handlers in `provisioner.go`
