---
title: "Step Functions — endpoint support"
description: "Step Functions accepts AWS JSON 1.0 via X-Amz-Target: AWSStepFunctions.\u003coperation\u003e. It also accepts Smithy RPC v2 CBOR at /service/StepFunctions/operation/\u003coperation\u003e with..."
section: "Service Reference"
tags:
  - docs
  - endpoint
  - functions
  - services
  - step
  - stepfunctions
  - support
---

# Step Functions — endpoint support

> AWS docs: [Step Functions API Reference](https://docs.aws.amazon.com/step-functions/latest/apireference/Welcome.html)

Step Functions accepts AWS JSON 1.0 via `X-Amz-Target:
AWSStepFunctions.<operation>`. It also accepts Smithy RPC v2 CBOR at
`/service/StepFunctions/operation/<operation>` with `Smithy-Protocol:
rpc-v2-cbor` and `Content-Type: application/cbor`. Overcast implements state
machine CRUD **and a real Amazon States Language interpreter**: executions run
the definition, invoke other emulated services, and record a state-by-state
history.

> [!IMPORTANT]
> **Anything Overcast cannot interpret fails the execution loudly.** An
> unsupported Task resource, `.waitForTaskToken`, an activity task, a
> distributed Map, or JSONata set on the definition or on a single state,
> produces a `FAILED` execution whose `error` is `States.Runtime` and whose
> `cause` names the feature — never a
> silent pass-through, and never a fake `SUCCEEDED`. `States.Runtime` is
> deliberately neither retriable nor catchable (matching AWS), so a
> `Catch` on `States.ALL` cannot swallow an Overcast gap.

---

## What the interpreter runs

| Area | Interpreted | Fails loudly |
| --- | --- | --- |
| State types | `Pass`, `Task`, `Choice`, `Wait`, `Succeed`, `Fail`, `Parallel`, `Map` (inline) | — (all eight ASL state types are interpreted; an unknown `Type` is rejected at `CreateStateMachine` with `InvalidDefinition`, as on AWS) |
| Data flow | `InputPath`, `OutputPath`, `ResultPath`, `Parameters`, `ResultSelector`, `ItemSelector`, `Result`, the `$$` context object | JSONPath wildcards, descendants, slices and filter expressions; `Assign` (variables) |
| Intrinsics | `States.Format`, `States.Array`, `States.ArrayLength`, `States.StringToJson`, `States.JsonToString`, `States.MathAdd` | every other `States.*` intrinsic |
| Choice | every ASL comparison operator, `And`/`Or`/`Not`, `Default` | an operator outside the language (rejected at create time) |
| Error handling | `Retry` (`ErrorEquals`, `IntervalSeconds`, `MaxAttempts`, `BackoffRate`, `MaxDelaySeconds`), `Catch` (`ErrorEquals`, `ResultPath`, `Next`). `States.ALL` and `States.TaskFailed` are both wildcards, matching any error name except `States.Runtime`; every other reserved name matches literally | `Assign` (variables) on a `Catch` |
| Task timeouts | `TimeoutSeconds` and `TimeoutSecondsPath` really bound the attempt and raise `States.Timeout`, which `Retry`/`Catch` can match | `HeartbeatSeconds` (unread — it only governs activity tasks and `.waitForTaskToken`, which already fail loudly) |
| Task integrations | a Lambda function ARN; `arn:aws:states:::lambda:invoke`; `sqs:sendMessage`; `sns:publish`; `dynamodb:putItem`/`getItem`/`updateItem`; `states:startExecution` and its `.sync` / `.sync:2` forms | every other service integration, all `aws-sdk:` integrations, `.waitForTaskToken`, activity ARNs |
| Map | inline `ItemsPath` iteration with `ItemProcessor` (or the legacy `Iterator`) | `ProcessorConfig.Mode: DISTRIBUTED`, `ItemReader`, `ItemBatcher`, `ResultWriter` |
| Query language | JSONPath | JSONata (`QueryLanguage: JSONata`), whether it is set on the whole definition or on a single state, and the JSONata-only `Output` field |

---

## Notes

- **Executions run in the background, as on AWS.** `StartExecution` persists
  the execution as `RUNNING` and returns; the interpreter continues on a
  tracked goroutine. `DescribeExecution` and `GetExecutionHistory` observe it
  progressing, and `StopExecution` really interrupts it. Nothing that dispatches
  to Step Functions — an EventBridge target, a Pipes target, a parent state
  machine's plain `states:startExecution` — is held open for the length of the
  workflow.
- **`StartSyncExecution` is the synchronous one**, which is exactly its
  express-workflow semantic on AWS, and `states:startExecution.sync` /
  `.sync:2` block on the child the same way.
- **`StopExecution` is asynchronous**, as on AWS: it returns the stop time and
  the execution reaches `ABORTED` a moment later, carrying the `error` and
  `cause` you supplied. A `RUNNING` record left behind by a process that exited
  mid-execution is transitioned directly instead.
- **Shutdown drains executions.** In-flight runs are cancelled and given the
  shutdown budget to write their terminal state, so a stopped emulator does not
  leave executions stuck at `RUNNING`.
- **The run is bounded.** `OVERCAST_STEPFUNCTIONS_EXECUTION_TIMEOUT` (default
  `15m`) is a runaway guard, not a request timeout — it never sits on the wire,
  so ordinary `Wait` states are unaffected. A state machine's own top-level
  `TimeoutSeconds` can lower the budget but never raise it. Exceeding it ends
  the execution `TIMED_OUT` with AWS's `States.Timeout`, which is also what
  stops a non-terminating `Choice` loop (alongside the 25,000-event history
  cap AWS itself applies).
- **A `Task`'s own `TimeoutSeconds` bounds that attempt.** It is a real
  deadline, not a value echoed into the history event: the integration is
  dispatched under it and an over-running attempt is interrupted and raised as
  `States.Timeout`, so `Retry`/`Catch` on a task timeout behave as they do on
  AWS and the history carries `TaskTimedOut`. Unlike the execution budget this
  does **not** end the execution `TIMED_OUT` — an uncaught task timeout is a
  `FAILED` execution whose `error` is `States.Timeout`, as on AWS. An
  integration that ignores cancellation can still run to completion; the
  attempt is reported timed out when it fails. Note that a local cold start
  can be slower than AWS's, so a tight `TimeoutSeconds` may fire here where it
  would not in the cloud.
- **Task states dispatch through Overcast's own router**, so a workflow step
  runs exactly the handler an SDK call would — there is no second code path that
  could drift from the service it targets.
- **`GetExecutionHistory` emits AWS's event vocabulary** (`ExecutionStarted`,
  `TaskStateEntered`, `TaskScheduled`, `TaskSucceeded`, `MapIterationStarted`,
  `LambdaFunctionFailed`, …) with 1-based `id` and `previousEventId` linkage, so
  step-functions-local-style assertions work unmodified. `reverseOrder`,
  `maxResults` and `includeExecutionData` are honoured; there is no pagination
  token.
