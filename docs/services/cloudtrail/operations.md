---
title: "CloudTrail operations"
description: "Every CloudTrail operation Overcast declares — 12 of 12 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - cloudtrail
  - docs
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# CloudTrail operations

All 12 listed operations are implemented. Back to [CloudTrail](../cloudtrail.md).

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

## Related

- [CloudTrail](../cloudtrail.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
