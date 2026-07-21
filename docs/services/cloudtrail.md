---
title: "CloudTrail — AWS CloudTrail"
description: "Metadata-only CloudTrail implementation for local development and CDK/Terraform compatibility."
section: "Service Reference"
tags:
  - aws
  - cloudtrail
  - docs
  - services
---

# CloudTrail — AWS CloudTrail

> AWS docs: https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/Welcome.html

Metadata-only CloudTrail implementation for local development and CDK/Terraform compatibility.

## Summary

Supports trail metadata CRUD and logging state toggles. `LookupEvents` is inert and always returns an empty result set.

## Behavior Notes

- No event ingestion or delivery pipeline is implemented.
- No S3 log file delivery is performed.
- `LookupEvents` always returns an empty `Events` list.
- Designed to unblock stacks that require CloudTrail control-plane calls.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category   | 🧊 Inert |
| ---------- | -------- |
| Operations | 9        |

---

## Endpoints

### Operations

| Operation        | Status   | Notes | AWS Docs                                                                                      |
| ---------------- | -------- | ----- | --------------------------------------------------------------------------------------------- |
| `CreateTrail`    | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_CreateTrail.html)    |
| `DescribeTrails` | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_DescribeTrails.html) |
| `UpdateTrail`    | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_UpdateTrail.html)    |
| `DeleteTrail`    | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_DeleteTrail.html)    |
| `ListTrails`     | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_ListTrails.html)     |
| `GetTrailStatus` | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_GetTrailStatus.html) |
| `StartLogging`   | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_StartLogging.html)   |
| `StopLogging`    | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_StopLogging.html)    |
| `LookupEvents`   | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_LookupEvents.html)   |

<!-- END overcast:capabilities -->
