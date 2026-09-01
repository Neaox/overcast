---
title: "Firehose — Amazon Data Firehose"
description: "Amazon Data Firehose uses the application/x-amz-json-1.1 protocol. Operations are identified by the X-Amz-Target header with the prefix Firehose_20150804.."
section: "Service Reference"
tags:
  - amazon
  - data
  - docs
  - firehose
  - services
---

# Firehose — Amazon Data Firehose

Amazon Data Firehose uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`Firehose_20150804.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: Firehose_20150804.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Records are accepted and acknowledged but silently discarded — no actual S3 or other destination delivery is performed.

<!-- BEGIN overcast:capabilities -->

## Operations

All 9 listed operations are implemented.
Per-operation status, notes and AWS API links: [Firehose operations](firehose/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/firehose/latest/APIReference/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
