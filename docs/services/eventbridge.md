---
title: "EventBridge — endpoint support"
description: "EventBridge accepts AWS JSON 1.1 via X-Amz-Target: AWSEvents.\u003coperation\u003e. It also accepts Smithy RPC v2 CBOR at /service/EventBridge/operation/\u003coperation\u003e with Smithy-Protocol..."
section: "Service Reference"
tags:
  - docs
  - endpoint
  - eventbridge
  - services
  - support
---

# EventBridge — endpoint support

> AWS docs: [EventBridge API Reference](https://docs.aws.amazon.com/eventbridge/latest/APIReference/Welcome.html)

EventBridge accepts AWS JSON 1.1 via `X-Amz-Target: AWSEvents.<operation>`.
It also accepts Smithy RPC v2 CBOR at `/service/EventBridge/operation/<operation>`
with `Smithy-Protocol: rpc-v2-cbor` and `Content-Type: application/cbor`.
Overcast implements event buses, rules, targets, tagging, event ingestion, and
same-process target delivery.

> [!WARNING]
> **Emulation tier: Partial** — EventBridge matches common event patterns and fans
> matched events out to Lambda, SQS, SNS, Step Functions, Kinesis, Firehose and
> EventBridge event bus targets;
> scheduled rules can also invoke ECS/Fargate `RunTask` targets. Target types outside
> that list, archives/replay, API destinations and advanced pattern operators
> (`prefix`, `numeric`, `anything-but`, …) are still incomplete.

---

## Notes

- **Target fan-out.** `PutEvents` evaluates exact-match rule patterns and delivers matching
  events to every target type listed above. A target ARN naming any other service is
  **rejected by `PutTargets`** with a `FailedEntries` entry carrying `ErrorCode:
  UnsupportedTargetType`, rather than being accepted and silently dropped at delivery time.
  This is a deliberate divergence from AWS, where all ~20 target types work: an honest
  refusal is preferred to a rule that provisions cleanly and never fires.
- **Input transformation.** `Input`, `InputPath` and `InputTransformer` are applied before
  delivery, and a target may set at most one of them (as on AWS). `InputPath` and
  `InputTransformer.InputPathsMap` accept the JSONPath subset AWS uses: `$`, dotted member
  access and array indexing. `InputTransformer` templates may reference
  `<aws.events.rule-name>`, `<aws.events.rule-arn>`, `<aws.events.event.json>` and
  `<aws.events.event.ingestion-time>`.
- **Event bus targets.** A rule may target another event bus. The event is republished
  there through `PutEvents` carrying the **original** `source`, `detail-type`, `detail` and
  `resources`, so a rule on the downstream bus can match on the fields a real rule filters
  on. A target that sets `Input`, `InputPath` or `InputTransformer` replaces the forwarded
  `detail` with the transformed payload and keeps the routing fields. Every hop is a nested
  in-process call, so a chain is capped at 4 hops: a cycle of buses forwarding to each other
  terminates with a delivery error naming the hop budget rather than exhausting the stack.
- **Retries and dead-letter queues.** A failed delivery is retried up to the target's
  `RetryPolicy.MaximumRetryAttempts` (capped at 5 retries) and stops early once the event is
  older than `RetryPolicy.MaximumEventAgeInSeconds`, measured from the envelope's own `time`.
  The event is then sent to the target's `DeadLetterConfig` SQS queue if one is configured,
  and otherwise dropped with a logged warning. Retries are immediate: real EventBridge backs
  off over up to 24 hours, which a synchronous emulator has nowhere to wait for — so in
  practice the age limit only bites when a target itself is slow to fail.
- **Delivery visibility.** Recent per-target outcomes (delivered / retried / dead-lettered /
  dropped) are exposed to the web console at `GET /_overcast/eventbridge/deliveries`, and
  each rule's targets with their resolved type at `GET /_overcast/eventbridge/rule-targets`.
  Both are emulator-only console endpoints, not AWS APIs, and the outcome feed is a bounded
  in-memory ring that does not survive a restart.
- **Scheduled ECS targets.** Rate and AWS cron expressions are evaluated by an
  in-process clock-driven engine. ECS/Fargate targets call ECS `RunTask` with the
  configured target parameters.
- **Cron expressions.** The full six-field AWS syntax is supported, shared with
  EventBridge Scheduler: numbers, `,` lists, `-` ranges, `/` steps (including over a
  range, `0-6/2`), the three-letter month and day names (`JAN`, `MON-FRI`,
  case-insensitive), and the `L`, `LW`, `<day>W`, `<day>L` and `<day>#<n>` day
  specifiers. Day-of-week is AWS's 1-7 from Sunday, not Go's 0-6 from Sunday, so `1`
  is Sunday and `7` is Saturday.

  `PutRule` refuses an expression it cannot honour rather than storing a rule that
  would never fire, and the error names the expression and the field at fault. The
  five-field Unix form is the common mistake and AWS refuses it too — every five
  minutes is `cron(*/5 * * * ? *)`, not `cron(*/5 * * * *)`.
- **Synthetic default bus.** `DescribeEventBus` returns a synthetic "default" bus even if one
  has not been explicitly created.
- **CDK compatible management plane.** Sufficient for CDK deployments that create buses,
  rules, and targets, including scheduled ECS/Fargate task target metadata.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category    | ✅ Supported | ❌ Unsupported |
| ----------- | ------------ | -------------- |
| Event buses | 4            |                |
| Rules       | 6            |                |
| Targets     | 3            |                |
| Events      | 1            |                |
| Tags        | 3            |                |
| Archives    |              | 4              |
| Replays     |              | 3              |
| Connections |              | 4              |

---

## Endpoints

### Event buses

| Operation          | Status       | Notes                                      | AWS Docs                                                                                      |
| ------------------ | ------------ | ------------------------------------------ | --------------------------------------------------------------------------------------------- |
| `CreateEventBus`   | ✅ Supported | Creates a custom event bus                 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_CreateEventBus.html)   |
| `DescribeEventBus` | ✅ Supported | Returns bus details; synthetic default bus | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeEventBus.html) |
| `ListEventBuses`   | ✅ Supported | Always includes default bus                | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListEventBuses.html)   |
| `DeleteEventBus`   | ✅ Supported |                                            | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DeleteEventBus.html)   |

