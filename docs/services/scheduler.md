---
title: "Scheduler — Amazon EventBridge Scheduler"
description: "Schedules and schedule groups with a clock-driven engine that dispatches to the same eight target types EventBridge rules reach."
section: "Service Reference"
tags:
  - docs
  - eventbridge
  - scheduler
  - services
---

# Scheduler — Amazon EventBridge Scheduler

Schedules and groups, with a clock-driven engine that fires them into the same
eight target types EventBridge rules reach.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

QUEUE=$(aws sqs create-queue --queue-name jobs --query QueueUrl --output text)
ARN=$(aws sqs get-queue-attributes --queue-url "$QUEUE" \
  --attribute-names QueueArn --query Attributes.QueueArn --output text)

aws scheduler create-schedule --name tick \
  --schedule-expression 'rate(1 minute)' \
  --flexible-time-window Mode=OFF \
  --target "Arn=$ARN,RoleArn=arn:aws:iam::000000000000:role/scheduler"

aws sqs receive-message --queue-url "$QUEUE" --wait-time-seconds 20
```

## What works

| Area | Behaviour |
| --- | --- |
| Schedules and groups | Full CRUD and tagging. `default` is auto-seeded and cannot be deleted; deleting a group deletes the schedules in it, as AWS does. |
| Expressions | `rate(...)`, `at(...)` and AWS's six-field `cron(...)` — the same parser EventBridge rules use, including `L`, `LW`, `<day>W`, `<day>L`, `<day>#<n>` and the three-letter month and day names. |
| Engine | A 1-second ticker hands each due schedule to a pool of delivery workers, so a slow or retrying target delays only its own schedule. A schedule is never in flight twice, so its firings stay in order. |
| Targets | Lambda, SQS, SNS, Step Functions, Kinesis, Firehose, ECS `RunTask` and EventBridge event buses — through the same dispatcher EventBridge rules and Pipes use, so a target ARN behaves identically wherever it is used. |
| Target parameters | `SqsParameters.MessageGroupId`, `KinesisParameters.PartitionKey`, `EventBridgeParameters` (`Source`, `DetailType`), and `EcsParameters` (`TaskDefinitionArn`, `TaskCount`, `LaunchType`, `PlatformVersion`, `Group`, `NetworkConfiguration`). |
| Payload | `Target.Input` is delivered verbatim; a target with none receives a generated `{"source":"aws.scheduler","time":…,"id":…}` envelope. |
| Retries | `RetryPolicy.MaximumRetryAttempts` and `MaximumEventAgeInSeconds`, with a `DeadLetterConfig` SQS queue after the final failed attempt. |
| Pagination | `MaxResults` (1–100) and `NextToken` on both list operations; `NamePrefix` and `State` filters on `ListSchedules`. |

## Differences from AWS

| Area | Overcast | AWS |
| --- | --- | --- |
| Target types | Eight, validated at `CreateSchedule`/`UpdateSchedule` | ~270, via templated and universal (`aws-sdk:`) targets |
| Retries | Back to back, no backoff, capped at 6 total attempts. No `RetryPolicy` means **one** attempt | Up to 185 attempts with backoff |
| `FlexibleTimeWindow` | Stored and returned; a schedule always fires at its exact due tick | Jittered across the window |
| `ScheduleExpressionTimezone` | Stored and returned; expressions evaluate against the emulator's clock | Evaluated in the named zone |
| `KmsKeyArn` | An association only; schedule data is held in plaintext | Encrypted |
| List responses | Return the full stored object rather than AWS's summary shape — a superset, so an SDK deserialises it unchanged | `ScheduleSummary` / `ScheduleGroupSummary` |

A target type Overcast cannot fire is **rejected at create and update** with a
`ValidationException`, rather than accepted and dropped at fire time — as are an
ECS target without `EcsParameters.TaskDefinitionArn` and an event-bus target
without `EventBridgeParameters`. The refusal fails locally and loudly instead of
leaving a schedule that reads correctly in `GetSchedule` and never fires. An
expression the engine cannot evaluate is refused for the same reason.

## Gotchas

> [!WARNING]
> **`UpdateSchedule` replaces, as AWS's does.** The request carries the whole
> schedule, so any optional member you omit — `Description`,
> `ScheduleExpressionTimezone`, `State`, `StartDate`, `EndDate`, or anything
> inside `Target` — ends up unset, and `State` returns to its `ENABLED` default.
> Read the schedule, change what you mean to change, and send the result back.
> Only the name, group, ARN and `CreationDate` survive regardless.

> [!IMPORTANT]
> `cron(...)` takes AWS's **six** fields, and day-of-week is 1-7 from Sunday, not
> 0-6 — so `1` is Sunday and `7` is Saturday. The five-field Unix form is refused,
> as it is on AWS: every five minutes is `cron(*/5 * * * ? *)`.

<!-- BEGIN overcast:capabilities -->

## Operations

All 12 listed operations are implemented.
Per-operation status, notes and AWS API links: [Scheduler operations](scheduler/operations.md).

<!-- END overcast:capabilities -->

## Related

- [EventBridge](./eventbridge.md) — the same targets, driven by event patterns
- [Pipes](./pipes.md) — the same targets, driven by a source
- [AWS API reference](https://docs.aws.amazon.com/scheduler/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
