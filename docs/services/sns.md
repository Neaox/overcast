---
title: "SNS — Simple Notification Service"
description: "Fan-out to SQS, Lambda, email, SMS and webhooks, with filter policies and subscription dead-letter queues. Subscriptions confirm themselves; FIFO ordering is not emulated."
section: "Service Reference"
tags:
  - docs
  - notification
  - service
  - services
  - simple
  - sns
---

# SNS — Simple Notification Service

Fan-out to SQS, Lambda, email, SMS and webhooks, with filter policies and
per-subscription dead-letter queues. Delivery is asynchronous, as on AWS.

**Status:** ⚠️ Partial

## Quick start

```sh
export AWS_ENDPOINT_URL=http://localhost:4566

TOPIC=$(aws sns create-topic --name orders --query TopicArn --output text)
QUEUE=$(aws sqs create-queue --queue-name orders-q --query QueueUrl --output text)
ARN=$(aws sqs get-queue-attributes --queue-url "$QUEUE" \
  --attribute-names QueueArn --query Attributes.QueueArn --output text)

aws sns subscribe --topic-arn "$TOPIC" --protocol sqs --notification-endpoint "$ARN"
aws sns publish --topic-arn "$TOPIC" --message '{"id":1}'
aws sqs receive-message --queue-url "$QUEUE"
```

## What works

| Area                  | Behaviour                                                                                    |
| --------------------- | ---------------------------------------------------------------------------------------------- |
| Protocols             | `sqs`, `lambda`, `email`, `email-json`, `sms`, `http`, `https`                                  |
| Publishing            | `Publish` and `PublishBatch` (10 per call), with `Message` and `Subject` validated as on AWS    |
| Destination selection | Exactly one of `TopicArn`, `PhoneNumber` or `TargetArn`, as on AWS                              |
| Per-protocol payloads | `MessageStructure=json` picks the entry keyed by each subscriber's protocol, falling back to `default` |
| Filter policies       | `FilterPolicy` on `SetSubscriptionAttributes`, matching string and number attributes            |
| Dead-letter queues    | A failed delivery goes to the SQS queue named by the subscription's `RedrivePolicy`             |
| Failure visibility    | A failed delivery is logged and published on the event stream as `sns:DeliveryFailed` — never silently dropped |
| Tags and attributes   | Topic tags, topic attributes, subscription attributes                                           |

Email, SMS and webhook deliveries all land in the console's
[Inbox](http://localhost:4567/inbox), threaded by publish. `Publish
--phone-number` delivers straight there with no topic involved.

## Differences from AWS

| Behaviour                | On AWS                                        | Here                                                          |
| ------------------------ | --------------------------------------------- | --------------------------------------------------------------- |
| `ConfirmSubscription`    | A token round-trip before delivery starts     | Auto-confirmed; any token is accepted                           |
| FIFO topics              | Ordered, deduplicated, with a `SequenceNumber` | Parameters are validated, but ordering and dedup are not emulated ([#183](https://github.com/overcast-sh/overcast/issues/183)) |
| Mobile push              | `application` protocol and platform endpoints  | `Subscribe` returns `400 InvalidParameter`; `Publish --target-arn` likewise |
| Kinesis Data Firehose    | `firehose` protocol                            | `Subscribe` returns `400 InvalidParameter`                      |
| `http`/`https` delivery  | POSTed to the endpoint                         | Captured in the Inbox; nothing is dialled                       |

The full list, including CloudFormation and Lambda delivery semantics, is in
[Limitations](sns/limitations.md).

## Gotchas

> [!WARNING]
> `RawMessageDelivery` never applies to a `lambda` subscription — the function
> always receives the full `Records[].Sns` event. That matches AWS, and it
> catches people who set the attribute and expect a bare body.

<!-- BEGIN overcast:capabilities -->

## Operations

22 of 30 listed operations are implemented.
Per-operation status, notes and AWS API links: [SNS operations](sns/operations.md).

<!-- END overcast:capabilities -->

## Related

- [SNS limitations](sns/limitations.md)
- [AWS API reference](https://docs.aws.amazon.com/sns/latest/api/welcome.html)
- [SQS](sqs.md) · [Lambda](lambda.md) · [SES](ses.md) — the delivery targets
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
