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

> AWS docs: https://docs.aws.amazon.com/firehose/latest/APIReference/

Amazon Data Firehose uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`Firehose_20150804.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: Firehose_20150804.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Records are accepted and acknowledged but silently discarded — no actual S3 or other destination delivery is performed.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category         | ✅ Supported |
| ---------------- | ------------ |
| Delivery Streams | 4            |
| Records          | 2            |

---

## Endpoints

### Delivery Streams

| Operation                | Status       | Notes                           | AWS Docs                                                                                         |
| ------------------------ | ------------ | ------------------------------- | ------------------------------------------------------------------------------------------------ |
| `CreateDeliveryStream`   | ✅ Supported | Creates a delivery stream       | [docs](https://docs.aws.amazon.com/firehose/latest/APIReference/API_CreateDeliveryStream.html)   |
| `DescribeDeliveryStream` | ✅ Supported | Returns delivery stream details | [docs](https://docs.aws.amazon.com/firehose/latest/APIReference/API_DescribeDeliveryStream.html) |
| `ListDeliveryStreams`    | ✅ Supported | Lists all delivery streams      | [docs](https://docs.aws.amazon.com/firehose/latest/APIReference/API_ListDeliveryStreams.html)    |
| `DeleteDeliveryStream`   | ✅ Supported | Deletes a delivery stream       | [docs](https://docs.aws.amazon.com/firehose/latest/APIReference/API_DeleteDeliveryStream.html)   |

### Records

| Operation        | Status       | Notes                                 | AWS Docs                                                                                 |
| ---------------- | ------------ | ------------------------------------- | ---------------------------------------------------------------------------------------- |
| `PutRecord`      | ✅ Supported | Writes a single record to the stream  | [docs](https://docs.aws.amazon.com/firehose/latest/APIReference/API_PutRecord.html)      |
| `PutRecordBatch` | ✅ Supported | Writes multiple records to the stream | [docs](https://docs.aws.amazon.com/firehose/latest/APIReference/API_PutRecordBatch.html) |

<!-- END overcast:capabilities -->
