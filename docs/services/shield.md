---
title: "Shield — AWS Shield"
description: "Quick start, the protection, subscription and tag operations that work, and the attack reporting and Shield Advanced surface that is not modelled."
section: "Service Reference"
tags:
  - aws
  - docs
  - services
  - shield
---

# Shield — AWS Shield

Protection records, so a stack that declares Shield resources provisions
cleanly. No traffic is ever inspected or mitigated.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws shield create-protection \
  --name web-alb \
  --resource-arn arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/web/abc

aws shield list-protections
```

## What works

| Area         | Behaviour                                                                 |
| ------------ | ------------------------------------------------------------------------- |
| Protections  | `CreateProtection`, `DescribeProtection` (by ID or resource ARN), `ListProtections`, `DeleteProtection` |
| Subscription | `DescribeSubscription` returns a minimal active subscription              |
| Tags         | `TagResource`, `UntagResource`, `ListTagsForResource` on a protection     |

## Differences from AWS

| Area                   | On AWS                                                      | Overcast                                       |
| ---------------------- | ----------------------------------------------------------- | ---------------------------------------------- |
| DDoS mitigation        | Traffic is inspected and attacks absorbed                   | Nothing is inspected; a protection is a record |
| Attack reporting       | `DescribeAttack`, `ListAttacks`, attack metrics             | Not implemented — `501 Not Implemented`        |
| Shield Advanced extras | Response team access, cost protection, proactive engagement | Not modelled                                   |

<!-- BEGIN overcast:capabilities -->

## Operations

All 8 listed operations are implemented.
Per-operation status, notes and AWS API links: [Shield operations](shield/operations.md).

<!-- END overcast:capabilities -->

## Related

- [WAF](./waf.md) — the other half of the same control plane
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/Welcome.html)
