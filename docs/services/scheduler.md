---
title: "Scheduler — Amazon EventBridge Scheduler"
description: "EventBridge Scheduler is served as a REST-JSON API at AWS's own paths, so an unmodified SDK or aws scheduler CLI call reaches it. This implementation focuses on schedule groups,..."
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

EventBridge Scheduler is served as a REST-JSON API at AWS's own paths, so an
unmodified SDK or `aws scheduler …` call reaches it. This implementation focuses
on schedule groups, schedules, tagging, and clock-driven target dispatch to
every target type EventBridge rules reach.

---

## Behavior Notes

- Request paths — AWS's own bindings, taken from the pinned Smithy model:

  | Operation | Binding |
  | --- | --- |
  | `CreateSchedule` | `POST /schedules/{Name}` — `GroupName` in the body |
  | `GetSchedule` | `GET /schedules/{Name}?groupName=` |
  | `UpdateSchedule` | `PUT /schedules/{Name}` — `GroupName` in the body |
  | `DeleteSchedule` | `DELETE /schedules/{Name}?groupName=` |
  | `ListSchedules` | `GET /schedules?ScheduleGroup=` |
  | `CreateScheduleGroup` | `POST /schedule-groups/{Name}` |
  | `GetScheduleGroup` | `GET /schedule-groups/{Name}` |
  | `DeleteScheduleGroup` | `DELETE /schedule-groups/{Name}` |
  | `ListScheduleGroups` | `GET /schedule-groups` |
  | `TagResource` / `UntagResource` / `ListTagsForResource` | `POST` / `DELETE` / `GET /tags/{ResourceArn}` |

  A schedule is addressed by name alone; its group is never a path segment.
  `/tags/{ResourceArn}` is shared with API Gateway, EKS and Pipes, and is
  dispatched on the `scheduler` segment of the resource ARN.

  Releases up to and including `0.0.1-alpha.33` served these operations under an
  emulator-invented `/_scheduler/` prefix instead, so every SDK and CLI call
  answered `501`. That prefix has been removed rather than kept as an alias.
- Default schedule group:
  - `default` is auto-seeded and cannot be deleted.
  - `DeleteScheduleGroup` deletes the schedules inside the group, as AWS does.
- Supported schedule expressions:
  - `rate(...)`
  - `at(...)`
  - `cron(...)` (AWS-style 6-field form). Each field takes `*`, `?`, a value,
    a comma-separated list, a range (`9-17`) or a step (`*/5`, `0/15`,
    `9-17/4`); a step over a range walks that range. The `L`, `W` and `#` day
    specifiers and the three-letter month and day names are **not** supported,
    and an expression using one is refused by `CreateSchedule`.
  - A cron expression is evaluated by advancing field by field, so a sparse
    schedule — yearly, say — costs the same per tick as a frequent one.
- Validation on `CreateSchedule` and `UpdateSchedule`:
  - The schedule and group names must match the model's constraint — 1–64
    characters of `[0-9a-zA-Z-_.]`.
  - The `ScheduleExpression` must be one the engine can evaluate. An expression
    it cannot parse is refused up front rather than accepted and reported as an
    engine error on every tick, which would leave a schedule that reads
    correctly in `GetSchedule` and never fires.
  - `FlexibleTimeWindow.Mode` is required and must be `OFF` or `FLEXIBLE`;
    `State` must be `ENABLED` or `DISABLED`.
- `UpdateSchedule` **replaces**, as AWS's does. The request carries the whole
  schedule, so any optional member the caller omits — `Description`,
  `ScheduleExpressionTimezone`, `State`, `StartDate`, `EndDate`, or anything
  inside `Target` — ends up unset, and `State` returns to its `ENABLED`
  default. Read the schedule, change what you mean to change, and send the
  result back. What survives is the schedule's identity, again as on AWS: its
  name, group, ARN and `CreationDate`.

  Releases up to and including `0.0.1-alpha.33` merged instead, keeping an
  omitted member at its stored value.
- Pagination and filtering:
  - `ListSchedules` and `ListScheduleGroups` honour `MaxResults` (1–100, a full
    page when omitted) and `NextToken`.
  - `ListSchedules` filters on `NamePrefix` and `State`; `ListScheduleGroups`
    filters on `NamePrefix`.
  - A `NextToken` that cannot be decoded is answered with a
    `ValidationException` rather than silently restarting at the first page,
    which an SDK paginator would read as a legitimate page and loop on.
  - Both operations return the full stored object rather than AWS's
    `ScheduleSummary`/`ScheduleGroupSummary` shape. That is a superset, so an
    SDK deserialises it unchanged.
- Background scheduler engine:
  - Polls on a 1-second clock ticker.
  - Uses the injected clock, so integration tests can advance time quickly.
  - A tick hands each due schedule to a pool of delivery workers rather than
    delivering it on the tick itself, so a target that is slow, unreachable or
    working through its `RetryPolicy` delays only its own schedule. A schedule
    is never in flight twice, so its firings stay in order; a tick that finds a
    schedule still mid-delivery leaves it due and skips it.
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

| Operation        | Status       | Notes                                                                                                                                                    | AWS Docs                                                                                  |
| ---------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `CreateSchedule` | ✅ Supported | `POST /schedules/{Name}`; `GroupName` in the body, defaulting to `default`; rejects a target type Overcast cannot fire                                   | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_CreateSchedule.html) |
| `GetSchedule`    | ✅ Supported | `GET /schedules/{Name}`; `?groupName` selects the group                                                                                                  | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_GetSchedule.html)    |
| `UpdateSchedule` | ✅ Supported | `PUT /schedules/{Name}`; `GroupName` in the body; replaces the whole schedule, so an omitted member is unset; rejects a target type Overcast cannot fire | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_UpdateSchedule.html) |
| `DeleteSchedule` | ✅ Supported | `DELETE /schedules/{Name}`; `?groupName` selects the group                                                                                               | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_DeleteSchedule.html) |
| `ListSchedules`  | ✅ Supported | `GET /schedules`; optional `?ScheduleGroup` filter                                                                                                       | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_ListSchedules.html)  |

### Tags

| Operation             | Status       | Notes                                                        | AWS Docs                                                                                       |
| --------------------- | ------------ | ------------------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Merges tags on ARN, at the shared `/tags/{ResourceArn}` path | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes `?TagKeys` from ARN                                  | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns tag map                                              | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
