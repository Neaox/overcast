---
title: "SQS — Simple Queue Service"
description: "Quick start, standard and FIFO coverage with visibility timeouts and redrive, why an SDK resolves the endpoint from the queue URL, and the permissions API that is missing."
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

Standard and FIFO queues with long polling, visibility timeouts and
dead-letter redrive. Queue URLs are minted on whichever origin you called.

**Status:** ✅ Supported

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

Q=$(aws sqs create-queue --queue-name orders --query QueueUrl --output text)
aws sqs send-message --queue-url "$Q" --message-body '{"id":1}'
aws sqs receive-message --queue-url "$Q" --wait-time-seconds 5
```

## What works

| Area               | Behaviour                                                                                                                   |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------- |
| Queues             | Idempotent `CreateQueue`, FIFO queues via the `.fifo` suffix, inline tags, all standard attributes                            |
| Sending            | `SendMessage` and `SendMessageBatch` (10 per call) with `DelaySeconds` and message attributes                                 |
| Receiving          | `MaxNumberOfMessages`, per-call and queue-default long polling, per-call `VisibilityTimeout`, FIFO `ReceiveRequestAttemptId`  |
| Visibility         | `ChangeMessageVisibility` and its batch form on in-flight messages                                                           |
| Dead-letter queues | `RedrivePolicy`, `ListDeadLetterSourceQueues`, and `StartMessageMoveTask` to redrive back to the source                       |
| Metrics            | `AWS/SQS` CloudWatch metrics, including depth gauges sampled every minute whether or not the queue has traffic                |

## Queue URLs and endpoint resolution

AWS SDKs resolve the SQS endpoint from the `QueueUrl`, not from the endpoint
you configured. The JS v3 client's `queueUrlMiddleware` replaces the resolved
endpoint with the queue URL's origin whenever the two differ, and .NET and
Java v1 use the queue URL as the request URI outright.

> [!IMPORTANT]
> `AWS_ENDPOINT_URL` does not protect against this. The middleware checks the
> client's `endpoint` config field, and the environment variable never reaches
> it. Only an endpoint passed explicitly to the client suppresses the override:
>
> ```js
> new SQSClient({ endpoint: process.env.AWS_ENDPOINT_URL });
> new SQSClient({ useQueueUrlAsEndpoint: false });
> ```

So a queue URL is only usable by a caller that can dial its origin. Overcast
mints them **per request**, on the origin the caller reached it on: a host CLI
hitting `localhost:4566` gets `localhost:4566` URLs, and a Lambda container
calling in on Overcast's container address gets that address.
`OVERCAST_HOSTNAME` is the fallback for callers with no usable origin, not an
override.

Queue URLs minted elsewhere are always accepted — only the queue name is read
out of the URL. For a URL that crosses the boundary out of band, such as a CDK
deploy baking `queue.queueUrl` into a function's environment, see
[Lambda: reaching Overcast from function code](./lambda.md#reaching-overcast-from-function-code).

## Differences from AWS

| Area                                 | On AWS                                        | Overcast                                                |
| ------------------------------------ | --------------------------------------------- | ------------------------------------------------------- |
| `AddPermission` / `RemovePermission` | Manage a queue's access policy                | Not implemented — `501 Not Implemented`                 |
| `redrivePermission`                  | Restricts which queues may redrive from a DLQ | Accepted, validated and round-tripped, but not enforced |
| Message attribute types              | `Binary` and `Number` values are validated    | Stored as given; the `DataType` is not checked          |
| Queue URL host                       | A fixed regional endpoint                     | The origin of the request that minted it                |

<!-- BEGIN overcast:capabilities -->

## Operations

19 of 21 listed operations are implemented.
Per-operation status, notes and AWS API links: [SQS operations](sqs/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Lambda](./lambda.md) — event source mappings poll these queues
- [SNS](./sns.md) — `sqs` subscriptions deliver into them
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [What host and port a URL carries](../networking/urls.md) — the rule behind the minted origin
- [AWS API reference](https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/Welcome.html)
