---
title: "CloudWatch Logs operations"
description: "Every CloudWatch Logs operation Overcast declares — 18 of 22 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - cloudwatch
  - docs
  - logs
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# CloudWatch Logs operations

18 of 22 listed operations are implemented. Back to [CloudWatch Logs](../cloudwatch-logs.md).

## Summary

| Category    | ✅ Supported | ❌ Unsupported |
| ----------- | ------------ | -------------- |
| Log groups  | 3            |                |
| Log streams | 3            |                |
| Log events  | 4            |                |
| Insights    |              | 3              |
| Retention   | 2            | 1              |
| Tagging     | 6            |                |

---

## Endpoints

### Log groups

| Operation           | Status       | Notes                                                                                                                                                                                   | AWS Docs                                                                                                |
| ------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `CreateLogGroup`    | ✅ Supported | Validates name; returns error on duplicate; applies create-time `tags` atomically with the group (`kmsKeyId`, `logGroupClass` and `deletionProtectionEnabled` are accepted but ignored) | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_CreateLogGroup.html)    |
| `DescribeLogGroups` | ✅ Supported | Optional `logGroupNamePrefix` filter                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DescribeLogGroups.html) |
| `DeleteLogGroup`    | ✅ Supported | Deletes group and all streams/events                                                                                                                                                    | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DeleteLogGroup.html)    |

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

| Operation               | Status         | Notes                                                                                                                                        | AWS Docs                                                                                                    |
| ----------------------- | -------------- | -------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `PutRetentionPolicy`    | ✅ Supported   | Sets retentionInDays on log group; values outside AWS's documented set are rejected with `InvalidParameterException` before any state change | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutRetentionPolicy.html)    |
| `DeleteRetentionPolicy` | ✅ Supported   | Clears retention (sets to 0)                                                                                                                 | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_DeleteRetentionPolicy.html) |
| `PutSubscriptionFilter` | ❌ Unsupported | stub; returns 501                                                                                                                            | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_PutSubscriptionFilter.html) |

### Tagging

| Operation             | Status       | Notes                                                                                                                             | AWS Docs                                                                                                  |
| --------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `TagLogGroup`         | ✅ Supported | Adds tags to a log group; enforces AWS's key/value length, reserved `aws:` prefix and 50-tag limits before mutating               | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_TagLogGroup.html)         |
| `UntagLogGroup`       | ✅ Supported | Removes tags from a log group                                                                                                     | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_UntagLogGroup.html)       |
| `ListTagsLogGroup`    | ✅ Supported | Returns tags for a log group                                                                                                      | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_ListTagsLogGroup.html)    |
| `TagResource`         | ✅ Supported | Modern, ARN-addressed sibling of TagLogGroup (#1195); resolves `resourceArn` to a log group and shares its validation and storage | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Modern, ARN-addressed sibling of UntagLogGroup (#1195)                                                                            | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Modern, ARN-addressed sibling of ListTagsLogGroup (#1195)                                                                         | [docs](https://docs.aws.amazon.com/AmazonCloudWatchLogs/latest/APIReference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
