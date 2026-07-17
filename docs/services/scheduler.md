---
title: "Scheduler — Amazon EventBridge Scheduler"
description: "EventBridge Scheduler is served as a REST-JSON API under /_scheduler/*. This implementation focuses on schedule groups, schedules, tagging, and clock-driven target dispatch for..."
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
clock-driven target dispatch for common target types.

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
  - Lambda targets dispatch via async invoke when Lambda service is enabled.
  - SQS targets dispatch via raw enqueue when SQS service is enabled.
  - Unknown/unsupported target types are logged and skipped.

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

| Operation        | Status       | Notes                                  | AWS Docs                                                                                  |
| ---------------- | ------------ | -------------------------------------- | ----------------------------------------------------------------------------------------- |
| `CreateSchedule` | ✅ Supported | Group-specific or default group path   | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_CreateSchedule.html) |
| `GetSchedule`    | ✅ Supported | Returns full schedule definition       | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_GetSchedule.html)    |
| `UpdateSchedule` | ✅ Supported | Updates expression/target/state fields | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_UpdateSchedule.html) |
| `DeleteSchedule` | ✅ Supported | Deletes schedule                       | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_DeleteSchedule.html) |
| `ListSchedules`  | ✅ Supported | Optional `ScheduleGroup` filter        | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_ListSchedules.html)  |

### Tags

| Operation             | Status       | Notes                 | AWS Docs                                                                                       |
| --------------------- | ------------ | --------------------- | ---------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Merges tags on ARN    | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes keys from ARN | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns tag map       | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
