# Compat Suite Runner Protocol

> **Status:** Definitive
> **Version:** 1.0
> **Audience:** Implementors of suite runners in any language

---

## 1. Overview

The interactive compat test runner uses a **long-lived worker process** model.
Each suite (e.g. `node-js-sdk`, `python-sdk`, `cli`) is spawned as a child
process that:

1. Starts idle, performing any necessary setup (dependency installation, compilation).
2. Signals readiness to the orchestrator.
3. Accepts test commands on **stdin** and emits results on **stdout**.

Communication in both directions is **NDJSON** — newline-delimited JSON. Each
message is a single JSON object terminated by `\n` (U+000A). Messages MUST NOT
contain embedded newlines within the JSON.

### Mode Detection

Suites detect interactive mode via the environment variable:

```
OVERCAST_COMPAT_INTERACTIVE=1
```

When this variable is **absent or not `"1"`**, suites run in **legacy batch
mode** (current behavior: execute all tests, emit results, exit). When set,
suites enter the interactive stdin/stdout protocol described in this document.

### Streams

| Stream   | Direction            | Format | Purpose                          |
| -------- | -------------------- | ------ | -------------------------------- |
| `stdin`  | orchestrator → suite | NDJSON | Commands (run, cancel, shutdown) |
| `stdout` | suite → orchestrator | NDJSON | Events (results, lifecycle)      |
| `stderr` | suite → orchestrator | text   | Diagnostics, debug logs          |

The orchestrator MUST NOT parse stderr for protocol messages. Suites MUST NOT
write protocol events to stderr.

---

## 2. Stdout Events (suite → orchestrator)

Every event is a JSON object with a required `"event"` field that identifies its
type. Unknown event types MUST be ignored by the orchestrator (forward
compatibility).

### Event Reference

| Event            | Emitted When                         | Required Fields                                                                    | Optional Fields |
| ---------------- | ------------------------------------ | ---------------------------------------------------------------------------------- | --------------- |
| `building`       | Suite is installing deps / compiling | `suite`, `message`                                                                 |                 |
| `ready`          | Suite is idle and accepting commands | `suite`, `total_tests`                                                             |                 |
| `test_start`     | A test begins executing              | `suite`, `service`, `group`, `test`                                                |                 |
| `test_result`    | A test finished                      | `suite`, `service`, `group`, `test`, `status`, `duration_ms`                       | `error`, `op`   |
| `batch_complete` | All tests in a submitted batch done  | `suite`, `batch_id`, `passed`, `failed`, `skipped`, `unimplemented`, `duration_ms` |                 |
| `cancelled`      | A test was cancelled                 | `suite`, `batch_id`, `group`, `test`                                               | `reason`        |
| `pong`           | Suite responded to a `ping` command  | `suite`, `running_test`                                                            |                 |
| `error`          | Fatal suite error                    | `suite`, `error`                                                                   |                 |

### Field Definitions

- **`suite`** (`string`): Suite identifier, e.g. `"node-js-sdk"`.
- **`message`** (`string`): Human-readable status message.
- **`total_tests`** (`integer`): Total number of tests declared in the suite registry.
- **`service`** (`string`): AWS service name, e.g. `"s3"`, `"sqs"`.
- **`group`** (`string`): Test group name, e.g. `"s3-buckets"`, `"sqs-queues"`.
- **`test`** (`string`): Individual test name, e.g. `"CreateBucket"`.
- **`status`** (`string`): One of `"pass"`, `"fail"`, `"skip"`, `"unimplemented"`, `"na"`.
- **`duration_ms`** (`number`): Wall-clock duration in milliseconds.
- **`error`** (`string`): Error message or stack trace.
- **`op`** (`string`): AWS operation name, e.g. `"PutObject"`.
- **`batch_id`** (`string`): Opaque identifier assigned by the orchestrator.
- **`passed`**, **`failed`**, **`skipped`**, **`unimplemented`** (`integer`): Counts for the batch.
- **`reason`** (`string`): Cancellation reason — one of `"user"`, `"dependency"`, `"batch"`.

### Examples

#### `building`

```json
{
  "event": "building",
  "suite": "node-js-sdk",
  "message": "Installing dependencies…"
}
```

```json
{
  "event": "building",
  "suite": "node-js-sdk",
  "message": "Compiling TypeScript…"
}
```

#### `ready`

