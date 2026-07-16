# SQS — Simple Queue Service

> AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/Welcome.html

SQS supports AWS JSON 1.0, AWS Query, and Smithy RPC v2 CBOR. JSON and Query
requests share the root endpoint; the action is identified by the `Action`
query parameter or the `X-Amz-Target` header in SDK requests. RPC v2 CBOR
requests use `/service/AmazonSQS/operation/<Operation>` with
`Smithy-Protocol: rpc-v2-cbor`.

Queue URLs are returned in the form `http://localhost:4566/<account-id>/<queue-name>`.
For local use, `<account-id>` defaults to `000000000000`.

---

---

## Known limitations

- Visibility timeout clocks are wall-clock based. They may drift slightly under
  high load in the in-memory backend.
- Message attribute data types `Binary` and `Number` are stored but not validated.
- SQS → Lambda event source mapping requires the Lambda service; see `lambda.md`.

<!-- BEGIN overcast:capabilities -->

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

| Operation            | Status       | Notes                                            | AWS Docs                                                                                                  |
| -------------------- | ------------ | ------------------------------------------------ | --------------------------------------------------------------------------------------------------------- |
| `CreateQueue`        | ✅ Supported | Idempotent; FIFO queues supported (.fifo suffix) | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_CreateQueue.html)        |
| `DeleteQueue`        | ✅ Supported |                                                  | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteQueue.html)        |
| `GetQueueUrl`        | ✅ Supported |                                                  | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_GetQueueUrl.html)        |
| `ListQueues`         | ✅ Supported | Optional QueueNamePrefix filter                  | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ListQueues.html)         |
| `GetQueueAttributes` | ✅ Supported | All standard attributes; All wildcard supported  | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_GetQueueAttributes.html) |
| `SetQueueAttributes` | ✅ Supported |                                                  | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SetQueueAttributes.html) |
| `PurgeQueue`         | ✅ Supported | Deletes all messages immediately                 | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_PurgeQueue.html)         |
| `ListQueueTags`      | ✅ Supported |                                                  | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ListQueueTags.html)      |
| `TagQueue`           | ✅ Supported | Merges with existing tags                        | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_TagQueue.html)           |
| `UntagQueue`         | ✅ Supported |                                                  | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_UntagQueue.html)         |

### Message operations

| Operation                      | Status       | Notes                                                                                                             | AWS Docs                                                                                                            |
| ------------------------------ | ------------ | ----------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `SendMessage`                  | ✅ Supported | DelaySeconds, MessageAttributes supported                                                                         | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessage.html)                  |
| `SendMessageBatch`             | ✅ Supported | Up to 10 messages per batch                                                                                       | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessageBatch.html)             |
| `ReceiveMessage`               | ✅ Supported | MaxNumberOfMessages, VisibilityTimeout, WaitTimeSeconds, queue default long polling, FIFO ReceiveRequestAttemptId | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ReceiveMessage.html)               |
| `DeleteMessage`                | ✅ Supported |                                                                                                                   | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteMessage.html)                |
| `DeleteMessageBatch`           | ✅ Supported | Up to 10 messages per batch                                                                                       | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteMessageBatch.html)           |
| `ChangeMessageVisibility`      | ✅ Supported | Sets new visibility timeout on an in-flight message                                                               | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibility.html)      |
| `ChangeMessageVisibilityBatch` | ✅ Supported | Batch visibility timeout changes; per-entry success/failure response                                              | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibilityBatch.html) |

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

<!-- END overcast:capabilities -->
