---
title: "Route 53 — Amazon Route 53"
description: "Route 53 is served as a REST-XML API under the /2013-04-01/ path. This implementation focuses on hosted zones, record sets, and change status."
section: "Service Reference"
tags:
  - amazon
  - docs
  - route
  - route53
  - services
---

# Route 53 — Amazon Route 53

> AWS docs: https://docs.aws.amazon.com/Route53/latest/APIReference/Welcome.html

Route 53 is served as a REST-XML API under the `/2013-04-01/` path.
This implementation focuses on hosted zones, record sets, and change status.

## Summary

| Operation                  | Status         | Notes                                                   |
| -------------------------- | -------------- | ------------------------------------------------------- |
| `CreateHostedZone`         | ✅ Supported   | Creates hosted zone metadata and default SOA/NS records |
| `ListHostedZones`          | ✅ Supported   | Lists all hosted zones                                  |
| `GetHostedZone`            | ✅ Supported   | Returns zone details                                    |
| `DeleteHostedZone`         | ✅ Supported   | Deletes hosted zone and associated record sets          |
| `ChangeResourceRecordSets` | ✅ Supported   | UPSERT/DELETE record sets; returns change ID            |
| `ListResourceRecordSets`   | ✅ Supported   | Lists record sets for a zone                            |
| `GetChange`                | ✅ Supported   | Returns `INSYNC` immediately                            |
| `ListHostedZonesByName`    | ❌ Unsupported | Not implemented                                         |
| `CreateTrafficPolicy`      | ❌ Unsupported | Not implemented                                         |
| `CreateHealthCheck`        | ❌ Unsupported | Not implemented                                         |

## Behavior Notes

- Route 53 is treated as a global service in this emulator.
- Hosted zone IDs are stored and returned in AWS-style path format, for example `/hostedzone/Z123...`.
- Change IDs are returned in AWS-style path format, for example `/change/C123...`.
- `ChangeResourceRecordSets` is metadata-only and does not serve real DNS traffic.
- `GetChange` reports `INSYNC` on first poll (no propagation delay simulation).

<!-- BEGIN overcast:capabilities -->

## Summary

| Category          | ✅ Supported | ❌ Unsupported |
| ----------------- | ------------ | -------------- |
| Hosted Zones      | 4            | 1              |
| Resource Records  | 2            |                |
| Change Management | 1            |                |
| Traffic Policies  |              | 1              |
| Health Checks     |              | 1              |

---

## Endpoints

### Hosted Zones

| Operation               | Status         | Notes | AWS Docs                                                                                       |
| ----------------------- | -------------- | ----- | ---------------------------------------------------------------------------------------------- |
| `CreateHostedZone`      | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_CreateHostedZone.html)      |
| `ListHostedZones`       | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListHostedZones.html)       |
| `GetHostedZone`         | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetHostedZone.html)         |
| `DeleteHostedZone`      | ✅ Supported   |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_DeleteHostedZone.html)      |
| `ListHostedZonesByName` | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListHostedZonesByName.html) |

### Resource Records

| Operation                  | Status       | Notes | AWS Docs                                                                                          |
| -------------------------- | ------------ | ----- | ------------------------------------------------------------------------------------------------- |
| `ChangeResourceRecordSets` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ChangeResourceRecordSets.html) |
| `ListResourceRecordSets`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListResourceRecordSets.html)   |

### Change Management

| Operation   | Status       | Notes | AWS Docs                                                                           |
| ----------- | ------------ | ----- | ---------------------------------------------------------------------------------- |
| `GetChange` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_GetChange.html) |

### Traffic Policies

| Operation             | Status         | Notes | AWS Docs                                                                                     |
| --------------------- | -------------- | ----- | -------------------------------------------------------------------------------------------- |
| `CreateTrafficPolicy` | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_CreateTrafficPolicy.html) |

### Health Checks

| Operation           | Status         | Notes | AWS Docs                                                                                   |
| ------------------- | -------------- | ----- | ------------------------------------------------------------------------------------------ |
| `CreateHealthCheck` | ❌ Unsupported |       | [docs](https://docs.aws.amazon.com/Route53/latest/APIReference/API_CreateHealthCheck.html) |

<!-- END overcast:capabilities -->
