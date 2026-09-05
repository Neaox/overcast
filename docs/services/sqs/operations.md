---
title: "SQS operations"
description: "Every SQS operation Overcast declares — 19 of 21 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - services
  - sqs
---

<!-- BEGIN overcast:capabilities -->

# SQS operations

19 of 21 listed operations are implemented. Back to [SQS](../sqs.md).

## Summary

| Category           | ✅ Supported | ❌ Unsupported |
| ------------------ | ------------ | -------------- |
| Queue management   | 10           |                |
| Message operations | 7            |                |
| Permissions        |              | 2              |
| Dead-letter queues | 2            |                |

---

## Endpoints

### Queue management

| Operation            | Status       | Notes                                                                                                                                                                                                                                                                           | AWS Docs                                                                                                  |
| -------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `CreateQueue`        | ✅ Supported | Idempotent; FIFO queues supported (.fifo suffix); accepts tags inline                                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_CreateQueue.html)        |
| `DeleteQueue`        | ✅ Supported |                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteQueue.html)        |
| `GetQueueUrl`        | ✅ Supported |                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_GetQueueUrl.html)        |
| `ListQueues`         | ✅ Supported | Optional QueueNamePrefix filter                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ListQueues.html)         |
| `GetQueueAttributes` | ✅ Supported | All standard attributes; All wildcard supported; ApproximateNumberOfMessages(NotVisible) reflects the same counts a background sampler also publishes to CloudWatch every minute as ApproximateNumberOfMessagesVisible/NotVisible/Delayed, whether or not the queue has traffic | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_GetQueueAttributes.html) |
| `SetQueueAttributes` | ✅ Supported | RedriveAllowPolicy accepted, validated, and round-tripped; the redrivePermission restriction itself is not enforced against StartMessageMoveTask or automatic DLQ redrive                                                                                                       | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SetQueueAttributes.html) |
| `PurgeQueue`         | ✅ Supported | Deletes all messages immediately                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_PurgeQueue.html)         |
| `ListQueueTags`      | ✅ Supported |                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ListQueueTags.html)      |
| `TagQueue`           | ✅ Supported | Merges with existing tags                                                                                                                                                                                                                                                       | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_TagQueue.html)           |
| `UntagQueue`         | ✅ Supported |                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_UntagQueue.html)         |

### Message operations

| Operation                      | Status       | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                        | AWS Docs                                                                                                            |
| ------------------------------ | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `SendMessage`                  | ✅ Supported | DelaySeconds, MessageAttributes supported; records AWS/SQS CloudWatch metrics NumberOfMessagesSent and SentMessageSize, skipped for a FIFO content-based-deduplication resend since no new message is enqueued                                                                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessage.html)                  |
| `SendMessageBatch`             | ✅ Supported | Up to 10 messages per batch; records NumberOfMessagesSent/SentMessageSize per successful entry                                                                                                                                                                                                                                                                                                                                               | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessageBatch.html)             |
| `ReceiveMessage`               | ✅ Supported | MaxNumberOfMessages, VisibilityTimeout, WaitTimeSeconds, queue default long polling, FIFO ReceiveRequestAttemptId with its 5-minute replay window; a FIFO batch drains one message group in sequence order before filling from other unblocked groups; the in-flight OverLimit quota is not enforced; records NumberOfMessagesReceived (non-empty) or NumberOfEmptyReceives (zero messages) once per call, after any long-poll retry settles | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ReceiveMessage.html)               |
| `DeleteMessage`                | ✅ Supported | Records NumberOfMessagesDeleted                                                                                                                                                                                                                                                                                                                                                                                                              | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteMessage.html)                |
| `DeleteMessageBatch`           | ✅ Supported | Up to 10 messages per batch; records NumberOfMessagesDeleted per successful entry                                                                                                                                                                                                                                                                                                                                                            | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteMessageBatch.html)           |
| `ChangeMessageVisibility`      | ✅ Supported | Sets new visibility timeout on an in-flight message                                                                                                                                                                                                                                                                                                                                                                                          | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibility.html)      |
| `ChangeMessageVisibilityBatch` | ✅ Supported | Batch visibility timeout changes; per-entry success/failure response                                                                                                                                                                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibilityBatch.html) |

### Permissions

| Operation          | Status         | Notes             | AWS Docs                                                                                                |
| ------------------ | -------------- | ----------------- | ------------------------------------------------------------------------------------------------------- |
| `AddPermission`    | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_AddPermission.html)    |
| `RemovePermission` | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_RemovePermission.html) |

### Dead-letter queues

| Operation                    | Status       | Notes                                                 | AWS Docs                                                                                                          |
| ---------------------------- | ------------ | ----------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `ListDeadLetterSourceQueues` | ✅ Supported | Lists queues that target a given DLQ                  | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ListDeadLetterSourceQueues.html) |
| `StartMessageMoveTask`       | ✅ Supported | Redrives messages from a DLQ back to its source queue | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_StartMessageMoveTask.html)       |

## Related

- [SQS](../sqs.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
