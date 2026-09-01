---
title: "DynamoDB Streams"
description: "Change records for DynamoDB tables, read through shard iterators. One shard per stream, records are never trimmed, and a stream belongs to its table's region."
section: "Service Reference"
tags:
  - docs
  - dynamodb
  - dynamodbstreams
  - services
  - streams
---

# DynamoDB Streams

Every write to a stream-enabled table produces a change record. A stream has one
shard, and its records are never trimmed.

**Status:** ✅ Supported

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws dynamodb update-table --table-name orders \
  --stream-specification StreamEnabled=true,StreamViewType=NEW_AND_OLD_IMAGES
ARN=$(aws dynamodbstreams list-streams --query 'Streams[0].StreamArn' --output text)

SHARD=$(aws dynamodbstreams describe-stream --stream-arn "$ARN" \
  --query 'StreamDescription.Shards[0].ShardId' --output text)
ITER=$(aws dynamodbstreams get-shard-iterator --stream-arn "$ARN" \
  --shard-id "$SHARD" --shard-iterator-type TRIM_HORIZON \
  --query ShardIterator --output text)
aws dynamodbstreams get-records --shard-iterator "$ITER"
```

## What works

| Area | Behaviour |
| --- | --- |
| View types | `KEYS_ONLY`, `NEW_IMAGE`, `OLD_IMAGE` and `NEW_AND_OLD_IMAGES` all produce the images AWS documents |
| Iterators | `TRIM_HORIZON`, `LATEST`, `AT_SEQUENCE_NUMBER` and `AFTER_SEQUENCE_NUMBER` |
| Consumers | Lambda event source mappings and EventBridge Pipes poll table streams |
| Regions | A stream belongs to its table's region, and every record carries `awsRegion` |
| Protocols | AWS JSON 1.0 on the shared root endpoint, and Smithy RPC v2 CBOR |

## Differences from AWS

| Difference | Detail |
| --- | --- |
| One shard per stream | The shard id is derived from the table name and never rolls over, so shard-splitting and parent/child traversal cannot be exercised |
| Nothing is trimmed | AWS discards stream records after 24 hours; here they survive for the life of the table, so `TRIM_HORIZON` always replays from the first write |
| Region-scoped ARNs | A stream ARN from another region answers `ResourceNotFoundException` on `DescribeStream` and `GetShardIterator`, as AWS's regional endpoints do |
| No cross-region triggers | A Lambda event source mapping or pipe naming one region's stream is not fired by writes to a same-named table in another region |

<!-- BEGIN overcast:capabilities -->

## Operations

All 4 listed operations are implemented.
Per-operation status, notes and AWS API links: [DynamoDB Streams operations](dynamodbstreams/operations.md).

<!-- END overcast:capabilities -->

## Related

- [DynamoDB](dynamodb.md) — where streams are enabled
- [Kinesis Data Streams](kinesis.md)
- [AWS API reference](https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Operations_Amazon_DynamoDB_Streams.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
