---
title: "Shield — AWS Shield"
description: "AWS Shield (DDoS protection) uses the application/x-amz-json-1.1 protocol. Operations are identified by the X-Amz-Target header with the prefix AWSShield_20160616.."
section: "Service Reference"
tags:
  - aws
  - docs
  - services
  - shield
---

# Shield — AWS Shield

AWS Shield (DDoS protection) uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`AWSShield_20160616.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: AWSShield_20160616.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Protection resources are stored but Shield Advanced features (e.g. DDoS cost protection, response team) are not emulated.

<!-- BEGIN overcast:capabilities -->

## Operations

All 8 listed operations are implemented.
Per-operation status, notes and AWS API links: [Shield operations](shield/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
