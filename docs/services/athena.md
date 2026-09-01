---
title: "Athena — Amazon Athena"
description: "Amazon Athena uses the application/x-amz-json-1.1 protocol. Operations are identified by the X-Amz-Target header with the prefix AmazonAthena.."
section: "Service Reference"
tags:
  - amazon
  - athena
  - docs
  - services
---

# Athena — Amazon Athena

Amazon Athena uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`AmazonAthena.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: AmazonAthena.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Queries immediately succeed with status `SUCCEEDED` and return empty result sets — no actual query execution is performed.

<!-- BEGIN overcast:capabilities -->

## Operations

All 11 listed operations are implemented.
Per-operation status, notes and AWS API links: [Athena operations](athena/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/athena/latest/APIReference/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
