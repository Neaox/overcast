---
title: "Scheduler — Amazon EventBridge Scheduler"
description: "EventBridge Scheduler is served as a REST-JSON API under /_scheduler/*. This implementation focuses on schedule groups, schedules, tagging, and clock-driven target dispatch to..."
section: "Service Reference"
tags:
  - amazon
  - docs
  - eventbridge
  - scheduler
  - services
---

# Scheduler — Amazon EventBridge Scheduler

> AWS docs: https://docs.aws.amazon.com/scheduler/latest/APIReference/Welcome.html

EventBridge Scheduler is served as a REST-JSON API under `/_scheduler/*`.
This implementation focuses on schedule groups, schedules, tagging, and
clock-driven target dispatch to every target type EventBridge rules reach.

---

## Behavior Notes

- Default schedule group:
  - `default` is auto-seeded and cannot be deleted.
- Supported schedule expressions:
  - `rate(...)`
  - `at(...)`
  - `cron(...)` (AWS-style 6-field form)
- Background scheduler engine:
  - Polls on a 1-second clock ticker.
  - Uses the injected clock, so integration tests can advance time quickly.
- Target dispatch:
  - Delivery goes through the same internal dispatcher EventBridge rules and
    Pipes use, so a target ARN behaves identically on a schedule and on a rule.
    A firing is replayed against the emulator's own API, which means a missing
    function, queue, topic, stream or state machine produces that service's own
    AWS error rather than a silent no-op.
  - Supported target types: **Lambda** (async invoke), **SQS**, **SNS**,
    **Step Functions**, **Kinesis**, **Firehose**, **ECS** (`RunTask`) and
    **EventBridge event buses** (`PutEvents`).
  - Target parameters honoured: `SqsParameters.MessageGroupId`,
    `KinesisParameters.PartitionKey`, `EventBridgeParameters` (`Source` and
    `DetailType`), and `EcsParameters` (`TaskDefinitionArn`, `TaskCount`,
    `LaunchType`, `PlatformVersion`, `Group`, `NetworkConfiguration`). The
    remainder of AWS's `EcsParameters` shape — tags, placement constraints and
    strategy, capacity provider strategy — is accepted and ignored.
  - `Target.Input` is delivered verbatim. A target with no `Input` receives a
    generated `{"source":"aws.scheduler","time":…,"id":…}` envelope.
  - **A target type Overcast cannot fire is rejected at `CreateSchedule` and
    `UpdateSchedule`** with a `ValidationException`, rather than being accepted
    and dropped at fire time. This is stricter than AWS, which delivers to
    ~270 services through templated and universal (`arn:aws:scheduler:::aws-sdk:…`)
    targets; the refusal fails locally and loudly instead of leaving a schedule
    that looks correct and never fires. An ECS target without
    `EcsParameters.TaskDefinitionArn`, and an event-bus target without
    `EventBridgeParameters`, are refused for the same reason.
- Retries and dead-lettering:
  - `RetryPolicy.MaximumRetryAttempts` is honoured, capped at **6 total
    attempts**. Retries run inline on the engine tick with no backoff, so AWS's
    default of 185 attempts is not replayed: a target with no `RetryPolicy` is
    attempted **once**. EventBridge rule targets behave the same way.
  - `RetryPolicy.MaximumEventAgeInSeconds` is honoured — once the payload is
    older than the budget, no further attempt is made.
  - `DeadLetterConfig.Arn` is honoured for SQS queues, which is the only
    dead-letter target AWS supports. The payload is sent to the queue after the
    final failed attempt.
  - A firing that cannot be delivered and has no dead-letter queue is logged at
    `ERROR` with the sink's own message — it is never dropped silently.
- Not implemented:
  - `FlexibleTimeWindow` is stored and returned, but a schedule always fires at
    its exact due tick rather than being jittered across the window.
  - `ScheduleExpressionTimezone` is stored and returned, but `cron(...)` and
    `at(...)` are evaluated against the emulator's own clock rather than the
    named zone.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category        | ✅ Supported |
| --------------- | ------------ |
| Schedule Groups | 4            |
| Schedules       | 5            |
| Tags            | 3            |

---

## Endpoints

### Schedule Groups

| Operation             | Status       | Notes                            | AWS Docs                                                                                       |
| --------------------- | ------------ | -------------------------------- | ---------------------------------------------------------------------------------------------- |
| `CreateScheduleGroup` | ✅ Supported | Creates a named group            | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_CreateScheduleGroup.html) |
| `GetScheduleGroup`    | ✅ Supported | Returns group metadata           | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_GetScheduleGroup.html)    |
| `ListScheduleGroups`  | ✅ Supported | Lists groups in region           | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_ListScheduleGroups.html)  |
| `DeleteScheduleGroup` | ✅ Supported | Deletes group (except `default`) | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_DeleteScheduleGroup.html) |

### Schedules

| Operation        | Status       | Notes                                                                              | AWS Docs                                                                                  |
| ---------------- | ------------ | ---------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `CreateSchedule` | ✅ Supported | Group-specific or default group path; rejects a target type Overcast cannot fire   | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_CreateSchedule.html) |
| `GetSchedule`    | ✅ Supported | Returns full schedule definition                                                   | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_GetSchedule.html)    |
| `UpdateSchedule` | ✅ Supported | Updates expression/target/state fields; rejects a target type Overcast cannot fire | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_UpdateSchedule.html) |
| `DeleteSchedule` | ✅ Supported | Deletes schedule                                                                   | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_DeleteSchedule.html) |
| `ListSchedules`  | ✅ Supported | Optional `ScheduleGroup` filter                                                    | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_ListSchedules.html)  |

### Tags

| Operation             | Status       | Notes                 | AWS Docs                                                                                       |
| --------------------- | ------------ | --------------------- | ---------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Merges tags on ARN    | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes keys from ARN | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns tag map       | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