- **`CreateStateMachine` validates the ASL** and returns `InvalidDefinition`
  for a structurally invalid definition, as AWS does. Definitions that are valid
  ASL but use features Overcast cannot interpret still provision — so CDK and
  CloudFormation deploys keep working — and fail at execution time instead.
- **`StartSyncExecution`** reuses the same interpreter and is served for
  `EXPRESS` state machines only; `STANDARD` gets AWS's
  `StateMachineTypeNotSupported`.
- **Idempotent creation.** `CreateStateMachine` returns the existing state
  machine if the name, definition, role ARN, and type all match.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category       | ✅ Supported |
| -------------- | ------------ |
| State machines | 6            |
| Executions     | 6            |
| Tags           | 3            |

---

## Endpoints

### State machines

| Operation                          | Status       | Notes                                                                                 | AWS Docs                                                                                                         |
| ---------------------------------- | ------------ | ------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `CreateStateMachine`               | ✅ Supported | Validates the ASL; idempotent — returns existing if name+def match                    | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_CreateStateMachine.html)               |
| `DescribeStateMachine`             | ✅ Supported |                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DescribeStateMachine.html)             |
| `ListStateMachines`                | ✅ Supported |                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_ListStateMachines.html)                |
| `DeleteStateMachine`               | ✅ Supported |                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DeleteStateMachine.html)               |
| `UpdateStateMachine`               | ✅ Supported | Definition/roleArn/loggingConfiguration/tracingConfiguration; no versioning (publish) | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_UpdateStateMachine.html)               |
| `DescribeStateMachineForExecution` | ✅ Supported |                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DescribeStateMachineForExecution.html) |

### Executions

| Operation             | Status       | Notes                                                                                                                                                                                                                          | AWS Docs                                                                                            |
| --------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| `StartExecution`      | ✅ Supported | Interprets the ASL; returns while the execution is RUNNING, as AWS does; a standard workflow's RUNNING and terminal status transitions each emit a Step Functions Execution Status Change event to the default EventBridge bus | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_StartExecution.html)      |
| `StartSyncExecution`  | ✅ Supported | EXPRESS only — same interpreter, run to completion before returning; EXPRESS executions do not emit EventBridge events, matching AWS                                                                                           | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_StartSyncExecution.html)  |
| `DescribeExecution`   | ✅ Supported | Real status, output, error and cause                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_DescribeExecution.html)   |
| `ListExecutions`      | ✅ Supported | statusFilter (validated against the ExecutionStatus enum) and maxResults honoured; no pagination token                                                                                                                         | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_ListExecutions.html)      |
| `GetExecutionHistory` | ✅ Supported | Real state-transition events in AWS's vocabulary; readable while RUNNING                                                                                                                                                       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_GetExecutionHistory.html) |
| `StopExecution`       | ✅ Supported | Interrupts a running execution; it reaches ABORTED asynchronously; a standard workflow's ABORTED transition emits a Step Functions Execution Status Change event to the default EventBridge bus                                | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_StopExecution.html)       |

### Tags

| Operation             | Status       | Notes | AWS Docs                                                                                            |
| --------------------- | ------------ | ----- | --------------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported |       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/step-functions/latest/apireference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
