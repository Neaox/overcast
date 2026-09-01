---
title: "Scheduler operations"
description: "Every Scheduler operation Overcast declares — 12 of 12 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - scheduler
  - services
---

<!-- BEGIN overcast:capabilities -->

# Scheduler operations

All 12 listed operations are implemented. Back to [Scheduler](../scheduler.md).

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

| Operation             | Status       | Notes                                                                                                 | AWS Docs                                                                                       |
| --------------------- | ------------ | ----------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Merges tags on ARN, at the shared `/tags/{ResourceArn}` path; refuses the keys and values AWS refuses | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes `?TagKeys` from ARN                                                                           | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns the modeled `TagList`, ordered by key                                                         | [docs](https://docs.aws.amazon.com/scheduler/latest/APIReference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
