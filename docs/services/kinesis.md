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
| General  | 23           |

---

## Endpoints

### General

| Operation                       | Status       | Notes                                                                                                                                                                                                    | AWS Docs                                                                                               |
| ------------------------------- | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `AddTagsToStream`               | ✅ Supported |                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_AddTagsToStream.html)               |
| `CreateStream`                  | ✅ Supported | Stream becomes ACTIVE immediately; inline `Tags` and `StreamModeDetails` applied at creation, defaulting to PROVISIONED                                                                                  | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_CreateStream.html)                  |
| `DecreaseStreamRetentionPeriod` | ✅ Supported | Stores and echoes the new value; does not trim any record now older than the shortened window                                                                                                            | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DecreaseStreamRetentionPeriod.html) |
| `DeleteStream`                  | ✅ Supported | Also removes all stored records                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DeleteStream.html)                  |
| `DescribeStream`                | ✅ Supported | Returns full Shards list                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DescribeStream.html)                |
| `DescribeStreamSummary`         | ✅ Supported | Lightweight summary without shard detail                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_DescribeStreamSummary.html)         |
| `GetRecords`                    | ✅ Supported | Returns stored records and a valid NextShardIterator; records are never expired by RetentionPeriodHours, so a shard keeps every record for the life of the stream regardless of the configured retention | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_GetRecords.html)                    |
| `GetShardIterator`              | ✅ Supported | Supports TRIM_HORIZON, LATEST, AT/AFTER_SEQUENCE_NUMBER                                                                                                                                                  | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_GetShardIterator.html)              |
| `IncreaseStreamRetentionPeriod` | ✅ Supported | Stores and echoes the new value from DescribeStream/DescribeStreamSummary; not enforced against stored records (see GetRecords)                                                                          | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_IncreaseStreamRetentionPeriod.html) |
| `ListShards`                    | ✅ Supported | Returns active (open) shards only; no pagination                                                                                                                                                         | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListShards.html)                    |
| `ListStreams`                   | ✅ Supported | Returns all stream names; no pagination                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListStreams.html)                   |
| `ListTagsForResource`           | ✅ Supported | Stream ARNs; the same tag set `ListTagsForStream` returns                                                                                                                                                | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListTagsForResource.html)           |
| `ListTagsForStream`             | ✅ Supported |                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_ListTagsForStream.html)             |
| `MergeShards`                   | ✅ Supported | Closes both parents, creates merged child shard                                                                                                                                                          | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_MergeShards.html)                   |
| `PutRecord`                     | ✅ Supported | Routes by partition key hash                                                                                                                                                                             | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_PutRecord.html)                     |
| `PutRecords`                    | ✅ Supported | Returns FailedRecordCount=0 for all records                                                                                                                                                              | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_PutRecords.html)                    |
| `RemoveTagsFromStream`          | ✅ Supported |                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_RemoveTagsFromStream.html)          |
| `SplitShard`                    | ✅ Supported | Closes parent, creates two children at NewStartingHashKey                                                                                                                                                | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_SplitShard.html)                    |
| `StartStreamEncryption`         | ✅ Supported | Stores EncryptionType/KeyId and echoes them from Describe*; records are not actually encrypted at rest                                                                                                   | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_StartStreamEncryption.html)         |
| `StopStreamEncryption`          | ✅ Supported | Resets EncryptionType to NONE and clears KeyId                                                                                                                                                           | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_StopStreamEncryption.html)          |
| `TagResource`                   | ✅ Supported | Stream ARNs; consumer ARNs are rejected because consumers are not emulated                                                                                                                               | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_TagResource.html)                   |
| `UntagResource`                 | ✅ Supported | Stream ARNs                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_UntagResource.html)                 |
| `UpdateStreamMode`              | ✅ Supported | Stores StreamModeDetails and echoes it from Describe*; on-demand capacity is not actually enforced                                                                                                       | [docs](https://docs.aws.amazon.com/kinesis/latest/APIReference/API_UpdateStreamMode.html)              |

<!-- END overcast:capabilities -->
