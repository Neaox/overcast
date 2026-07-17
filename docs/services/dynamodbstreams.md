---
title: "DynamoDB Streams"
description: "DynamoDB Streams accepts the AWS JSON 1.0 API over the shared root endpoint with X-Amz-Target: DynamoDBStreams_20120810.\u003cOperation\u003e. It also accepts Smithy RPC v2 CBOR at..."
section: "Service Reference"
tags:
  - docs
  - dynamodb
  - dynamodbstreams
  - services
  - streams
---

# DynamoDB Streams

> AWS docs: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Operations_Amazon_DynamoDB_Streams.html

DynamoDB Streams accepts the AWS JSON 1.0 API over the shared root endpoint
with `X-Amz-Target: DynamoDBStreams_20120810.<Operation>`. It also accepts
Smithy RPC v2 CBOR at `/service/DynamoDBStreams/operation/<Operation>` with
`Smithy-Protocol: rpc-v2-cbor` and `Content-Type: application/cbor`.

---

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| General  | 4            |

---

## Endpoints

### General

| Operation          | Status       | Notes | AWS Docs                                                                                         |
| ------------------ | ------------ | ----- | ------------------------------------------------------------------------------------------------ |
| `DescribeStream`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeStream.html)   |
| `GetRecords`       | ✅ Supported |       | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetRecords.html)       |
| `GetShardIterator` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetShardIterator.html) |
| `ListStreams`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListStreams.html)      |

<!-- END overcast:capabilities -->