```json
{ "event": "ready", "suite": "node-js-sdk", "total_tests": 147 }
```

#### `test_start`

```json
{
  "event": "test_start",
  "suite": "node-js-sdk",
  "service": "s3",
  "group": "s3-buckets",
  "test": "CreateBucket"
}
```

#### `test_result`

Pass:

```json
{
  "event": "test_result",
  "suite": "node-js-sdk",
  "service": "s3",
  "group": "s3-buckets",
  "test": "CreateBucket",
  "status": "pass",
  "duration_ms": 42,
  "op": "CreateBucket"
}
```

Fail:

```json
{
  "event": "test_result",
  "suite": "node-js-sdk",
  "service": "sqs",
  "group": "sqs-queues",
  "test": "SendMessage",
  "status": "fail",
  "duration_ms": 118,
  "op": "SendMessage",
  "error": "Expected MessageId to be defined, got undefined"
}
```

Skip:

```json
{
  "event": "test_result",
  "suite": "node-js-sdk",
  "service": "dynamodb",
  "group": "dynamodb-tables",
  "test": "UpdateTimeToLive",
  "status": "skip",
  "duration_ms": 0
}
```

Unimplemented:

```json
{
  "event": "test_result",
  "suite": "node-js-sdk",
  "service": "s3",
  "group": "s3-versioning",
  "test": "GetBucketVersioning",
  "status": "unimplemented",
  "duration_ms": 0
}
```

#### `batch_complete`

```json
{
  "event": "batch_complete",
  "suite": "node-js-sdk",
  "batch_id": "batch-001",
  "passed": 12,
  "failed": 1,
  "skipped": 2,
  "unimplemented": 0,
  "duration_ms": 3842
}
```

#### `cancelled`

```json
{
  "event": "cancelled",
  "suite": "node-js-sdk",
  "batch_id": "batch-001",
  "group": "s3-buckets",
  "test": "DeleteBucket",
  "reason": "user"
}
```

```json
{
  "event": "cancelled",
  "suite": "node-js-sdk",
  "batch_id": "batch-001",
  "group": "s3-buckets",
  "test": "PutBucketPolicy",
  "reason": "dependency"
}
```

#### `error`

```json
{
  "event": "error",
  "suite": "node-js-sdk",
  "error": "Cannot connect to Overcast endpoint at http://localhost:4566: ECONNREFUSED"
}
```

---

## 3. Stdin Commands (orchestrator → suite)

Every command is a JSON object with a required `"command"` field. Suites MUST
ignore unknown command types (forward compatibility).

### Command Reference

| Command    | Fields                         | Behavior                                                                                           |
| ---------- | ------------------------------ | -------------------------------------------------------------------------------------------------- |
| `run`      | `batch_id`, `tests?`           | Execute specified tests (or all if `tests` omitted); emit lifecycle events; emit `batch_complete`. |
| `cancel`   | `batch_id?`, `group?`, `test?` | Cancel matching queued or running work.                                                            |
| `ping`     | _(none)_                       | Respond immediately with a `pong` event on stdout. Used as a liveness check by the orchestrator.   |
| `shutdown` | _(none)_                       | Graceful exit.                                                                                     |

### `run`

Execute a set of tests. The `tests` field is optional:

- **If `tests` is absent or `null`**: the suite MUST run **every** test in the
  suite, sorted by group name. This is the "run all" case sent by the
  orchestrator when the user triggers a full suite run.
- **If `tests` is a non-empty array**: each entry names a `group` and
  optionally a `tests` array of individual test names within that group. If
  `tests` is omitted for a group entry, the suite MUST run **all** tests in
  that group.

The suite emits `test_start` before each test and `test_result` after it
completes. When every test in the batch has finished (or been cancelled), the
suite MUST emit `batch_complete` — even if zero tests were selected.

**Run all tests** (omitted `tests` field):

```json
{
  "command": "run",
  "batch_id": "batch-001"
}
```

**Run specific groups/tests:**

```json
{
  "command": "run",
  "batch_id": "batch-002",
  "tests": [
    { "group": "s3-buckets", "tests": ["CreateBucket", "DeleteBucket"] },
    { "group": "sqs-queues" }
  ]
}
```

The above runs two specific tests from `s3-buckets` and all tests from
`sqs-queues`.

A minimal single-group invocation:

```json
{
  "command": "run",
  "batch_id": "batch-003",
  "tests": [{ "group": "dynamodb-tables", "tests": ["CreateTable"] }]
}
```

