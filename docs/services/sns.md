---
title: "SNS — Simple Notification Service"
description: "SNS uses a query-string or JSON API. Topics are identified by ARN: arn:aws:sns:us-east-1:000000000000:\u003ctopic-name\u003e"
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

SNS uses a query-string or JSON API. Topics are identified by ARN:
`arn:aws:sns:us-east-1:000000000000:<topic-name>`

Subscription delivery is asynchronous — the HTTP response is returned before delivery
completes to subscribers, matching the behaviour of real SNS.

---

## Known limitations

- Subscription confirmation (`ConfirmSubscription` token flow) is simplified:
  the emulator auto-confirms all subscriptions without requiring a token round-trip.
- HTTP/HTTPS subscriptions require a reachable URL inside the Docker network.
- `email` and `email-json` subscriptions are captured in the Inbox (`/_overcast/ses/inbox`),
  viewable in the web UI.
- `sms` subscriptions are captured in the same Inbox with `kind=sms`, viewable
  in the web UI. No real SMS is sent. The endpoint must be a phone number in
  E.164 format (e.g. `+12125551234`).
- Mobile push (`application` protocol) and Kinesis Firehose (`firehose` protocol)
  are not supported and return `400 InvalidParameter` on `Subscribe`.
- `lambda` subscriptions invoke the function asynchronously with AWS's SNS event —
  `Records[0].EventSource` is `aws:sns` and the notification sits under
  `Records[0].Sns`. As on AWS, `RawMessageDelivery` has no effect on a `lambda`
  subscription: the function always receives the full event.
- Delivery to `lambda` means Lambda *accepted* the event, exactly as an
  `InvocationType=Event` invoke returns `202` before the handler runs. A function
  that is throttled — including one reserved to zero concurrency — is retried
  inside Lambda and is not a delivery failure, matching AWS. Whether the handler
  then succeeded is reported against the function, not the subscription.
- A delivery that fails is not silently discarded. It is logged, published on the
  event stream as `sns:DeliveryFailed`, and — when the subscription's
  `RedrivePolicy` names a `deadLetterTargetArn` — written to that SQS queue. For
  `lambda` that covers a function that does not exist, one that is not in an
  invokable state, a missing layer version, and a runtime the emulator cannot
  execute.
- CloudFormation applies `AWS::SNS::Topic` attributes through SNS on both create
  and update, and applies standalone `AWS::SNS::Subscription` attributes through
  SNS after subscribing. The topic's inline `Subscription` list creates the
  listed SNS subscriptions during creation. Updating that list or removing a
  previously configured SNS attribute fails the stack update rather than leaving
  stale SNS configuration. Cross-region `AWS::SNS::Subscription` `Region` is not
  implemented and fails the stack rather than being ignored. FIFO topic
  attributes round-trip, and `Publish` validates the FIFO-only
  `MessageGroupId`/`MessageDeduplicationId` parameters against a topic name
  ending in `.fifo`, but actual FIFO ordering, deduplication, and
  `SequenceNumber` generation remain unimplemented (#183).
- `Publish` accepts exactly one of `TopicArn`, `PhoneNumber`, or `TargetArn` as
  the destination, matching AWS. `PhoneNumber` delivers directly to the Inbox
  as `kind=sms` with no topic involved. `TargetArn` (publishing to a mobile
  platform endpoint) returns `400 InvalidParameter` — Overcast has no
  `CreatePlatformEndpoint`, so no `TargetArn` is ever a real endpoint.
- `Publish` and `PublishBatch` validate `Message` (≤ 256 KB, except SMS) and
  `Subject` (< 100 characters, no line breaks or control characters), and
  support `MessageStructure=json`: the `Message` value must be a JSON object
  with a string `default` key, and each subscriber receives the entry keyed by
  its own protocol name (falling back to `default`) — including under
  `RawMessageDelivery`, which strips the notification envelope but not the
  per-protocol selection underneath it.

<!-- BEGIN overcast:capabilities -->

## Operations

22 of 30 listed operations are implemented.
Per-operation status, notes and AWS API links: [SNS operations](sns/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/sns/latest/api/welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
