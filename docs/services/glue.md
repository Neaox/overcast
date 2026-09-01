---
title: "Glue — AWS Glue Data Catalog"
description: "AWS Glue Data Catalog uses the application/x-amz-json-1.1 protocol. Operations are identified by the X-Amz-Target header with the prefix AWSGlue.."
section: "Service Reference"
tags:
  - aws
  - catalog
  - data
  - docs
  - glue
  - services
---

# Glue — AWS Glue Data Catalog

AWS Glue Data Catalog uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`AWSGlue.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: AWSGlue.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Only the Data Catalog subset of Glue is emulated (databases and tables). ETL jobs, crawlers, and workflows are not supported.

<!-- BEGIN overcast:capabilities -->

## Operations

All 11 listed operations are implemented.
Per-operation status, notes and AWS API links: [Glue operations](glue/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/glue/latest/webapi/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
