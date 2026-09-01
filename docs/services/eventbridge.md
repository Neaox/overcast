---
title: "EventBridge — Amazon EventBridge"
description: "Event buses, rules and targets, with matched events delivered in-process to eight target types. Event patterns match on exact values only."
section: "Service Reference"
tags:
  - docs
  - eventbridge
  - events
  - services
---

# EventBridge — Amazon EventBridge

Buses, rules and targets, with matched events delivered in-process to eight
target types — and patterns that match on exact values only.

**Status:** ⚠️ Partial

## Quick start

Route an event to an SQS queue:

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

QUEUE=$(aws sqs create-queue --queue-name orders --query QueueUrl --output text)
ARN=$(aws sqs get-queue-attributes --queue-url "$QUEUE" \
  --attribute-names QueueArn --query Attributes.QueueArn --output text)

aws events put-rule --name orders --event-pattern '{"source":["app.orders"]}'
aws events put-targets --rule orders --targets "Id=1,Arn=$ARN"
aws events put-events --entries \
  '[{"Source":"app.orders","DetailType":"OrderPlaced","Detail":"{\"id\":1}"}]'

aws sqs receive-message --queue-url "$QUEUE"
```

## What works

| Area | Behaviour |
| --- | --- |
| Buses and rules | Bus, rule, target and tag CRUD. `DescribeEventBus` answers for `default` whether or not it was created. |
| Delivery | `PutEvents` evaluates every rule on the bus and delivers matches to Lambda, SQS, SNS, Step Functions, Kinesis, Firehose, ECS `RunTask` and another event bus. |
| Scheduled rules | `rate(...)` and AWS's six-field `cron(...)` fire on an in-process clock, through the same target dispatcher. |
| Input shaping | `Input`, `InputPath` and `InputTransformer`, at most one per target, over the JSONPath subset AWS uses (`$`, dotted members, array indexing). |
| Retries and dead-lettering | `RetryPolicy.MaximumRetryAttempts`, `MaximumEventAgeInSeconds` measured from the envelope's `time`, and a `DeadLetterConfig` SQS queue. |
| Service-originated events | CloudWatch alarm transitions, S3 object created/deleted, EC2 and ECS state changes, Auto Scaling launch and terminate events, and Step Functions execution status changes publish onto the default bus. |

`InputTransformer` templates may reference `<aws.events.rule-name>`,
`<aws.events.rule-arn>`, `<aws.events.event.json>` and
`<aws.events.event.ingestion-time>`. Recent per-target outcomes — delivered,
retried, dead-lettered, dropped — are readable at
`GET /_overcast/eventbridge/deliveries`, an emulator-only endpoint backed by an
in-memory ring that does not survive a restart.

## Differences from AWS

| Area | Overcast | AWS |
| --- | --- | --- |
| Event patterns | Exact value matching only | `prefix`, `suffix`, `numeric`, `anything-but`, `exists`, `cidr`, `wildcard` |
| Target types | Eight; anything else is refused by `PutTargets` | ~20 |
| Retry timing | Immediate, capped at 5 retries | Exponential backoff over up to 24 hours |
| Bus-to-bus forwarding | Nested in-process call, capped at 4 hops | Independent delivery, no hop budget |
| Archives, replay, API destinations | Not implemented | Supported |
| Service-originated events | Six publishers | Substantially more |

A target ARN naming an unsupported service comes back in `PutTargets`'s
`FailedEntries` with `ErrorCode: UnsupportedTargetType`. That is deliberately
stricter than AWS: a rule that provisions cleanly and never fires is worse than
one that refuses up front.

## Gotchas

> [!WARNING]
> **A content-filtering pattern never matches.** `{"detail":{"amount":[{"numeric":[">",100]}]}}`
> is stored, the rule looks correct in `DescribeRule`, and no event ever
> satisfies it. Filter on exact `source`, `detail-type` and `detail` values, and
> do the rest in the target.

> [!IMPORTANT]
> A `cron(...)` expression takes AWS's **six** fields, and day-of-week is 1-7
> from Sunday. The five-field Unix form is refused, as it is on AWS: every five
> minutes is `cron(*/5 * * * ? *)`. `L`, `LW`, `<day>W`, `<day>L`, `<day>#<n>`
> and the three-letter month and day names all work.

<!-- BEGIN overcast:capabilities -->

## Operations

18 of 29 listed operations are implemented.
Per-operation status, notes and AWS API links: [EventBridge operations](eventbridge/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Scheduler](./scheduler.md) — the same target dispatcher, on a clock
- [Pipes](./pipes.md) — point-to-point source → target wiring
- [AWS API reference](https://docs.aws.amazon.com/eventbridge/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
