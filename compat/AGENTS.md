# AGENTS.md — Overcast Compatibility Tests

> Conventions for AI agents and contributors working in `compat/`.
> For the project-wide conventions see the root [AGENTS.md](../AGENTS.md).
> For the wire format and architecture see [README.md](./README.md).
>
> Each suite has its own `AGENTS.md` for runtime/language/library specifics.
> **Read both this file and the suite's `AGENTS.md` before making any changes.**
> See the [Suite-specific conventions](#suite-specific-conventions) section for
> what every suite `AGENTS.md` must contain, and links to each one.

---

## Purpose of this directory

`compat/` is a **black-box external test harness** for Overcast. Each suite
uses its AWS tool (SDK, CLI, CDK, IaC) **without any modification** — the only
difference from talking to real AWS is the endpoint URL (`http://localhost:4566`
instead of `https://*.amazonaws.com`). The emulator has no knowledge that compat
exists, and compat has no knowledge of Overcast's internals.

Its job:

1. Measure overall AWS compatibility across services and clients.
2. Catch regressions when emulator internals change.
3. Provide a shared benchmark for “what’s left to implement”.
4. Drive the compat server and UI that visualise the results.

Failing tests are **normal and expected** for unimplemented services. The goal
of a compat test is not “the suite passes” but “the suite accurately reflects
reality”.

---

## Separation boundary — non-negotiable

The boundary between `compat/` and the Overcast emulator is absolute:

| Allowed                                      | Forbidden                                                 |
| -------------------------------------------- | --------------------------------------------------------- |
| HTTP calls to a running Overcast instance    | Importing anything from `internal/`                       |
| Reading `compat/result.go` types             | Importing `router/`, `middleware/`, `protocol/`, `state/` |
| Sharing nothing with `web/`                  | Adding compat routes to the Overcast server               |
| Building a standalone binary in `cmd/compat` | Adding compat config to `internal/config`                 |
| A separate `compat/ui/` Vite app             | Adding compat pages to `web/src/`                         |

If you find yourself touching anything outside `compat/` or `cmd/compat/`, stop
and reconsider the approach.

---

## Core principles (compat-specific)

1. **Tests use the SDK/CLI/CDK exactly as production code would.** The only
   configuration change is the endpoint URL. No special Overcast-only headers,
   no internal client factories, no test-only SDK modes. If real application
   code wouldn't do it, neither should a compat test.

2. **Tests must never mock the AWS SDK or the emulator.** Every call goes over
   the wire to a real Overcast instance. No `jest.mock`, no HTTP intercept.

3. **Tests cover all services, not just implemented ones.** A 501 response that
   causes the SDK to throw is recorded as `"fail"` — that's the correct result.
   Never `skip` a test because a feature isn't implemented yet. Only `skip` when
   the test requires external infrastructure that isn't guaranteed (e.g. Docker
   for Lambda invocation).

4. **Each group is independently runnable.** A group must not depend on state
   left by another group. Use setup/teardown to create and destroy resources.

5. **Teardown must be fault-tolerant.** Wrap every delete in `try/catch` (or
   `//nolint:errcheck` in Go, `except Exception: pass` in Python) so partial
   failures don't block cleanup of other resources.

6. **Resource names must be unique per run.** Use the `ctx.runId` prefix (e.g.
   `oc-{runId}-s3-crud-bucket`) to avoid conflicts between concurrent runs.

