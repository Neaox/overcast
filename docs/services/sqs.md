# SQS — Simple Queue Service

> AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/Welcome.html

SQS uses a JSON (or form-encoded) API over HTTPS. All operations share a single
endpoint URL; the action is identified by the `Action` query parameter or the
`X-Amz-Target` header in SDK requests.

Queue URLs are returned in the form `http://localhost:4566/<account-id>/<queue-name>`.
For local use, `<account-id>` defaults to `000000000000`.

---

## Summary

| Category | ✅ Supported | ⚠️ Partial | 🚧 WIP | ❌ Unsupported |
|----------|------------|-----------|--------|--------------|
| Queue management | 0 | 0 | 0 | 7 |
| Message operations | 0 | 0 | 0 | 5 |
| Permissions | 0 | 0 | 0 | 2 |
| Dead-letter queues | 0 | 0 | 0 | 2 |
| FIFO queues | 0 | 0 | 0 | 3 |

---

## Endpoints

### Queue management

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `CreateQueue` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_CreateQueue.html) |
| `DeleteQueue` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteQueue.html) |
| `GetQueueUrl` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_GetQueueUrl.html) |
| `ListQueues` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ListQueues.html) |
| `ListQueueTags` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ListQueueTags.html) |
| `TagQueue` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_TagQueue.html) |
| `UntagQueue` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_UntagQueue.html) |
| `GetQueueAttributes` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_GetQueueAttributes.html) |
| `SetQueueAttributes` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SetQueueAttributes.html) |
| `PurgeQueue` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_PurgeQueue.html) |

### Message operations

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `SendMessage` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessage.html) |
| `SendMessageBatch` | ❌ Unsupported | Up to 10 messages per batch | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessageBatch.html) |
| `ReceiveMessage` | ❌ Unsupported | Long-polling (`WaitTimeSeconds`) requires async timer support | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ReceiveMessage.html) |
| `DeleteMessage` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteMessage.html) |
| `DeleteMessageBatch` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteMessageBatch.html) |
| `ChangeMessageVisibility` | ❌ Unsupported | Requires visibility-timeout ticker | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibility.html) |
| `ChangeMessageVisibilityBatch` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibilityBatch.html) |

### Permissions

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `AddPermission` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_AddPermission.html) |
| `RemovePermission` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_RemovePermission.html) |

### Dead-letter queues

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `ListDeadLetterSourceQueues` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ListDeadLetterSourceQueues.html) |
| DLQ redrive (attribute on CreateQueue) | ❌ Unsupported | Set via `RedrivePolicy` queue attribute | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html) |

### FIFO queues

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| FIFO queue creation (`QueueName.fifo`) | ❌ Unsupported | Requires deduplication ID tracking | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/FIFO-queues.html) |
| Message deduplication (`MessageDeduplicationId`) | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/FIFO-queues-exactly-once-processing.html) |
| Message group ordering (`MessageGroupId`) | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/FIFO-queues-message-order.html) |

---

## Known limitations

- Visibility timeout clocks are wall-clock based. They may drift slightly under
  high load in the in-memory backend.
- Message attribute data types `Binary` and `Number` are stored but not validated.
- SQS → Lambda event source mapping requires the Lambda service; see `lambda.md`.
