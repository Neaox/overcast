---
title: "DynamoDB Streams operations"
description: "Every DynamoDB Streams operation Overcast declares — 4 of 4 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - dynamodbstreams
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# DynamoDB Streams operations

All 4 listed operations are implemented. Back to [DynamoDB Streams](../dynamodbstreams.md).

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| General  | 4            |

---

## Endpoints

### General

| Operation          | Status       | Notes                                                                                             | AWS Docs                                                                                         |
| ------------------ | ------------ | ------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `DescribeStream`   | ✅ Supported | A stream ARN from another region is a `ResourceNotFoundException`, as on AWS's regional endpoints | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeStream.html)   |
| `GetRecords`       | ✅ Supported | Reads the stream in the region its shard iterator names; each record carries `awsRegion`          | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetRecords.html)       |
| `GetShardIterator` | ✅ Supported |                                                                                                   | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_GetShardIterator.html) |
| `ListStreams`      | ✅ Supported | Region-scoped — reports only streams for tables in the request's region                           | [docs](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ListStreams.html)      |

## Related

- [DynamoDB Streams](../dynamodbstreams.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
