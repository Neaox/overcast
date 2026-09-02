---
title: "SNS limitations"
description: "Every way Overcast's SNS diverges from AWS: subscription confirmation, FIFO topics, delivery failure semantics, and what CloudFormation does with a topic."
section: "Service Reference"
tags:
  - docs
  - limitations
  - services
  - sns
---

# SNS limitations

Divergences from AWS, in full. The summary is on the
[SNS](../sns.md).

## Subscriptions

| Behaviour             | On AWS                                                       | Here                                                     |
| --------------------- | ------------------------------------------------------------ | ---------------------------------------------------------- |
| `ConfirmSubscription` | A token is emailed or POSTed and must be echoed back          | Every subscription is confirmed on creation; any token is accepted |
| `application` (mobile push) | Requires a platform application and device endpoint     | `Subscribe` returns `400 InvalidParameter`                 |
| `firehose`            | Streams to a Kinesis Data Firehose delivery stream            | `Subscribe` returns `400 InvalidParameter`                 |
| `http` / `https`      | The notification is POSTed to the endpoint                    | Captured in the Inbox as a webhook delivery; nothing is dialled |
| `sms`                 | A real message is sent                                        | Captured in the Inbox; the endpoint must be E.164, e.g. `+12125551234` |
| `TargetArn` on `Publish` | Publishes to a platform endpoint                           | `400 InvalidParameter` — there is no `CreatePlatformEndpoint`, so no `TargetArn` is ever real |

## FIFO topics

A topic named `*.fifo` round-trips its FIFO attributes, and `Publish` enforces
the FIFO-only parameters against it: `MessageGroupId` is required, and so is
`MessageDeduplicationId` unless the topic has `ContentBasedDeduplication` set.
FIFO-ness is read off the `.fifo` suffix, the convention AWS enforces at
`CreateTopic`.

Ordering, deduplication and `SequenceNumber` generation are **not**
implemented — messages fan out exactly as they do on a standard topic. See
[#183](https://github.com/overcast-sh/overcast/issues/183).

## Delivery to Lambda

A `lambda` subscription invokes the function asynchronously with AWS's own
event shape: `Records[0].EventSource` is `aws:sns` and the notification sits
under `Records[0].Sns`. `RawMessageDelivery` has no effect, matching AWS.

"Delivered" means Lambda *accepted* the event, exactly as an
`InvocationType=Event` invoke returns `202` before the handler runs. A
throttled function — including one reserved to zero concurrency — is retried
inside Lambda and is not a delivery failure. Whether the handler then
succeeded is reported against the function, not the subscription.

These are delivery failures: the function does not exist, is not in an
invokable state, is missing a layer version, or has a runtime this emulator
cannot execute.

## What happens to a failed delivery

Nothing is silently discarded. A failed delivery is logged, published on the
event stream as `sns:DeliveryFailed`, counted as `NumberOfNotificationsFailed`,
and — when the subscription's `RedrivePolicy` names a `deadLetterTargetArn` —
written to that SQS queue.

A protocol whose dependency was never wired into the running instance fails the
same way, with a one-time warning per topic and protocol saying what is
missing. A notification that vanishes without a trace is the failure mode this
exists to prevent.

## CloudFormation

- `AWS::SNS::Topic` attributes are applied through SNS on both create and
  update. A topic's inline `Subscription` list creates real SNS subscriptions
  during creation.
- Updating that inline list, or removing an SNS attribute that was previously
  configured, **fails the stack update** rather than leaving stale
  configuration behind.
- Standalone `AWS::SNS::Subscription` attributes are applied through SNS after
  subscribing.
- Cross-region `AWS::SNS::Subscription` `Region` is not implemented, and fails
  the stack rather than being quietly ignored.
