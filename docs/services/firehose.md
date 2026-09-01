---
title: "Firehose — Amazon Data Firehose"
description: "Delivery streams as control-plane records. Records are accepted and acknowledged with real record ids, then discarded — nothing is delivered to S3 or any other destination."
section: "Service Reference"
tags:
  - amazon
  - data
  - docs
  - firehose
  - services
---

# Firehose — Amazon Data Firehose

Delivery streams exist as records and accept writes, but there is no delivery:
every record is acknowledged and discarded.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws firehose create-delivery-stream --delivery-stream-name events
aws firehose put-record \
  --delivery-stream-name events \
  --record Data="$(printf 'hello' | base64)"
# → { "RecordId": "…", "Encrypted": false }
```

## What works

| Area | Behaviour |
| --- | --- |
| Delivery streams | Create, describe, list and delete; `ACTIVE` the moment they are created |
| Stream type | `DeliveryStreamType` is honoured, defaulting to `DirectPut` |
| Writes | `PutRecord` and `PutRecordBatch` return a record id per record and `FailedPutCount: 0` |
| Tags | Inline `Tags` at creation, plus `TagDeliveryStream`, `UntagDeliveryStream` and `ListTagsForDeliveryStream` |

## Differences from AWS

| Difference | Detail |
| --- | --- |
| Records are discarded | Nothing reaches S3, Redshift, OpenSearch or an HTTP endpoint — a bucket wired as a destination stays empty |
| Destinations are not stored | `S3DestinationConfiguration` and its siblings are ignored, and `DescribeDeliveryStream` always reports an empty `Destinations` list |
| No transformation | Lambda processors, dynamic partitioning, format conversion and compression are not applied |
| No buffering | There is no buffer interval or size, so nothing is ever flushed |
| No updates | `UpdateDestination` is not implemented |

## Gotchas

> [!WARNING]
> A test that writes to Firehose and then asserts on objects in the
> destination bucket will never pass here. Write to [S3](s3.md) directly, or
> use [Kinesis](kinesis.md), whose records are really stored and readable.

<!-- BEGIN overcast:capabilities -->

## Operations

All 9 listed operations are implemented.
Per-operation status, notes and AWS API links: [Firehose operations](firehose/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Kinesis Data Streams](kinesis.md)
- [AWS API reference](https://docs.aws.amazon.com/firehose/latest/APIReference/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
