---
title: "Step Functions — AWS Step Functions"
description: "A real Amazon States Language interpreter: executions run the definition, call other emulated services, and record a state-by-state history. What it cannot interpret fails loudly."
section: "Service Reference"
tags:
  - docs
  - services
  - stepfunctions
  - workflows
---

# Step Functions — AWS Step Functions

A real Amazon States Language interpreter — executions run the definition, call
other emulated services and record a state-by-state history.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

SM=$(aws stepfunctions create-state-machine --name hello \
  --role-arn arn:aws:iam::000000000000:role/sfn \
  --definition '{"StartAt":"Greet","States":{"Greet":{"Type":"Pass","Result":"hi","End":true}}}' \
  --query stateMachineArn --output text)

EX=$(aws stepfunctions start-execution --state-machine-arn "$SM" \
  --input '{}' --query executionArn --output text)

aws stepfunctions describe-execution --execution-arn "$EX"
aws stepfunctions get-execution-history --execution-arn "$EX"
```

> [!IMPORTANT]
> Anything Overcast cannot interpret **fails the execution loudly** — never a
> silent pass-through, and never a fake `SUCCEEDED`. The error is
> `States.Runtime` and the `cause` names the feature. `States.Runtime` is
> deliberately neither retriable nor catchable, matching AWS, so a `Catch` on
> `States.ALL` cannot swallow an Overcast gap.

## What works

| Area | Interpreted |
| --- | --- |
| State types | All eight: `Pass`, `Task`, `Choice`, `Wait`, `Succeed`, `Fail`, `Parallel`, `Map` (inline) |
| Data flow | `InputPath`, `OutputPath`, `ResultPath`, `Parameters`, `ResultSelector`, `ItemSelector`, `Result`, and the `$$` context object |
| Choice | Every ASL comparison operator, `And`/`Or`/`Not`, `Default` |
| Error handling | `Retry` (`ErrorEquals`, `IntervalSeconds`, `MaxAttempts`, `BackoffRate`, `MaxDelaySeconds`) and `Catch` (`ErrorEquals`, `ResultPath`, `Next`). `States.ALL` and `States.TaskFailed` are wildcards over every error but `States.Runtime` |
| Task timeouts | `TimeoutSeconds` and `TimeoutSecondsPath` really bound the attempt and raise `States.Timeout`, which `Retry`/`Catch` can match |
| Task integrations | A Lambda function ARN; `arn:aws:states:::lambda:invoke`; `sqs:sendMessage`; `sns:publish`; `dynamodb:putItem`/`getItem`/`updateItem`; `states:startExecution` and its `.sync` / `.sync:2` forms |
| Map | Inline `ItemsPath` iteration with `ItemProcessor` or the legacy `Iterator` |
| Execution model | `StartExecution` persists `RUNNING` and returns; the interpreter continues on a tracked goroutine, so nothing dispatching to Step Functions is held open for the length of the workflow |
| History | AWS's event vocabulary with 1-based `id` and `previousEventId` linkage, so step-functions-local-style assertions work unmodified |

Task states dispatch through Overcast's own router, so a workflow step runs
exactly the handler an SDK call would — there is no second code path that could
drift from the service it targets.

## Differences from AWS

| Area | Overcast | AWS |
| --- | --- | --- |
| Query language | JSONPath only; `QueryLanguage: JSONata` fails the execution, whether set on the definition or one state | JSONPath and JSONata |
| Variables | `Assign` and the JSONata-only `Output` field fail the execution | Supported |
| Intrinsics | `States.Format`, `States.Array`, `States.ArrayLength`, `States.StringToJson`, `States.JsonToString`, `States.MathAdd` — every other `States.*` fails | The full set |
| JSONPath | Dotted members and array indices; wildcards, descendants, slices and filters fail | Full JSONPath |
| Task integrations | The list above; every other service integration, all `aws-sdk:` integrations, `.waitForTaskToken` and activity ARNs fail | ~200 services |
| Map | Inline only; `ProcessorConfig.Mode: DISTRIBUTED`, `ItemReader`, `ItemBatcher` and `ResultWriter` fail | Distributed Map |
| `HeartbeatSeconds`, `Retry.JitterStrategy` | Parsed and never read | Honoured |
| `GetExecutionHistory` | `reverseOrder`, `maxResults` and `includeExecutionData` are honoured; there is no pagination token | Paginated |

`CreateStateMachine` validates the ASL and returns `InvalidDefinition` for a
structurally invalid definition, as AWS does. Definitions that are valid ASL but
use features Overcast cannot interpret still provision — so CDK and CloudFormation
deploys keep working — and fail at execution time instead.

## Gotchas

> [!WARNING]
> `OVERCAST_STEPFUNCTIONS_EXECUTION_TIMEOUT` (default `15m`) is a runaway guard,
> not a request timeout — it never sits on the wire, so ordinary `Wait` states are
> unaffected. A state machine's own top-level `TimeoutSeconds` can lower the
> budget but never raise it. Exceeding it ends the execution `TIMED_OUT` with
> `States.Timeout`, which is also what stops a non-terminating `Choice` loop
> (alongside the 25,000-event history cap AWS itself applies).

A `Task`'s own `TimeoutSeconds` is a different thing: it bounds that attempt, and
an uncaught task timeout is a `FAILED` execution rather than a `TIMED_OUT` one, as
on AWS. Note that a local cold start can be slower than AWS's, so a tight
`TimeoutSeconds` may fire here where it would not in the cloud.

`StartSyncExecution` is served for `EXPRESS` state machines only; `STANDARD` gets
AWS's `StateMachineTypeNotSupported`. `StopExecution` is asynchronous, as on AWS,
and shutdown drains in-flight executions rather than leaving them stuck at
`RUNNING`.

<!-- BEGIN overcast:capabilities -->

## Operations

All 15 listed operations are implemented.
Per-operation status, notes and AWS API links: [Step Functions operations](stepfunctions/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Lambda](./lambda.md) — the most common `Task` target
- [EventBridge](./eventbridge.md) and [Scheduler](./scheduler.md) — what starts executions on a schedule
- [AWS API reference](https://docs.aws.amazon.com/step-functions/latest/apireference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
