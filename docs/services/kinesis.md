# Kinesis — Amazon Kinesis Data Streams

> AWS docs: https://docs.aws.amazon.com/kinesis/latest/APIReference/Welcome.html

Kinesis Data Streams accepts the AWS JSON 1.1 protocol on the shared `POST /`
endpoint with `X-Amz-Target: Kinesis_20131202.<OperationName>`. It also accepts
Smithy RPC v2 CBOR at `/service/Kinesis/operation/<OperationName>` with
`Smithy-Protocol: rpc-v2-cbor` and `Content-Type: application/cbor`.

---

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| General  | 17           |

---

## Endpoints

### General

| Operation                       | Status       | Notes                                                     | AWS Docs                                                                                               |
| ------------------------------- | ------------ | --------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `AddTagsToStream`               | ✅ Supported |                                                           | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_AddTagsToStream.html)               |
| `CreateStream`                  | ✅ Supported | Stream becomes ACTIVE immediately                         | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_CreateStream.html)                  |
| `DecreaseStreamRetentionPeriod` | ✅ Supported |                                                           | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DecreaseStreamRetentionPeriod.html) |
| `DeleteStream`                  | ✅ Supported | Also removes all stored records                           | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DeleteStream.html)                  |
| `DescribeStream`                | ✅ Supported | Returns full Shards list                                  | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DescribeStream.html)                |
| `DescribeStreamSummary`         | ✅ Supported | Lightweight summary without shard detail                  | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DescribeStreamSummary.html)         |
| `GetRecords`                    | ✅ Supported | Returns stored records and a valid NextShardIterator      | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_GetRecords.html)                    |
| `GetShardIterator`              | ✅ Supported | Supports TRIM_HORIZON, LATEST, AT/AFTER_SEQUENCE_NUMBER   | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_GetShardIterator.html)              |
| `IncreaseStreamRetentionPeriod` | ✅ Supported |                                                           | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_IncreaseStreamRetentionPeriod.html) |
| `ListShards`                    | ✅ Supported | Returns active (open) shards only; no pagination          | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListShards.html)                    |
| `ListStreams`                   | ✅ Supported | Returns all stream names; no pagination                   | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListStreams.html)                   |
| `ListTagsForStream`             | ✅ Supported |                                                           | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListTagsForStream.html)             |
| `MergeShards`                   | ✅ Supported | Closes both parents, creates merged child shard           | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_MergeShards.html)                   |
| `PutRecord`                     | ✅ Supported | Routes by partition key hash                              | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_PutRecord.html)                     |
| `PutRecords`                    | ✅ Supported | Returns FailedRecordCount=0 for all records               | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_PutRecords.html)                    |
| `RemoveTagsFromStream`          | ✅ Supported |                                                           | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_RemoveTagsFromStream.html)          |
| `SplitShard`                    | ✅ Supported | Closes parent, creates two children at NewStartingHashKey | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_SplitShard.html)                    |

<!-- END overcast:capabilities -->
