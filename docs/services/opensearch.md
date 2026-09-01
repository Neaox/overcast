---
title: "OpenSearch — Amazon OpenSearch Service"
description: "Amazon OpenSearch Service uses the REST JSON protocol, served under the /2021-01-01/ API version prefix."
section: "Service Reference"
tags:
  - amazon
  - docs
  - opensearch
  - service
  - services
---

# OpenSearch — Amazon OpenSearch Service

Amazon OpenSearch Service uses the REST JSON protocol, served under the
`/2021-01-01/` API version prefix that every OpenSearch binding carries. AWS
SDKs and `aws opensearch …` work unmodified.

---

## Notes

- Routes are AWS's own bindings. The domain surface is under
  `/2021-01-01/opensearch/` (e.g. `POST /2021-01-01/opensearch/domain`), while
  `ListDomainNames` and the tag operations sit directly under `/2021-01-01/`.
- Domains are per-region, as on AWS: the same domain name in two regions is two
  domains.
- Operations that are not emulated return a JSON `501 Not Implemented` error
  response.
- Domain records are stored, but no OpenSearch cluster is provisioned, so
  `DomainStatus.Endpoint` names a host that serves nothing.

<!-- BEGIN overcast:capabilities -->

## Operations

All 8 listed operations are implemented.
Per-operation status, notes and AWS API links: [OpenSearch operations](opensearch/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
