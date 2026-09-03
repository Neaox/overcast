---
title: "WAF — AWS WAF v2"
description: "Quick start, the Web ACL and tag operations that work, and why no request is ever evaluated against a stored rule."
section: "Service Reference"
tags:
  - aws
  - docs
  - services
  - waf
---

# WAF — AWS WAF v2

Web ACL records for SDK and `AWS::WAFv2::WebACL` workflows. Rules are stored
verbatim and never evaluated — nothing is allowed or blocked.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws wafv2 create-web-acl \
  --name api-acl --scope REGIONAL \
  --default-action Allow={} \
  --visibility-config SampledRequestsEnabled=false,CloudWatchMetricsEnabled=false,MetricName=api

aws wafv2 list-web-acls --scope REGIONAL
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

## What works

| Area     | Behaviour                                                                  |
| -------- | -------------------------------------------------------------------------- |
| Web ACLs | `CreateWebACL`, `GetWebACL`, `ListWebACLs`, `DeleteWebACL` in both `REGIONAL` and `CLOUDFRONT` scope |
| Tags     | `TagResource`, `UntagResource`, `ListTagsForResource` by Web ACL ARN        |
| Console  | Create, list, detail and delete views, plus a node on the system map        |

## Differences from AWS

| Area            | On AWS                                                    | Overcast                                           |
| --------------- | --------------------------------------------------------- | -------------------------------------------------- |
| Rule evaluation | Requests to API Gateway, CloudFront and ALBs are filtered | Rules are stored metadata; no request is evaluated |
| `UpdateWebACL`  | Edits rules and visibility config on an existing ACL      | Not implemented — `501 Not Implemented`            |
| `LockToken`     | Rejects a stale token with `WAFOptimisticLockException`   | Accepted and ignored                               |
| Association     | `AssociateWebACL` attaches an ACL to a resource           | Not implemented                                    |
| WAF Classic     | The `AWSWAF_20150824` API is still served                 | Not implemented — `501 Not Implemented`            |

## Gotchas

> [!NOTE]
> Everything unlisted above returns `501 Not Implemented` rather than a
> success that did nothing.

<!-- BEGIN overcast:capabilities -->

## Operations

All 7 listed operations are implemented.
Per-operation status, notes and AWS API links: [WAF v2 operations](waf/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Shield](./shield.md) — the other half of the same control plane
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/waf/latest/APIReference/Welcome.html)