### Rules

| Operation      | Status       | Notes                       | AWS Docs                                                                                  |
| -------------- | ------------ | --------------------------- | ----------------------------------------------------------------------------------------- |
| `PutRule`      | ✅ Supported | Creates or updates a rule   | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_PutRule.html)      |
| `DescribeRule` | ✅ Supported |                             | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeRule.html) |
| `ListRules`    | ✅ Supported | Lists rules for a bus       | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListRules.html)    |
| `EnableRule`   | ✅ Supported | Sets rule state to ENABLED  | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_EnableRule.html)   |
| `DisableRule`  | ✅ Supported | Sets rule state to DISABLED | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DisableRule.html)  |
| `DeleteRule`   | ✅ Supported |                             | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DeleteRule.html)   |

### Targets

| Operation           | Status       | Notes                                                                                                            | AWS Docs                                                                                       |
| ------------------- | ------------ | ---------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `PutTargets`        | ✅ Supported | Adds Lambda, SQS, SNS, Step Functions, Kinesis, Firehose and ECS targets; rejects other target types at add time | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_PutTargets.html)        |
| `ListTargetsByRule` | ✅ Supported | Lists targets including input transformers and ECS/Kinesis/SQS target parameters                                 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListTargetsByRule.html) |
| `RemoveTargets`     | ✅ Supported | Removes targets from a rule                                                                                      | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_RemoveTargets.html)     |

### Events

| Operation   | Status       | Notes                                                                                                                                              | AWS Docs                                                                               |
| ----------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `PutEvents` | ✅ Supported | Delivers matching rules to Lambda, SQS, SNS, Step Functions, Kinesis and Firehose targets, applying InputPath/InputTransformer and RetryPolicy/DLQ | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_PutEvents.html) |

### Tags

| Operation             | Status       | Notes                        | AWS Docs                                                                                         |
| --------------------- | ------------ | ---------------------------- | ------------------------------------------------------------------------------------------------ |
| `TagResource`         | ✅ Supported | Tag buses and rules          | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_TagResource.html)         |
| `ListTagsForResource` | ✅ Supported | List tags for a resource     | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListTagsForResource.html) |
| `UntagResource`       | ✅ Supported | Removes tags from a resource | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_UntagResource.html)       |

### Archives

| Operation         | Status         | Notes       | AWS Docs                                                                                     |
| ----------------- | -------------- | ----------- | -------------------------------------------------------------------------------------------- |
| `CreateArchive`   | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_CreateArchive.html)   |
| `DescribeArchive` | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeArchive.html) |
| `ListArchives`    | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListArchives.html)    |
| `DeleteArchive`   | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DeleteArchive.html)   |

### Replays

| Operation        | Status         | Notes       | AWS Docs                                                                                    |
| ---------------- | -------------- | ----------- | ------------------------------------------------------------------------------------------- |
| `StartReplay`    | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_StartReplay.html)    |
| `DescribeReplay` | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeReplay.html) |
| `ListReplays`    | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListReplays.html)    |

### Connections

| Operation            | Status         | Notes       | AWS Docs                                                                                        |
| -------------------- | -------------- | ----------- | ----------------------------------------------------------------------------------------------- |
| `CreateConnection`   | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_CreateConnection.html)   |
| `DescribeConnection` | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeConnection.html) |
| `ListConnections`    | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListConnections.html)    |
| `DeleteConnection`   | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DeleteConnection.html)   |

<!-- END overcast:capabilities -->