### `ping`

Sent by the orchestrator when a suite has been silent for >10 seconds while
a test is running. The suite MUST respond with a `pong` event within 5 seconds.
If no pong arrives, the orchestrator cancels the stuck test.

```json
{ "command": "ping" }
```

**Response (stdout):**

```json
{ "event": "pong", "suite": "node-js-sdk", "running_test": "s3-crud:CreateBucket" }
```

The `running_test` field tells the orchestrator which test is currently
executing (useful for stall diagnosis).

### `cancel`

Cancel queued or in-progress work. The scope of cancellation depends on which
fields are provided:

| Fields Provided      | Scope                                 |
| -------------------- | ------------------------------------- |
| `batch_id` only      | Cancel **all** tests in the batch.    |
| `group` + `test`     | Cancel a **single** test.             |
| `batch_id` + `group` | Cancel all tests in that group/batch. |

Cancelled tests emit a `cancelled` event. See §5 for the full cancellation
contract.

Cancel an entire batch:

```json
{ "command": "cancel", "batch_id": "batch-001" }
```

Cancel a specific test:

```json
{ "command": "cancel", "group": "s3-buckets", "test": "DeleteBucket" }
```

### `shutdown`

Request graceful termination. The suite MUST:

1. Finish the currently executing test (do not abort mid-assertion).
2. Cancel all queued tests (emit `cancelled` for each).
3. Run all pending cleanup/teardown.
4. Emit any remaining `test_result`, `cancelled`, and `batch_complete` events.
5. Exit with code **0**.

```json
{ "command": "shutdown" }
```

---

## 4. Suite Process Lifecycle

```
  ┌─────────────────────────────────────────────────────┐
  │                   ORCHESTRATOR                       │
  │                                                      │
  │  spawn(suite)                                        │
  │      │                                               │
  │      ▼                                               │
  │  ┌────────┐   building    ┌────────┐   ready         │
  │  │ SETUP  │──────────────▶│  IDLE  │────────────────▶│ (read loop)
  │  └────────┘   (stdout)    └────────┘   (stdout)      │
  │                               ▲                      │
  │                               │ batch_complete       │
  │                    ┌──────────┘                       │
  │                    │                                  │
  │              ┌───────────┐                            │
  │   run ──────▶│ EXECUTING │──── test_start/result ───▶│
  │   (stdin)    └───────────┘    (stdout)               │
  │                    │                                  │
  │   cancel ──────────┤                                  │
  │   shutdown ────────┤                                  │
  │                    ▼                                  │
  │              ┌───────────┐                            │
  │              │ TEARDOWN  │──── exit 0                 │
  │              └───────────┘                            │
  └─────────────────────────────────────────────────────┘
```

### Phase 1: Setup

The process starts and performs initialization: installing dependencies,
compiling source, loading the test registry, connecting to the Overcast
endpoint.

During this phase the suite emits zero or more `building` events to keep the
orchestrator (and user) informed of progress.

### Phase 2: Ready

