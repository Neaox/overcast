---
title: "WAF — AWS WAF v2"
description: "Metadata-only AWS WAF v2 Web ACL CRUD for SDK and CloudFormation workflows; rules are stored but are not evaluated or enforced."
section: "Service Reference"
tags:
  - aws
  - docs
  - services
  - waf
---

# WAF — AWS WAF v2

AWS WAF v2 (Web Application Firewall) uses the `application/x-amz-json-1.1`
protocol. Operations are identified by the `X-Amz-Target` header with the
prefix `AWSWAF_20190729.`.

Overcast provides a deliberately small, metadata-only WAFv2 control plane. It
can create, read, list, and delete Web ACL records for SDK and
`AWS::WAFv2::WebACL` CloudFormation workflows. Web ACL configuration is
persisted as metadata only: Overcast does not evaluate it or allow/block
requests to API Gateway, CloudFront, or Application Load Balancers.

---

## Notes

- Target dispatch header: `X-Amz-Target: AWSWAF_20190729.<Operation>`.
- Supported operations: `CreateWebACL`, `GetWebACL`, `ListWebACLs`, and
  `DeleteWebACL`.
- `DeleteWebACL` accepts `LockToken`, but does not validate it.
- All other WAFv2 operations return a JSON `501 Not Implemented` error response.
- WAF Classic (`AWSWAF_20150824`) is not implemented and returns `501`.

## Web UI and system map

The Web UI provides create, list, detail, and delete views for WAFv2 Web ACL
metadata. Creation uses an `Allow` default action, no rules, and disabled
metrics because `UpdateWebACL` is not implemented. Global search includes Web
ACLs from both `REGIONAL` and `CLOUDFRONT` scopes.

Stored Web ACLs also appear on the system map with their scope and stored rule
count. Selecting a node opens its detail view. This visualization represents
control-plane metadata only and does not imply that WAF rules protect or route
traffic.

<!-- BEGIN overcast:capabilities -->

## Operations

All 7 listed operations are implemented.
Per-operation status, notes and AWS API links: [WAF v2 operations](waf/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/waf/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
