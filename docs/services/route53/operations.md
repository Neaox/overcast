---
title: "Route 53 operations"
description: "Every Route 53 operation Overcast declares — 19 of 25 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - route53
  - services
---

<!-- BEGIN overcast:capabilities -->

# Route 53 operations

19 of 25 listed operations are implemented. Back to [Route 53](../route53.md).

## Summary

| Category          | ✅ Supported | ❌ Unsupported |
| ----------------- | ------------ | -------------- |
| Hosted Zones      | 7            |                |
| Resource Records  | 2            |                |
| Change Management | 1            |                |
| Tags              | 3            |                |
| Health Checks     | 6            | 2              |
| Traffic Policies  |              | 1              |
| VPC Associations  |              | 3              |

---

## Endpoints

### Hosted Zones

| Operation                 | Status       | Notes | AWS Docs                                                                                         |
| ------------------------- | ------------ | ----- | ------------------------------------------------------------------------------------------------ |
| `CreateHostedZone`        | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_CreateHostedZone.html)        |
| `ListHostedZones`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListHostedZones.html)         |
| `ListHostedZonesByName`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListHostedZonesByName.html)   |
| `GetHostedZone`           | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetHostedZone.html)           |
| `GetHostedZoneCount`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetHostedZoneCount.html)      |
| `UpdateHostedZoneComment` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_UpdateHostedZoneComment.html) |
| `DeleteHostedZone`        | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_DeleteHostedZone.html)        |

### Resource Records

| Operation                  | Status       | Notes | AWS Docs                                                                                          |
| -------------------------- | ------------ | ----- | ------------------------------------------------------------------------------------------------- |
| `ChangeResourceRecordSets` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ChangeResourceRecordSets.html) |
| `ListResourceRecordSets`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListResourceRecordSets.html)   |

### Change Management

| Operation   | Status       | Notes | AWS Docs                                                                           |
| ----------- | ------------ | ----- | ---------------------------------------------------------------------------------- |
| `GetChange` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetChange.html) |

### Tags

| Operation               | Status       | Notes | AWS Docs                                                                                       |
| ----------------------- | ------------ | ----- | ---------------------------------------------------------------------------------------------- |
| `ChangeTagsForResource` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ChangeTagsForResource.html) |
| `ListTagsForResource`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListTagsForResource.html)   |
| `ListTagsForResources`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListTagsForResources.html)  |

### Health Checks

| Operation                         | Status         | Notes | AWS Docs                                                                                                 |
| --------------------------------- | -------------- | ----- | -------------------------------------------------------------------------------------------------------- |
| `CreateHealthCheck`               | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_CreateHealthCheck.html)               |
| `GetHealthCheck`                  | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetHealthCheck.html)                  |
| `ListHealthChecks`                | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListHealthChecks.html)                |
| `GetHealthCheckCount`             | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetHealthCheckCount.html)             |
| `UpdateHealthCheck`               | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_UpdateHealthCheck.html)               |
| `DeleteHealthCheck`               | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_DeleteHealthCheck.html)               |
| `GetHealthCheckStatus`            | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetHealthCheckStatus.html)            |
| `GetHealthCheckLastFailureReason` | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetHealthCheckLastFailureReason.html) |

### Traffic Policies

| Operation             | Status         | Notes | AWS Docs                                                                                     |
| --------------------- | -------------- | ----- | -------------------------------------------------------------------------------------------- |
| `CreateTrafficPolicy` | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_CreateTrafficPolicy.html) |

### VPC Associations

| Operation                       | Status         | Notes | AWS Docs                                                                                               |
| ------------------------------- | -------------- | ----- | ------------------------------------------------------------------------------------------------------ |
| `AssociateVPCWithHostedZone`    | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_AssociateVPCWithHostedZone.html)    |
| `DisassociateVPCFromHostedZone` | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_DisassociateVPCFromHostedZone.html) |
| `ListHostedZonesByVPC`          | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListHostedZonesByVPC.html)          |

## Related

- [Route 53](../route53.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
