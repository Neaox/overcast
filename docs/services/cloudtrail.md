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

Metadata-only CloudTrail implementation for local development and CDK/Terraform compatibility.

## What works
Supports trail metadata CRUD and logging state toggles. `LookupEvents` is inert and always returns an empty result set.

## Behavior Notes

- No event ingestion or delivery pipeline is implemented.
- No S3 log file delivery is performed.
- `LookupEvents` always returns an empty `Events` list.
- Designed to unblock stacks that require CloudTrail control-plane calls.

<!-- BEGIN overcast:capabilities -->

## Operations

All 12 listed operations are implemented.
Per-operation status, notes and AWS API links: [CloudTrail operations](cloudtrail/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/awscloudtrail/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
