---
title: "SQS — Simple Queue Service"
description: "SQS supports AWS JSON 1.0, AWS Query, and Smithy RPC v2 CBOR. JSON and Query requests share the root endpoint; the action is identified by the Action query parameter or the..."
section: "Service Reference"
tags:
  - docs
  - queue
  - service
  - services
  - simple
  - sqs
---

# SQS — Simple Queue Service

> AWS docs: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/Welcome.html

SQS supports AWS JSON 1.0, AWS Query, and Smithy RPC v2 CBOR. JSON and Query
requests share the root endpoint; the action is identified by the `Action`
query parameter or the `X-Amz-Target` header in SDK requests. RPC v2 CBOR
requests use `/service/AmazonSQS/operation/<Operation>` with
`Smithy-Protocol: rpc-v2-cbor`.

Queue URLs are returned in the form `http://localhost:4566/<account-id>/<queue-name>`.
For local use, `<account-id>` defaults to `000000000000`.

## Queue URLs and endpoint resolution

AWS SDKs resolve the SQS endpoint from the `QueueUrl`, not from the endpoint you
configured. The JS v3 client's `queueUrlMiddleware` replaces the resolved
endpoint with the queue URL's origin whenever the two differ, and .NET and Java
v1 use the queue URL as the request URI outright — a leftover from the Query
protocol, where a queue was addressed by its URL.

**`AWS_ENDPOINT_URL` does not protect against this.** It is resolved through the
endpoint ruleset's `Endpoint` parameter and never becomes the client's
`endpoint` config field, which is what the middleware checks. Only an endpoint
passed explicitly to the client suppresses the override:

```js
// Both of these keep the client pinned to Overcast:
new SQSClient({ endpoint: process.env.AWS_ENDPOINT_URL });
new SQSClient({ useQueueUrlAsEndpoint: false });
```

The practical consequence is that a queue URL is only usable by a caller that
can dial its origin. Overcast therefore mints queue URLs **per request**, on the
origin the caller reached it on: a host CLI hitting `localhost:4566` gets
`localhost:4566` URLs, and a Lambda container calling in on Overcast's container
address gets that address. `OVERCAST_HOSTNAME` is the fallback for callers with
no usable origin, not an override.

Queue URLs minted elsewhere are always accepted — only the queue name is read
from the URL, so a queue created on one origin can be addressed from another.

For URLs that cross the boundary out-of-band — a CDK deploy run on the host
baking `queue.queueUrl` into a function's environment — see
[Lambda: reaching Overcast from function code](lambda.md#reaching-overcast-from-function-code).

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

| Operation                      | Status       | Notes                                                                                                                                                                                                                                                     | AWS Docs                                                                                                            |
| ------------------------------ | ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `SendMessage`                  | ✅ Supported | DelaySeconds, MessageAttributes supported; records AWS/SQS CloudWatch metrics NumberOfMessagesSent and SentMessageSize, skipped for a FIFO content-based-deduplication resend since no new message is enqueued                                            | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessage.html)                  |
| `SendMessageBatch`             | ✅ Supported | Up to 10 messages per batch; records NumberOfMessagesSent/SentMessageSize per successful entry                                                                                                                                                            | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessageBatch.html)             |
| `ReceiveMessage`               | ✅ Supported | MaxNumberOfMessages, VisibilityTimeout, WaitTimeSeconds, queue default long polling, FIFO ReceiveRequestAttemptId; records NumberOfMessagesReceived (non-empty) or NumberOfEmptyReceives (zero messages) once per call, after any long-poll retry settles | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ReceiveMessage.html)               |
| `DeleteMessage`                | ✅ Supported | Records NumberOfMessagesDeleted                                                                                                                                                                                                                           | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteMessage.html)                |
| `DeleteMessageBatch`           | ✅ Supported | Up to 10 messages per batch; records NumberOfMessagesDeleted per successful entry                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_DeleteMessageBatch.html)           |
| `ChangeMessageVisibility`      | ✅ Supported | Sets new visibility timeout on an in-flight message                                                                                                                                                                                                       | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibility.html)      |
| `ChangeMessageVisibilityBatch` | ✅ Supported | Batch visibility timeout changes; per-entry success/failure response                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibilityBatch.html) |

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
