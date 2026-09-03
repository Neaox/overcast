---
title: "CloudTrail — AWS CloudTrail"
description: "Quick start, the trail, logging-state and CloudFormation coverage, and why every operation is inert: nothing is recorded, delivered or looked up."
section: "Service Reference"
tags:
  - aws
  - cloudtrail
  - docs
  - services
---

# CloudTrail — AWS CloudTrail

Trail metadata and logging state exist so a stack that declares a trail
deploys. Nothing is ever audited: no events are recorded and none are
delivered.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
aws s3 mb s3://audit
aws cloudtrail create-trail --name main --s3-bucket-name audit
aws cloudtrail start-logging --name main
aws cloudtrail get-trail-status --name main
```

## What works

| Area | Behaviour |
| --- | --- |
| Trails | Create, describe, list, update and delete, with tags applied inline at creation |
| Logging state | `StartLogging` and `StopLogging` toggle the flag `GetTrailStatus` reports |
| Tagging | `AddTags`, `RemoveTags` and `ListTags` on trail ARNs |
| CloudFormation | `AWS::CloudTrail::Trail` provisions, updates, deletes and syncs tags |
| Protocols | AWS JSON 1.0 and Smithy RPC v2 CBOR |

## Differences from AWS

| Area | Overcast |
| --- | --- |
| Event ingestion | None. No API call is recorded anywhere |
| `LookupEvents` | Always returns an empty `Events` list, whatever the filters |
| S3 delivery | No log file is ever written to the trail's bucket |
| Event data stores, channels, Insights | Not emulated. Tagging operations accept trail ARNs only |
| Region scoping | Trails are global to the emulator; `HomeRegion` always reports the configured default region |

## Gotchas

> [!NOTE]
> Every CloudTrail operation is 🧊 Inert: the call is accepted and answered
> correctly, and nothing happens as a result. A stack that requires CloudTrail
> control-plane calls deploys; nothing it does is audited.

<!-- BEGIN overcast:capabilities -->

## Operations

All 12 listed operations are implemented.
Per-operation status, notes and AWS API links: [CloudTrail operations](cloudtrail/operations.md).

<!-- END overcast:capabilities -->

## Related

- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/Welcome.html)
