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
- **Service-originated events.** A handful of emulated services publish their own events onto
  the default bus, the way real AWS services do on a customer's behalf: CloudWatch alarms
  (`CloudWatch Alarm State Change`, every transition), S3 (`Object Created` / `Object Deleted`,
  when a bucket's `EventBridgeConfiguration` is set), EC2 (`EC2 Instance State-change
  Notification`, every time an instance's state changes — including its first transition into
  `pending`), ECS (`ECS Task State Change`, every time a task's `lastStatus` changes), and Step
  Functions (`Step Functions Execution Status Change`, source `aws.states`, every time a
  standard-workflow execution's status changes — RUNNING on start, then SUCCEEDED, FAILED,
  TIMED_OUT or ABORTED on completion; the FAILED/TIMED_OUT detail carries the execution's
  `error`/`cause`). Real AWS emits substantially more service-originated events than these five;
  a rule matching one of those will never fire here — see
  [#758](https://github.com/overcast-sh/overcast/issues/758) (closed out by
  [#1225](https://github.com/overcast-sh/overcast/pull/1225) and
  [#1221](https://github.com/overcast-sh/overcast/issues/1221)).
- **Synthetic default bus.** `DescribeEventBus` returns a synthetic "default" bus even if one
  has not been explicitly created.
- **CDK compatible management plane.** Sufficient for CDK deployments that create buses,
  rules, and targets, including scheduled ECS/Fargate task target metadata.

<!-- BEGIN overcast:capabilities -->

## Operations

18 of 29 listed operations are implemented.
Per-operation status, notes and AWS API links: [EventBridge operations](eventbridge/operations.md).

<!-- END overcast:capabilities -->

## Related

- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
