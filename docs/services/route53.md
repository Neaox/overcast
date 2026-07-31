---
title: "Route 53 — Amazon Route 53"
description: "Route 53 is served as a REST-XML API under the /2013-04-01/ path. Hosted zones, record sets, tags, and health checks are emulated at inert level: full metadata CRUD with AWS-faithful validation, but no DNS is actually served."
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

Route 53 is served as a REST-XML API under the `/2013-04-01/` path. Hosted
zones, resource record sets, tags, and health checks are emulated at **inert
level**: resources are real metadata with AWS-faithful validation rules, error
codes, defaults, auto-created child records, and pagination — but no DNS
queries are actually answered and no health check probes are sent.

## Behavior Notes

- Route 53 is treated as a global service in this emulator.
- Hosted zone IDs are stored and returned in AWS-style path format, for example
  `/hostedzone/Z123...`; change IDs likewise (`/change/C123...`).
- Errors use Route 53's `ErrorResponse` envelope (`Type`/`Code`/`Message`),
  not S3's bare `<Error>` shape.
- `CreateHostedZone` requires a `CallerReference` and rejects a reused one with
  `HostedZoneAlreadyExists` (HTTP 409). Each new zone gets the default apex
  `NS` and `SOA` record sets and a four-server delegation set, derived
  deterministically from the zone ID. The new zone's URL is returned in the
  `Location` header, as on real AWS.
- Domain and record names are canonicalised to lowercase with a trailing dot.
- `ChangeResourceRecordSets` validates the whole batch atomically before
  applying anything: `InvalidChangeBatch` for creating an existing record,
  deleting a missing record or one whose provided values don't match, records
  outside the zone, a CNAME at the zone apex, and deleting the default apex
  NS/SOA records. Weighted/latency/failover/geolocation routing metadata
  (`SetIdentifier`, `Weight`, `Region`, `Failover`, `GeoLocation`,
  `MultiValueAnswer`, `HealthCheckId`) is stored and returned, but routing is
  not evaluated.
- `ListResourceRecordSets` returns records in DNS order (names compared with
  labels reversed) and paginates with `name`/`type`/`identifier`/`maxitems`.
- `DeleteHostedZone` returns `HostedZoneNotEmpty` while non-default records
  exist; a successful delete cascades the default records and the zone's tags.
- Health checks are metadata-only: configuration is stored with AWS defaults
  (`RequestInterval` 30, `FailureThreshold` 3, port 80/443 by type) and
  versioned by `UpdateHealthCheck`, but no endpoint is ever probed.

### Known divergences

- `ChangeResourceRecordSets` applies synchronously, so change status is
  `INSYNC` immediately — there is no `PENDING` phase.
- `GetChange` reports `INSYNC` for unknown change IDs instead of
  `NoSuchChange`, so CDK/CLI waiters always converge (even across a store
  reset).
- Private hosted zones may be created without a VPC (real AWS requires one);
  the VPC passed to `CreateHostedZone` is stored and returned, but
  `AssociateVPCWithHostedZone`/`DisassociateVPCFromHostedZone` are not
  implemented.
- `InvalidChangeBatch`/`InvalidInput` message texts approximate AWS's wording;
  codes and HTTP statuses match.

<!-- BEGIN overcast:capabilities -->

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

<!-- END overcast:capabilities -->
