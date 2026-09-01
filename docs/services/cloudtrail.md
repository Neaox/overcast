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

## What's covered

Supports trail metadata CRUD and logging state toggles. `LookupEvents` is inert and always returns an empty result set. For the operation count, see [Summary](#summary) at the bottom of this page.

## Behavior Notes

- No event ingestion or delivery pipeline is implemented.
- No S3 log file delivery is performed.
- `LookupEvents` always returns an empty `Events` list.
- Designed to unblock stacks that require CloudTrail control-plane calls.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category   | 🧊 Inert |
| ---------- | -------- |
| Operations | 12       |

---

## Endpoints

### Operations

| Operation        | Status   | Notes                                                          | AWS Docs                                                                                      |
| ---------------- | -------- | -------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `CreateTrail`    | 🧊 Inert | Inline `TagsList` applied at creation                          | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_CreateTrail.html)    |
| `DescribeTrails` | 🧊 Inert |                                                                | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_DescribeTrails.html) |
| `UpdateTrail`    | 🧊 Inert |                                                                | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_UpdateTrail.html)    |
| `DeleteTrail`    | 🧊 Inert |                                                                | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_DeleteTrail.html)    |
| `ListTrails`     | 🧊 Inert |                                                                | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_ListTrails.html)     |
| `GetTrailStatus` | 🧊 Inert |                                                                | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_GetTrailStatus.html) |
| `StartLogging`   | 🧊 Inert |                                                                | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_StartLogging.html)   |
| `StopLogging`    | 🧊 Inert |                                                                | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_StopLogging.html)    |
| `LookupEvents`   | 🧊 Inert |                                                                | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_LookupEvents.html)   |
| `AddTags`        | 🧊 Inert | Trail ARNs; event data stores and channels are not emulated    | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_AddTags.html)        |
| `RemoveTags`     | 🧊 Inert | Matches each `TagsList` entry on its `Key`, ignoring the value | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_RemoveTags.html)     |
| `ListTags`       | 🧊 Inert | One `ResourceTagList` entry per requested ARN; no pagination   | [docs](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/API_ListTags.html)       |

<!-- END overcast:capabilities -->
