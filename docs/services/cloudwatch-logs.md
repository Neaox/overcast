---
title: "CloudWatch Logs"
description: "CloudWatch Logs accepts the AWS JSON 1.1 API over the shared root endpoint with X-Amz-Target: Logs_20140328.\u003cOperationName\u003e. It also accepts Smithy RPC v2 CBOR at..."
section: "Service Reference"
tags:
  - cloudwatch
  - docs
  - logs
  - services
---

# CloudWatch Logs

> AWS docs: https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/Welcome.html

CloudWatch Logs accepts the AWS JSON 1.1 API over the shared root endpoint
with `X-Amz-Target: Logs_20140328.<OperationName>`. It also accepts Smithy
RPC v2 CBOR at `/service/Logs_20140328/operation/<OperationName>` with
`Smithy-Protocol: rpc-v2-cbor` and `Content-Type: application/cbor`.

Log group names are typically in the form `/aws/lambda/<function-name>` or
`/custom/<app-name>`. Log stream names can be any valid string.

Storage and retention behavior:

- In the SQLite-backed storage modes, log events live in a dedicated indexed table
  (`logs_events`), so appends and time-range reads stay fast regardless of stream size;
  pre-existing blob-format events are converted automatically by a one-time migration on
  first startup after upgrade.
- `RetentionInDays` (set via `PutRetentionPolicy`) is **enforced**: a periodic background
  sweep deletes events older than the group's retention window in every storage mode. Groups
  with no retention policy keep events indefinitely.
- The same sweep also removes a log stream's metadata (its `DescribeLogStreams` entry) once
  its last event has aged out of the retention window and the stream has no events left
  anywhere — matching real CloudWatch Logs, which eventually deletes empty log streams rather
  than leaving a stale entry behind forever. A stream is only removed once it has no persisted
  events, no buffered (not-yet-flushed) events, and a non-zero last-event timestamp — streams
  that have never received an event are never removed, regardless of age.
- Incoming events are briefly write-buffered per stream (~50 ms debounce, flushed early on
  bursts) to coalesce writes; buffers are flushed synchronously on graceful shutdown.

---

<!-- BEGIN overcast:capabilities -->

## Summary

| Category    | ✅ Supported | ❌ Unsupported |
| ----------- | ------------ | -------------- |
| Log groups  | 3            |                |
| Log streams | 3            |                |
| Log events  | 4            |                |
| Insights    |              | 3              |
| Retention   | 2            | 1              |
| Tagging     | 3            |                |

---

## Endpoints

### Log groups

| Operation           | Status       | Notes                                      | AWS Docs                                                                                                |
| ------------------- | ------------ | ------------------------------------------ | ------------------------------------------------------------------------------------------------------- |
| `CreateLogGroup`    | ✅ Supported | Validates name; returns error on duplicate | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_CreateLogGroup.html)    |
| `DescribeLogGroups` | ✅ Supported | Optional `logGroupNamePrefix` filter       | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DescribeLogGroups.html) |
| `DeleteLogGroup`    | ✅ Supported | Deletes group and all streams/events       | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DeleteLogGroup.html)    |

### Log streams

| Operation            | Status       | Notes                                              | AWS Docs                                                                                                 |
| -------------------- | ------------ | -------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `CreateLogStream`    | ✅ Supported | Validates group exists; returns error on duplicate | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_CreateLogStream.html)    |
| `DescribeLogStreams` | ✅ Supported | Optional `logStreamNamePrefix` filter              | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DescribeLogStreams.html) |
| `DeleteLogStream`    | ✅ Supported | Deletes stream and all its events                  | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DeleteLogStream.html)    |

### Log events

| Operation         | Status       | Notes                                                                                                                                                                                                                                                       | AWS Docs                                                                                              |
| ----------------- | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `PutLogEvents`    | ✅ Supported | Accepts batch of events; sets ingestion time                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutLogEvents.html)    |
| `GetLogEvents`    | ✅ Supported | startTime/endTime filtering; startFromHead                                                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_GetLogEvents.html)    |
| `FilterLogEvents` | ✅ Supported | Text patterns (AND, quoted, ?OR), JSON patterns (`{ $.field op value }` with `&&`/`\|\|`, EXISTS, IS NULL), space-delimited patterns (`[col, col = val, ...]` with `*` glob, `%regex%`, numeric ops, `&&`/`\|\|`, ellipsis); time range, stream name/prefix | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_FilterLogEvents.html) |
| `StartLiveTail`   | ✅ Supported | AWS event-stream response with sessionStart/sessionUpdate; supports group identifiers, stream names/prefixes, and filter patterns                                                                                                                           | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_StartLiveTail.html)   |

### Insights

| Operation         | Status         | Notes             | AWS Docs                                                                                              |
| ----------------- | -------------- | ----------------- | ----------------------------------------------------------------------------------------------------- |
| `StartQuery`      | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_StartQuery.html)      |
| `GetQueryResults` | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_GetQueryResults.html) |
| `PutMetricFilter` | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutMetricFilter.html) |

### Retention

| Operation               | Status         | Notes                             | AWS Docs                                                                                                    |
| ----------------------- | -------------- | --------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `PutRetentionPolicy`    | ✅ Supported   | Sets retentionInDays on log group | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutRetentionPolicy.html)    |
| `DeleteRetentionPolicy` | ✅ Supported   | Clears retention (sets to 0)      | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DeleteRetentionPolicy.html) |
| `PutSubscriptionFilter` | ❌ Unsupported | stub; returns 501                 | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutSubscriptionFilter.html) |

### Tagging

| Operation          | Status       | Notes                         | AWS Docs                                                                                               |
| ------------------ | ------------ | ----------------------------- | ------------------------------------------------------------------------------------------------------ |
| `TagLogGroup`      | ✅ Supported | Adds tags to a log group      | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_TagLogGroup.html)      |
| `UntagLogGroup`    | ✅ Supported | Removes tags from a log group | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_UntagLogGroup.html)    |
| `ListTagsLogGroup` | ✅ Supported | Returns tags for a log group  | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_ListTagsLogGroup.html) |

<!-- END overcast:capabilities -->
