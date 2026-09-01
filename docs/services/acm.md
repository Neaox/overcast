---
title: "ACM — AWS Certificate Manager"
description: "AWS Certificate Manager uses the application/x-amz-json-1.1 protocol. Operations are identified by the X-Amz-Target header with the prefix CertificateManager.."
section: "Service Reference"
tags:
  - acm
  - aws
  - certificate
  - docs
  - manager
  - services
---

# ACM — AWS Certificate Manager

AWS Certificate Manager uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`CertificateManager.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: CertificateManager.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Certificates are immediately issued with status `ISSUED` — no DNS or email validation is performed.

<!-- BEGIN overcast:capabilities -->

## Operations

All 10 listed operations are implemented.
Per-operation status, notes and AWS API links: [ACM operations](acm/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/acm/latest/APIReference/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
