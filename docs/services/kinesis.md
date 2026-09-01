---
title: "Kinesis — Amazon Kinesis Data Streams"
description: "Kinesis Data Streams accepts the AWS JSON 1.1 protocol on the shared POST / endpoint with X-Amz-Target: Kinesis_20131202.\u003cOperationName\u003e. It also accepts Smithy RPC v2 CBOR at..."
section: "Service Reference"
tags:
  - amazon
  - data
  - docs
  - kinesis
  - services
  - streams
---

# Kinesis — Amazon Kinesis Data Streams

Kinesis Data Streams accepts the AWS JSON 1.1 protocol on the shared `POST /`
endpoint with `X-Amz-Target: Kinesis_20131202.<OperationName>`. It also accepts
Smithy RPC v2 CBOR at `/service/Kinesis/operation/<OperationName>` with
`Smithy-Protocol: rpc-v2-cbor` and `Content-Type: application/cbor`.

---

<!-- BEGIN overcast:capabilities -->

## Operations

All 23 listed operations are implemented.
Per-operation status, notes and AWS API links: [Kinesis operations](kinesis/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/kinesis/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