When setup is complete, the suite emits a single `ready` event containing
`total_tests` (the number of tests declared in the suite's registry). The
orchestrator MUST NOT send commands before receiving `ready`.

### Phase 3: Command Loop

The suite enters a non-blocking stdin read loop. It MUST be able to:

- Read commands from stdin **without blocking** test execution. Commands and
  test execution run concurrently (e.g. separate goroutine, async reader,
  thread).
- **Run tests off the main command-loop thread.** The stdin read loop is the
  hot-path — it must never be blocked by a running test. If a test blocks the
  event loop, the suite cannot respond to `ping`, `cancel`, or `shutdown`
  commands, and the orchestrator will mark the suite as stalled after 10 seconds
  of silence.
- Process `run` commands by adding tests to an internal queue and executing
  them, emitting `test_start` and `test_result` for each.
- Process `ping` commands immediately — respond with a `pong` event on stdout
  containing the `running_test` field.
- Process `cancel` commands at any time, including while tests are running.
- Process `shutdown` at any time.

Multiple `run` commands may be sent before a prior batch completes. The suite
MUST handle concurrent batches or queue them — it MUST NOT drop commands.

### Phase 4: Shutdown

On receiving `shutdown` (or stdin EOF), the suite performs graceful teardown
and exits with code 0. See the `shutdown` command specification in §3.

---

## 5. Cancellation Contract

Cancellation is cooperative. Suites MUST implement the following behavior.

### Individual Test Cancellation

A test is in one of two states when `cancel` arrives:

| State       | Behavior                                                                                                                                                         |
| ----------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Queued**  | Remove from queue immediately. Emit `cancelled` with the appropriate `reason`.                                                                                   |
| **Running** | Signal cancellation (e.g. `AbortSignal` in JS, `context.Cancel` in Go). The in-flight SDK call aborts. Emit `cancelled` once the test acknowledges cancellation. |

### Dependency Cascade

A cancelled test is treated as a **failure** for the purpose of `depends`
checks. If test B declares `depends: ["A"]` and test A is cancelled, test B
is automatically skipped with `reason: "dependency"`.

This cascade is transitive: if C depends on B and B is skipped due to A's
cancellation, C is also skipped.

### Independent Siblings

Tests that do not depend on the cancelled test continue executing normally.
Cancellation MUST NOT affect unrelated tests.

### Group Teardown

Group teardown (cleanup) **always runs**, regardless of cancellation. Even if
every test in a group is cancelled, the group's teardown MUST execute to clean
up any resources created during setup.

### Batch Cancellation

Cancelling a `batch_id` is equivalent to cancelling every test in that batch.
The suite emits `cancelled` for each affected test, then emits
`batch_complete` with the final counts.

---

## 6. Cleanup Contract

Resource cleanup (teardown) is critical for test isolation. Suites MUST adhere
to the following rules.

### Fault Tolerance

Teardown MUST be fault-tolerant. A failed cleanup step:

- Logs the error to **stderr** (not stdout).
- Does **not** throw, panic, or abort the process.
- Does **not** prevent subsequent cleanup steps from running.

### Execution Order

Cleanup runs in **reverse creation order**. If setup created resources A, B, C
(in that order), teardown deletes C, B, A.

### Cancellation

Cleanup runs even when tests are cancelled. Cancellation of a test does not
skip its group's teardown.

### Example

```
Setup:    create bucket → create queue → put object
Teardown: delete object → delete queue → delete bucket
```

If `delete queue` fails (e.g. 404 because it was already deleted), the suite
logs the error to stderr and proceeds to `delete bucket`.

---

## 7. Implementation Requirement

**Every suite runner MUST implement the interactive protocol.** This is not
optional — the orchestrator starts all registered suites as long-lived
processes and communicates exclusively via stdin/stdout NDJSON. A runner that
ignores `OVERCAST_COMPAT_INTERACTIVE=1` and runs all tests immediately will
cause the dashboard to trigger a full test run on startup, which defeats the
purpose of on-demand execution.

### Dual-Mode Requirement

Each runner MUST support both modes:

| Env Var                         | Mode        | Behavior                                                                         |
| ------------------------------- | ----------- | -------------------------------------------------------------------------------- |
| `OVERCAST_COMPAT_INTERACTIVE=1` | Interactive | Full protocol: `building`, `ready`, command loop, `batch_complete`, `cancelled`. |
| _(absent or other value)_       | Batch       | Legacy behavior: run all tests, emit `test_start`/`test_result` on stdout, exit. |

When `OVERCAST_COMPAT_INTERACTIVE=1` is set, the runner MUST:

1. **NOT run any tests automatically.** Start idle.
2. Emit `building` events during setup (dependency install, compilation).
3. Emit a single `ready` event when setup is complete.
4. Enter the stdin command loop and wait for `run` commands.
5. On `run` with **no `tests` field** (or `tests: null`): execute **all** tests sorted by group name.
6. On `run` with a `tests` array: execute only the specified groups/tests.
7. Support `cancel` and `shutdown` commands.

When the variable is absent, the runner continues to work in legacy batch mode
(run all tests, emit results, exit).

Both modes emit the same `test_start` and `test_result` events on stdout. The
interactive mode **adds** the following events that are not emitted in batch
mode:

- `building`
- `ready`
- `batch_complete`
- `cancelled`
- `pong`

The orchestrator detects the mode by the presence or absence of the `ready`
event. In batch mode, test results begin arriving immediately without a
preceding `ready`.

### Implementation Checklist (per language)

To add interactive mode to a runner:

1. Check `OVERCAST_COMPAT_INTERACTIVE` at startup (env var).
2. If set, emit `building` → `ready` and enter a stdin read loop.
3. Parse NDJSON lines from stdin into command objects.
4. On `run` with **no `tests` field** (or `tests: null`/empty): execute all
   tests in the suite, sorted by group name.
5. On `run` with a non-empty `tests` array: look up the requested groups/tests
   and execute them, emitting `test_start`, `test_result`, and `batch_complete`.
6. On `cancel`: abort in-flight tests (AbortSignal / context cancel) and
   remove queued tests. Emit `cancelled` for each.
7. On `shutdown` (or stdin EOF): cancel pending work, run teardown, exit 0.
8. On any stdin parse error: log to stderr, skip the line, continue.

The batch-mode code path remains unchanged — wrap it in an `else` branch.

### Protocol Compliance Checklist

Every suite runner MUST self-test against this checklist before declaring
interactive-mode support complete. Each item is a hard requirement.

#### Stream discipline

- [ ] All protocol events are written to **stdout** — never to stderr.
- [ ] Stderr is used only for diagnostic / debug logs.
- [ ] Stdout messages are **single-line NDJSON** terminated by `\n`.
- [ ] Stdin is parsed as NDJSON; parse errors are logged to stderr and the
      line is skipped (the command loop continues).

#### Lifecycle

- [ ] On startup with `OVERCAST_COMPAT_INTERACTIVE=1`, the suite does **not**
      automatically run any tests.
- [ ] Emits one or more `building` events during setup.
- [ ] Emits exactly one `ready` event (with `total_tests`) when setup completes.
- [ ] Enters the stdin command loop and waits for a `run` command.
- [ ] On `shutdown` (or stdin EOF): cancels queued work, runs teardown, exits 0.

#### Off-main-thread execution (NON-NEGOTIABLE)

- [ ] The stdin reader and command loop run on the **main thread / hot-path**
      and are never blocked by test execution.
- [ ] Test execution is launched asynchronously (goroutine, thread pool,
      async task) — the `run` handler returns immediately.
- [ ] A `ping` command sent while a test is running MUST produce a `pong`
      response within 500 ms (the orchestrator tolerates up to 5 s).
- [ ] A `cancel` command sent while a test is running MUST be acknowledged
      (via `cancelled` event) within the same constraint.

#### Commands

- [ ] `ping` → emits `pong` immediately (while idle or busy). The `pong`
      event includes a `running_test` field if a test is currently executing.
- [ ] `run` (with `tests` omitted or null) → executes every registered test,
      sorted by group name. Emits `batch_complete` when done.
- [ ] `run` (with explicit `tests` array) → executes only the requested
      groups/tests.
- [ ] `cancel` (batch-level) → cancels all tests in that batch. Emits
      `cancelled` for each, then `batch_complete`.
- [ ] `cancel` (group+test level) → cancels the specified test. Transitive
      dependency cancellation is correct.
- [ ] `shutdown` → graceful exit after teardown. See §3.

#### Batch discipline

- [ ] Every `run` command that produces at least one `test_start` MUST
      eventually produce exactly one `batch_complete` — even if every test
      was cancelled.
- [ ] `batch_complete` includes accurate `passed`, `failed`, `skipped`,
      `unimplemented` counts.
- [ ] Multiple concurrent `run` commands are supported (or queued serially).
      Commands are never dropped.

#### Robustness

- [ ] If the Overcast endpoint is unreachable, the suite emits an `error`
      event with a descriptive message and exits non-zero.
- [ ] If a single test throws an unexpected exception, the failure is reported
      via `test_result` with `status: "fail"`. The suite continues processing
      remaining tests and commands.
- [ ] Teardown always runs — even if every test in a group was cancelled.
- [ ] Group teardown is fault-tolerant: a failed cleanup step does not prevent
      subsequent cleanup steps from running.
- [ ] On SIGINT / SIGTERM: the suite performs graceful shutdown (cancel
      in-flight tests, run teardown, exit 0). If run as a standalone
      process (not under the orchestrator), the suite should still handle
      Ctrl+C cleanly rather than leaving orphaned resources.

---

## 8. Error Handling

### Fatal Errors

A fatal error is one that prevents the suite from continuing (e.g. cannot
connect to the Overcast endpoint, registry file missing, compile failure).

On fatal error the suite MUST:

1. Emit an `error` event with a descriptive message.
2. Run any possible cleanup.
3. Exit with a **non-zero** exit code.

```json
{
  "event": "error",
  "suite": "node-js-sdk",
  "error": "Failed to compile: src/groups/s3.ts(42): Cannot find module '@aws-sdk/client-s3'"
}
```

### Non-Fatal Errors

Non-fatal errors (e.g. a single test throws an unexpected exception) do not
terminate the suite. The suite:

- Reports the failure via `test_result` with `status: "fail"` and the `error`
  field populated.
- Logs diagnostic details to stderr.
- Continues processing the remaining tests and commands.

### Suite Crash

If the suite process crashes (segfault, unhandled exception, OOM kill), the
orchestrator detects this via:

- **stdout EOF** — the pipe closes.
- **Non-zero exit code** — from `wait()` / `waitpid()`.

The orchestrator treats any in-flight tests as failed and any queued tests as
cancelled. No `batch_complete` is emitted for a crashed suite; the orchestrator
synthesizes the final state internally.

---

## Appendix A: Full Session Example

Below is a complete session transcript showing the interleaved stdin/stdout
messages for a typical run.

**stdout** (suite → orchestrator):

```json
{"event":"building","suite":"node-js-sdk","message":"Installing dependencies…"}
{"event":"building","suite":"node-js-sdk","message":"Compiling TypeScript…"}
{"event":"ready","suite":"node-js-sdk","total_tests":147}
```

**stdin** (orchestrator → suite):

```json
{
  "command": "run",
  "batch_id": "batch-001",
  "tests": [
    {
      "group": "s3-buckets",
      "tests": ["CreateBucket", "HeadBucket", "DeleteBucket"]
    },
    { "group": "sqs-queues", "tests": ["CreateQueue"] }
  ]
}
```

**stdout**:

```json
{"event":"test_start","suite":"node-js-sdk","service":"s3","group":"s3-buckets","test":"CreateBucket"}
{"event":"test_result","suite":"node-js-sdk","service":"s3","group":"s3-buckets","test":"CreateBucket","status":"pass","duration_ms":38,"op":"CreateBucket"}
{"event":"test_start","suite":"node-js-sdk","service":"s3","group":"s3-buckets","test":"HeadBucket"}
{"event":"test_result","suite":"node-js-sdk","service":"s3","group":"s3-buckets","test":"HeadBucket","status":"pass","duration_ms":12,"op":"HeadBucket"}
{"event":"test_start","suite":"node-js-sdk","service":"sqs","group":"sqs-queues","test":"CreateQueue"}
{"event":"test_result","suite":"node-js-sdk","service":"sqs","group":"sqs-queues","test":"CreateQueue","status":"pass","duration_ms":27,"op":"CreateQueue"}
{"event":"test_start","suite":"node-js-sdk","service":"s3","group":"s3-buckets","test":"DeleteBucket"}
{"event":"test_result","suite":"node-js-sdk","service":"s3","group":"s3-buckets","test":"DeleteBucket","status":"pass","duration_ms":19,"op":"DeleteBucket"}
{"event":"batch_complete","suite":"node-js-sdk","batch_id":"batch-001","passed":4,"failed":0,"skipped":0,"unimplemented":0,"duration_ms":96}
```

**stdin** (cancel mid-flight):

```json
{
  "command": "run",
  "batch_id": "batch-002",
  "tests": [{ "group": "dynamodb-tables" }]
}
```

**stdout**:

```json
{
  "event": "test_start",
  "suite": "node-js-sdk",
  "service": "dynamodb",
  "group": "dynamodb-tables",
  "test": "CreateTable"
}
```

**stdin**:

```json
{ "command": "cancel", "batch_id": "batch-002" }
```

**stdout**:

```json
{"event":"test_result","suite":"node-js-sdk","service":"dynamodb","group":"dynamodb-tables","test":"CreateTable","status":"pass","duration_ms":64,"op":"CreateTable"}
{"event":"cancelled","suite":"node-js-sdk","batch_id":"batch-002","group":"dynamodb-tables","test":"DescribeTable","reason":"batch"}
{"event":"cancelled","suite":"node-js-sdk","batch_id":"batch-002","group":"dynamodb-tables","test":"DeleteTable","reason":"dependency"}
{"event":"batch_complete","suite":"node-js-sdk","batch_id":"batch-002","passed":1,"failed":0,"skipped":0,"unimplemented":0,"duration_ms":64}
```

**stdin**:

```json
{ "command": "shutdown" }
```

_Suite runs cleanup, emits no further events, exits with code 0._