7. **Tests assert meaningful state, not just "no error".** After creating a
   resource, verify it appears in the corresponding List call. After writing
   data, verify it can be read back. See the [Assertion contract](#assertion-contract) section below for the full
   requirement table — this is a hard rule, not a guideline.

8. **No sleep/polling unless strictly necessary.** If an operation is truly
   async (Lambda cold start, SQS visibility timeout), use a short poll loop
   with a maximum retry count rather than a fixed `sleep()`.

9. **Do not hard-code the emulator endpoint.** Always use `ctx.endpoint`
   (Node.js) or `cfg.Endpoint` (Go).

---

## Assertion contract

Every test function **must** verify the server's observable state — not just
that the call did not throw. A test that fires an API call and returns without
inspecting the response is incomplete. These are the hard requirements:

### Required roundtrips by operation pattern

| Operation type                                               | Required assertion                                                                                                               |
| ------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------- |
| `Create*` / `Put*`                                           | Call the matching `Describe*`, `Get*`, or `List*` and verify the resource appears with the correct field values                  |
| `Update*` / `Set*Attributes` / `TagResource`                 | Call `Describe*` / `Get*Attributes` / `List*Tags*` and verify the changed field now holds the new value                          |
| `Delete*` / `Untag*`                                         | Call `Describe*` / `List*` and verify the resource is absent; or call the direct `Get*` and assert the expected not-found error  |
| `Put*` on data plane (S3 object, SQS message, DynamoDB item) | Call `Get*` / `Receive*` / `GetItem` and verify the returned value matches what was stored                                       |
| `Publish` (SNS, EventBridge)                                 | If testing cross-service delivery (e.g. SNS→SQS), poll the destination with a retry loop and assert at least one message arrives |

### Checking response fields (minimum bar)

- A `Create*` response **must** assert at least: ARN/ID is non-empty, name
  matches what was requested.
- A `List*` response **must** check: result list is non-empty **and** contains
  the resource created in setup or earlier in the group.
- A `Describe*` / `Get*` response **must** assert the key identifying field
  (name, ARN, value) plus any field that was just mutated by an `Update*`.
- A `Scan` / `Query` response **must** assert `Count >= expected_seed_count`.

### Exceptions

The following are the **only** acceptable cases for a test with no roundtrip:

1. The test exercises an operation that has no observable side-effect visible via
   any other API call (e.g. `GenerateDataKey`, `GetRandomPassword` — verify the
   returned value's shape/length instead).
2. The operation is specifically testing a negative path (e.g. expecting a
   specific error code on a bad request), in which case asserting the error
   code and message is the assertion.
3. The operation is on a service that is entirely stubbed (returns 501) — in
   that case the test is expected to fail and there is nothing to assert.

### Anti-patterns to reject in code review

- `_, err := callAPI(...)` followed by `return err` — response discarded.
- `await client.send(new SomeCommand(...))` with no assignment and no further
  assertion — silently passes regardless of what the server returned.
- `_ = output` / `_ = resp` after a call — result discarded.
- Checking only `if resp.SomeId == nil` when the test is about a mutation — the
  ID was already set before the mutation; asserting its non-nil-ness doesn't
  verify the mutation happened.
- Relying on test ordering to provide coverage: `UpdateX` has no assertion
  because "the next test `GetX` will catch it". If `UpdateX` is broken, it must
  fail at `UpdateX` — not pass through and cause `GetX` to report the failure.

---

## Teardown rules (apply to ALL suites)

These rules are canonical and apply to every suite — cli, go-sdk, python-sdk,
node-js-sdk. Each suite's own `AGENTS.md` may add language-specific detail but
must never contradict or weaken these requirements.

1. **Every group that creates at least one durable resource must have a
   teardown.** The only acceptable exception is a group that is entirely
   read-only (e.g. `GetCallerIdentity`, `DescribeKey`). Tests that delete a
   resource inline as the last step of a happy-path sequence are **not** a
   substitute for teardown — teardown exists precisely to handle the cases
   where that last step is skipped (test failure, early return, etc.).

2. **Clean up ALL resources, including incidental ones.** If a test creates a
   resource as a side effect of testing something else — an access key created
   to test `CreateAccessKey`, a subscription created when subscribing a queue
   to a topic, an inline policy attached to a user, a role added to an instance
   profile — that resource **must** also be cleaned up in teardown. Do not rely
   on the parent resource's delete to cascade unless AWS documents the cascade
   explicitly (deleting a DynamoDB table removes all items; deleting a log
   group removes all streams). When the cascade is not documented, add an
   explicit delete call.

3. **Tear down in reverse creation order.** Dependencies must be removed before
   the resource that owns them. Examples: detach role from instance profile
   before deleting the profile; delete objects before deleting the bucket;
   delete subnet before deleting the VPC; remove targets before deleting an
   EventBridge rule; deregister task definitions before deleting an ECS cluster.

4. **Resources that require pre-conditions before deletion must handle them.**
   Examples: disable a CloudFront distribution before deleting it; fetch a
   fresh `LockToken` before deleting a WAF Web ACL (the token changes after
   each mutating call); cancel a pending KMS key deletion before rescheduling;
   detach or delete a managed policy before deleting the IAM role.

5. **Incomplete multipart uploads are invisible to `ListObjectsV2`.** If a
   group creates or might leave an in-progress multipart upload, teardown must
   call `ListMultipartUploads` (or equivalent) and abort each one before
   attempting to delete the bucket.

6. **KMS aliases are NOT deleted when the key is scheduled for deletion.** Any
   group that creates a KMS alias must explicitly call `DeleteAlias` before (or
   as well as) scheduling the key for deletion.

7. **SQS FIFO queues use a different suffix (`.fifo`)** — make sure teardown
   references the correct queue name / URL stored in context, not a hardcoded
   standard name.

8. **Teardown must not throw.** Wrap individual deletes so that one failure does
   not prevent subsequent deletes from running.

---

## Wire format contract

Every suite runner **must** emit valid NDJSON to stdout matching the schema in
[README.md](./README.md). The Go runner (`compat/runner.go`) parses this output
line-by-line — malformed lines are silently skipped and a warning is logged.

Invariants:

- Exactly one `run_start` event, as the first line.
- Exactly one `run_end` event, as the last line.
- One `test_result` per test case, emitted immediately after the test completes.
- `duration_ms` is always present and non-negative.
- `error` is only present (and non-empty) when `status` is `"fail"` or `"unimplemented"`.
- `"unimplemented"` means the emulator returned HTTP 501 — it is never used for
  assertion failures or unexpected errors. `"fail"` means something that should
  work didn't.

---

## Compat server contract

The compat server (`compat/server.go`) exposes the NDJSON event stream over
HTTP using **Server-Sent Events (SSE)**, not polling and not WebSockets.

### `GET /events`

- `Content-Type: text/event-stream`
- Each SSE message is a single JSON object on a `data:` line, using the same
  event shapes as the internal NDJSON wire format.
- The server buffers all events from the current (or last completed) run in
  memory. A client that connects after the run has started receives all buffered
  events immediately, then live events as they arrive.
- The stream stays open after `run_end` so the UI can reconnect when a new run
  starts without a page reload.

### `GET /results`

- Returns the latest completed `RunReport` as a single JSON object.
- Returns `204 No Content` if no run has completed yet.
- Intended for CI badge generation and one-shot queries.

### `GET /`

- Serves the embedded `compat/ui/` static bundle.

### Rules for the compat server

- Must never import anything from `internal/`, `router/`, `middleware/`, or
  `state/`.
- Must not connect to the Overcast emulator itself — it only receives events
  from the runner and serves them to the UI.
- SSE connections must respect `r.Context()` cancellation (client disconnect)
  to avoid goroutine leaks.

### `POST /mcp/` — MCP server (agent interface)

The compat server embeds an **MCP (Model Context Protocol)** server at `/mcp/`
that AI agents can use to trigger test runs and query results without parsing
raw SSE streams or JSON files.

**Transport:** Streamable HTTP — JSON-RPC 2.0 over `POST /mcp/`, with an
optional SSE stream at `GET /mcp/sse` for live event notifications.

**When the compat dev server is running** (`./dev.sh` in `compat/`), the MCP
endpoint is live at `http://localhost:7777/mcp/`. Agents with an MCP client
configured can call tools directly. Agents without an MCP client can call the
endpoint via `curl`:

```bash
# List all available tools
curl -s -X POST http://localhost:7777/mcp/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq '.result.tools[].name'

# Run all node-js-sdk tests
curl -s -X POST http://localhost:7777/mcp/ \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"compat_run_tests","arguments":{"suite":"node-js-sdk","all":true}}}'
```

**Available tools:**

| Tool                   | Description                                                                                   | Key parameters                                |
| ---------------------- | --------------------------------------------------------------------------------------------- | --------------------------------------------- |
| `compat_list_suites`   | List all suite runners and their state (`building`, `ready`, `busy`, `error`, `stopped`).     | —                                             |
| `compat_list_services` | List all AWS services from the registry with group and test counts.                           | —                                             |
| `compat_list_tests`    | List tests from the registry with last result status, filterable.                             | `service`, `group`, `suite`                   |
| `compat_get_results`   | Get test results, filterable by suite/service/group/test/status.                              | `suite`, `service`, `group`, `test`, `status` |
| `compat_get_queue`     | Show tests currently queued or running across all suites.                                     | —                                             |
| `compat_run_tests`     | Queue tests for execution. Returns `batch_id`, `queued`, `skipped_duplicates`.                | `all`, `suite`, `service`, `group`, `test`    |
| `compat_run_failing`   | Re-run all tests that failed in the last run.                                                 | `suite`, `service`                            |
| `compat_cancel`        | Cancel queued or running tests.                                                               | `batch_id`, `suite`, `group`, `test`, `all`   |
| `compat_reload_suite`  | Hot-reload a suite runner (rebuilds the suite, restores queued tests). Requires `suite` name. | `suite` (required)                            |

**Preferred workflow for agents:** use `compat_run_tests` to trigger a run,
then poll `compat_get_queue` until the queue empties, then call
`compat_get_results` (filtered by `status: "fail"`) to see what to fix. This
is preferable to reading `compat-results.json` directly because it reflects
real-time state rather than the last completed run.

**Rules for the MCP server:**

- The `/mcp/` handler lives in `compat/mcp.go` — do not add MCP logic to any
  other file.
- `POST /mcp/` responds synchronously (JSON-RPC result or error). Long-running
  operations (`compat_run_tests`) return immediately with a `batch_id`; the
  actual test output arrives via SSE.
- `GET /mcp/sse` streams the same event feed as `GET /events` — do not add a
  second SSE pump; reuse the orchestrator's existing channel.

---

## Running suites (Docker / CI)

All suites are designed to run cross-platform via Docker. No local toolchain
(Node.js, Go, etc.) is required.

```bash
# From repo root — works on any machine with Docker
docker compose -f compat/docker-compose.yml run --rm compat

# Or via the Makefile shorthand
make -C compat ci
```

Each suite image is independently buildable:

```bash
# Build just the node-js-sdk image
docker build -t overcast-compat-node-js-sdk compat/suites/node-js-sdk
```

The `docker-compose.yml` in `compat/` wires up:

1. **overcast** — the emulator, health-checked before compat tests start.
2. **compat** — the Go CLI runner (`cmd/compat`) that spawns suite subprocesses.

Suite images are pre-built and injected via `OVERCAST_COMPAT_NODEJS_IMAGE`
so the Go runner can `docker run` them, directing stdout to its NDJSON parser.

### Cross-platform rules for suite authors

- Do **not** use shell scripts as entry points (`sh -c "..."`) — use a proper
  language runtime command in `CMD` to avoid platform-specific shell differences.
- Do **not** use `#!/bin/sh` shebangs in TypeScript; rely on `node --import tsx/esm`.
- Do **not** hard-code `/tmp` paths; use `os.tmpdir()` / Node's `tmp` utilities.
- All suite images derive from official multi-arch base images (`node:20-alpine`,
  `golang:1.24-alpine`) and build cleanly on `amd64` and `arm64`.

---

## Suite-specific conventions

Every suite **must** have both an `AGENTS.md` and a `README.md` at its root.
These are two distinct documents with different audiences and purposes — never
merge them.

### README.md vs AGENTS.md — what goes where

| Document    | Audience         | Purpose                                                                                                                                                                                                                                  |
| ----------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `README.md` | Human developers | Human-facing project documentation: what the suite is, what it covers, current status, prerequisites, how to run it locally and via Docker, environment variable reference, architecture diagram.                                        |
| `AGENTS.md` | AI agents        | Machine-targeted implementation instructions: how to add a test group, exact code conventions, file layout, key types, group anatomy with working code examples, teardown rules specific to the suite's language, explicit prohibitions. |

**README.md is the entry point for humans.** It answers "what is this?" and
"how do I run it?". It should be readable in isolation. It should **not**
contain code conventions or agent-facing checklists.

**AGENTS.md is the entry point for agents.** It answers "how do I implement
this?" and "what must I never do?". It should **not** repeat information
already in `README.md` (prerequisites, env vars, quick-start). Link to
`README.md` for those. For suites that are not yet implemented, `AGENTS.md`
must also include an **implementation checklist** that an agent can follow to
build the suite from scratch.

### README.md required sections

Every suite `README.md` must contain at minimum:

| Section                   | Content                                                                             |
| ------------------------- | ----------------------------------------------------------------------------------- |
| **Title + status**        | Suite name, technology, and current status (implemented / planned).                 |
| **What it covers**        | Which AWS services / operations / lifecycle phases the suite verifies.              |
| **Prerequisites**         | Required tools, language runtimes, and CLIs — with install instructions or links.   |
| **Running the suite**     | Three paths: locally (native toolchain), via Docker, via the Go CLI (`cmd/compat`). |
| **Environment variables** | Table of all `OVERCAST_*` variables plus any suite-specific env vars.               |
| **Architecture**          | Annotated directory tree and brief description of key modules.                      |

### What a suite AGENTS.md must cover

Use [suites/node-js-sdk/AGENTS.md](suites/node-js-sdk/AGENTS.md) as the
canonical reference. At minimum, every suite `AGENTS.md` must document:

| Section                     | Content                                                                                                                                                                                         |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **What this suite tests**   | Which AWS client/tool (SDK version, CLI version, etc.) and which column of the compat matrix it represents.                                                                                     |
| **Status**                  | `Implemented` or `Planned`. For planned suites, include an implementation checklist at the end.                                                                                                 |
| **Runtime**                 | Language version, AWS library name and version, CI base image.                                                                                                                                  |
| **File layout**             | Annotated directory tree so contributors know where to put new files.                                                                                                                           |
| **Group anatomy**           | A worked example showing the exact structure of a service group — setup, teardown, and one or two test functions — in that suite's language.                                                    |
| **Key types / interfaces**  | The `TestContext` fields (endpoint, region, runId, log, state bag), `TestGroup`/`ServiceGroup` shape, and how test functions signal pass/fail.                                                  |
| **Naming conventions**      | Group name format (`<service>-<feature>`), resource name prefix pattern, context key style, file name rules, export/function name style.                                                        |
| **Inter-test state**        | How to pass data between sequential tests within a group (context bag); rules about what must/must not be stashed.                                                                              |
| **Teardown rules**          | Suite-specific additions and gotchas on top of the canonical rules in this file (e.g. which helpers to use for S3 bucket emptying, how to suppress errors in Go, paginator patterns in Python). |
| **Error messages**          | How assertion errors should be formatted so failures are actionable.                                                                                                                            |
| **Adding a new group**      | Step-by-step checklist: create file → implement group → register in runner → verify wire output.                                                                                                |
| **What agents must NOT do** | Hard prohibitions specific to this suite (e.g. "never construct SDK clients inside test functions", "never call sys.exit", "no require() in Node.js").                                          |

Current suite AGENTS.md files (implemented suites):

- [suites/node-js-sdk/AGENTS.md](suites/node-js-sdk/AGENTS.md)
- [suites/cli/AGENTS.md](suites/cli/AGENTS.md)
- [suites/go-sdk/AGENTS.md](suites/go-sdk/AGENTS.md)
- [suites/python-sdk/AGENTS.md](suites/python-sdk/AGENTS.md)

Planned suite AGENTS.md files (implementation guide for agents building each suite):

- [suites/cdk/AGENTS.md](suites/cdk/AGENTS.md)
- [suites/dotnet-sdk/AGENTS.md](suites/dotnet-sdk/AGENTS.md)
- [suites/java-sdk/AGENTS.md](suites/java-sdk/AGENTS.md)
- [suites/pulumi/AGENTS.md](suites/pulumi/AGENTS.md)
- [suites/rust-sdk/AGENTS.md](suites/rust-sdk/AGENTS.md)
- [suites/terraform/AGENTS.md](suites/terraform/AGENTS.md)
- [suites/tofu/AGENTS.md](suites/tofu/AGENTS.md)

When adding a new suite, create **both** `AGENTS.md` and `README.md` before writing any group code.

### registry.json — canonical test matrix

`compat/suites/registry.json` is the **single source of truth** for every
group and test that any suite should implement. It lists all services, group
names, and individual test names across the entire compat matrix.

```
compat/suites/
  registry.json         ← canonical list of all groups + tests
  registry.schema.json  ← JSON Schema for the registry
```

**Rules for every suite:**

- A suite must implement the groups listed in `registry.json` **for the
  services its tool supports**. For example, `rust-sdk` only covers the nine
  core services listed in its README — it should still register the remaining
  groups but emit `"skip"` for them so the dashboard shows a consistent matrix.
- When a suite has not yet implemented a group, it must emit a `test_result`
  with `status: "skip"` for every test in that group — never simply omit the
  group from the output.
- Group names and test names in suite implementations **must exactly match**
  the registry (`name` fields are case-sensitive). The dashboard joins results
  across suites using these names.
- The `op` field on a test entry is the AWS API operation being exercised
  (absent when it matches `name`). Suites may use it for display or filtering
  but must still use the `name` field as the test identifier.

**Rules for modifying the registry:**

- Adding a new group or test to `registry.json` is the first step when
  implementing a new service or operation — do it before writing suite code.
- Never remove or rename an existing group or test entry — that breaks the
  dashboard history. If an operation is no longer relevant, mark it with
  `"deprecated": true` instead (field is supported by the schema).
- Bump the `version` field only for breaking schema changes; adding new groups
  is non-breaking and does not require a version bump.

### When a new Overcast service is implemented

Every new service **must** have a corresponding compat group added at the same
time as the implementation:

1. Add the new service's groups and tests to `compat/suites/registry.json`.
2. Create `compat/suites/node-js-sdk/src/groups/<service>.ts` with test cases
   matching the registry group/test names exactly.
3. Register the new group in `compat/suites/node-js-sdk/src/index.ts`.
4. If the service has CLI support, add a matching group to `compat/suites/cli/`.

Do not open a PR that adds a new service without also updating the registry and
adding its compat group. Compat tests are the external contract check —
integration tests alone are not sufficient.

---

## Go runner conventions

- The Go runner in `compat/runner.go` starts each suite as a subprocess.
- It reads NDJSON from the subprocess stdout line by line.
- It surfaces `stderr` from subprocesses as `WARN`-level log lines.
- Suite processes are run sequentially by default; `--parallel` flag is planned.

### Adding a new Go suite

Implement `Suite` interface in a new file under `compat/`:

```go
type Suite interface {
    Name() string
    // Command returns the argv to run (first element is the executable).
    Command(cfg RunConfig) []string
    // Env returns additional environment variables for the subprocess.
    Env(cfg RunConfig) []string
}
```

---

## Reading compat results — how agents should use the report

After a compat run the full results are written to **`/workspace/compat-results.json`**
(path is configurable via `OVERCAST_COMPAT_RESULTS_FILE`). This file is the
canonical source of truth for the current state of compatibility. It is **not**
the raw NDJSON event stream — it is the aggregated `RunReport` built by
`compat/runner.go`.

### Getting an actionable summary

The fastest way to turn the report into something actionable is:

```bash
# From the workspace root — no Docker, no running server needed:
make compat-report

# Or equivalently:
go run ./cmd/compat --report

# Point at a non-default file:
go run ./cmd/compat --report --results-file /path/to/results.json
```

This prints three sections:

| Section                    | What to do with it                                                                                                                          |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| **UNIMPLEMENTED SERVICES** | Lists every service where the emulator returned HTTP 501, grouped by service directory. These are prioritised implementation targets.       |
| **GENUINE FAILURES**       | Tests that should work but returned the wrong response or a non-501 error. Investigate `internal/services/<service>/`.                      |
| **CASCADE FAILURES**       | Tests that failed only because an earlier step in the same group failed. Fix the genuine failure above first; cascades should self-resolve. |

### JSON schema for direct parsing

If you need to parse the file directly (e.g. to filter by suite or service):

```python
import json
data = json.load(open("compat-results.json"))
# Top-level keys: Endpoint, StartedAt, FinishedAt, Suites
for suite in data["Suites"]:          # suite["Suite"] = "go-sdk", "cli", …
    for group in suite["Groups"]:     # group["Name"] = "s3-crud", "sqs-queues", …
        for test in group["Tests"]:   # test["status"] = "pass"|"fail"|"unimplemented"|"skip"
            if test["status"] in ("fail", "unimplemented"):
                print(f"{suite['Suite']}/{group['Name']}/{test['test']}: {test.get('error','')}")
```

Full field reference for each `test` entry:

| Field         | Type   | Notes                                                               |
| ------------- | ------ | ------------------------------------------------------------------- |
| `event`       | string | Always `"test_result"`                                              |
| `suite`       | string | e.g. `"go-sdk"`                                                     |
| `service`     | string | e.g. `"s3"`, `"sqs"`                                                |
| `group`       | string | Group name e.g. `"s3-crud"`                                         |
| `test`        | string | Test name e.g. `"CreateBucket"`                                     |
| `op`          | string | AWS operation name (may differ from `test`)                         |
| `status`      | string | `"pass"`, `"fail"`, `"unimplemented"`, `"skip"`                     |
| `duration_ms` | int    | Elapsed time in milliseconds                                        |
| `error`       | string | Only present (non-empty) when `status` is `fail` or `unimplemented` |

### Interpreting `fail` vs `unimplemented`

- **`unimplemented`** — the emulator returned HTTP 501. The error message identifies
  the exact AWS target, e.g. `"Unknown target: Kinesis_20131202.CreateStream"`. The
  fix is to implement or stub the endpoint in `internal/services/<service>/`. These
  are always expected gaps — never treat them as urgent bugs.

- **`fail`** — the emulator returned a non-501 response that caused the test to fail.
  Sub-categories:

  | Error pattern                                                                    | Likely cause                                | Fix                                                                                    |
  | -------------------------------------------------------------------------------- | ------------------------------------------- | -------------------------------------------------------------------------------------- |
  | `"ResourceAlreadyExists"`, `"BucketAlreadyOwnedByYou"`, `"Table already exists"` | Orphan from a previous run; teardown failed | Fix teardown in the suite group; run `make -C compat ci` (which rebuilds from scratch) |
  | `"no <resource> from <PreviousOp>"`                                              | Cascade — earlier step failed               | Fix the root cause (the operation listed in `GENUINE FAILURES`)                        |
  | `"Error parsing parameter '--body'"`                                             | CLI group passes a file path incorrectly    | Fix in `compat/suites/cli/internal/groups/<service>.go`                                |
  | AWS error on a supposedly implemented op                                         | Emulator bug                                | Investigate `internal/services/<service>/handler*.go`                                  |

### Mapping a failing test to emulator code

1. From the test's `service` field (e.g. `"sqs"`), find `internal/services/sqs/`.
2. Check `internal/services/sqs/handler.go` for the handler method (search for the operation name).
3. If the method is in `handler_stubs.go`, the operation is not yet implemented — add it to `handler.go`.
4. For cross-suite failures (same operation fails in `go-sdk` AND `cli`), the bug is certainly in the emulator, not the suite.

### Cross-suite signal

When the same group/operation fails in **multiple suites**, that strongly
indicates an emulator bug rather than a suite authoring error. A single-suite
failure is more likely a suite bug (wrong resource name, missing teardown,
wrong parameter). Use `make compat-report` to quickly see which suites hit each failure.

---

## What agents must NOT do in compat/

### Separation boundary

- **Never import from `internal/`** — compat has zero dependency on the emulator source tree.
- **Never add routes, middleware, or handlers to the Overcast server** for compat purposes.
- **Never add compat pages, components, or API calls to `web/`** (the Overcast UI) — the compat UI lives in `compat/ui/` and is served by the compat server only.
- **Never add compat configuration to `internal/config/`**.
- **Never reference `cmd/overcast/` or `internal/` from any compat file**.

### Runner and suite behaviour

- Never start Overcast inside a test group — the runner manages the emulator lifecycle.
- Never use `process.exit()` inside a test function — throw instead.
- Never write to stdout inside a test function — use `ctx.log()` which writes to stderr.
- Never add dependencies that require native binaries (e.g. `node-gyp`) to any suite.
- Never skip a test to hide a gap — let it run, let it fail, record the result.

### Compat server and UI

- The compat server (`compat/server.go`, served by `cmd/compat`) must never import Overcast internals.
- The compat UI (`compat/ui/`) must only fetch from the compat server, never from the Overcast emulator directly.
- Do not embed compat UI assets into the Overcast binary (`cmd/overcast`).

---

## SDK version pinning & upgrade strategy

Every suite **must pin** its AWS SDK version to a specific, reproducible
version — never use floating tags (`latest`, `^x.y.z` in npm, `>=x.y.z` in
pip). Pinning ensures that CI results are identical across every machine and
every day. A compat test that passes with SDK v3.1020.0 must still pass with
the same SDK a month later; the only variable is the emulator.

### Pinned versions by suite

| Suite        | File                      | Pinned version                                        |
| ------------ | ------------------------- | ----------------------------------------------------- |
| node-js-sdk  | `package.json`            | `@aws-sdk/client-*` `^3.1020.0`                       |
| python-sdk   | `requirements.txt`        | `boto3>=1.34.0`, `botocore>=1.34.0`                   |
| go-sdk       | `go.mod`                  | `github.com/aws/aws-sdk-go-v2 v1.41.5` (+ `config/*`, `service/*`) |
| dotnet-sdk   | `OvercastCompat.csproj`   | `AWSSDK.*` `4.0.0`                                    |
| java-sdk     | `pom.xml`                 | `software.amazon.awssdk` (BOM-managed)                 |
| rust-sdk     | `Cargo.toml`              | `aws-sdk-*` `=1.x` (exact, e.g. `=1.65.0`)            |
| cli          | `Dockerfile`              | AWS CLI v2 (pinned via base image tag)                 |
| cdk          | `package.json`            | `aws-cdk-lib` / `aws-cdk` (v2, pinned in package.json) |

### Upgrade procedure

1. **Do not upgrade unprompted.** SDK upgrades are rare — only trigger one when
   a suite hits an SDK bug (fixed in a newer point release), or when a new
   major version brings material API surface that the suite should exercise.

2. **Upgrade one suite at a time.** Open a separate PR for each suite's SDK
   bump so CI can isolate regressions.

3. **Update the pin file and the AGENTS.md simultaneously.** Every version
   change in `package.json` / `Cargo.toml` / `.csproj` / `go.mod` /
   `requirements.txt` must be paired with an update to the pinned version
   table in that suite's own `AGENTS.md`.

4. **Full re-run against latest emulator `main`.** After bumping, trigger a
   complete run of the affected suite and verify zero new failures:
   - Zero new `fail` results (regressions)
   - Zero new `unimplemented` results (API shape changes may rename operations)
   - Any status change from the previous run must be explainable and documented
     in the PR body.

5. **Dockerfile compatibility.** If the SDK bump requires a newer runtime
   (e.g. .NET 9, Node.js 22), update the base image tag in the Dockerfile in
   the same PR. Do not leave a suite with a runtime that cannot execute the
   SDK it declares.

6. **Notification list.** When upgrading, mention the suite maintainers in
   the PR description so they can spot-check the API diffs. A breaking change
   in the SDK's public API may require test code changes that only a human
   familiar with the service can validate.
