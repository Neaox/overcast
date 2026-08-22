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

> AWS docs: https://docs.aws.amazon.com/sns/latest/api/welcome.html

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

## Summary

| Category                            | ✅ Supported | ⚠️ Partial | ❌ Unsupported |
| ----------------------------------- | ------------ | ---------- | -------------- |
| Topics                              | 8            |            |                |
| Subscriptions                       | 7            |            |                |
| Publishing                          | 5            | 1          |                |
| Platform applications (mobile push) |              |            | 5              |
| SMS                                 | 1            |            | 3              |

---

## Endpoints

### Topics

| Operation             | Status       | Notes                                                                                                | AWS Docs                                                                        |
| --------------------- | ------------ | ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| `CreateTopic`         | ✅ Supported | Idempotent; attributes stored; inline `Tags` applied at creation and left untouched by a repeat call | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CreateTopic.html)         |
| `DeleteTopic`         | ✅ Supported |                                                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_DeleteTopic.html)         |
| `GetTopicAttributes`  | ✅ Supported |                                                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_GetTopicAttributes.html)  |
| `SetTopicAttributes`  | ✅ Supported |                                                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_SetTopicAttributes.html)  |
| `ListTopics`          | ✅ Supported |                                                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListTopics.html)          |
| `TagResource`         | ✅ Supported | Tags are stored on the topic; member-indexed form encoding                                           | [docs](https://docs.aws.amazon.com/sns/latest/api/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported |                                                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported |                                                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListTagsForResource.html) |

### Subscriptions

| Operation                   | Status       | Notes                                                                                                               | AWS Docs                                                                              |
| --------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `Subscribe`                 | ✅ Supported | Protocols: `sqs`, `sms`, `email`, `email-json`, `http`, `https`, `lambda`. `application` and `firehose` return 400. | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Subscribe.html)                 |
| `ConfirmSubscription`       | ✅ Supported | Emulator auto-confirms; any token accepted                                                                          | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ConfirmSubscription.html)       |
| `Unsubscribe`               | ✅ Supported |                                                                                                                     | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Unsubscribe.html)               |
| `ListSubscriptions`         | ✅ Supported |                                                                                                                     | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListSubscriptions.html)         |
| `ListSubscriptionsByTopic`  | ✅ Supported |                                                                                                                     | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListSubscriptionsByTopic.html)  |
| `GetSubscriptionAttributes` | ✅ Supported | Returns SubscriptionArn, TopicArn, Protocol, Endpoint, Owner + custom attributes                                    | [docs](https://docs.aws.amazon.com/sns/latest/api/API_GetSubscriptionAttributes.html) |
| `SetSubscriptionAttributes` | ✅ Supported | Stores any attribute; FilterPolicy drives message filtering, RedrivePolicy the subscription dead-letter queue       | [docs](https://docs.aws.amazon.com/sns/latest/api/API_SetSubscriptionAttributes.html) |

### Publishing

| Operation                                        | Status       | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | AWS Docs                                                                        |
| ------------------------------------------------ | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------- |
| `Publish`                                        | ✅ Supported | TopicArn/PhoneNumber/TargetArn destination selection (exactly one required); MessageStructure=json per-protocol payload selection; Message (256 KB) and Subject (<100 chars, no control/line-break chars) validation; async fan-out to `sqs`, `lambda`, `email`, `email-json`, `http`, `https`, and `sms` subscribers; records AWS/SNS CloudWatch metrics NumberOfMessagesPublished and PublishSize on accept, and NumberOfNotificationsDelivered/NumberOfNotificationsFailed per subscription delivery attempt (service-metrics-platform.md phase 2) — a delivery whose protocol dependency was never wired (nil enqueuer/mailer/etc.) records neither, since there is no delivery outcome to observe | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html)             |
| `PublishBatch`                                   | ✅ Supported | Up to 10 messages; each entry independently validated (Message/Subject/MessageStructure=json), a bad entry fails without aborting the batch; records NumberOfMessagesPublished/PublishSize per successful entry                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/sns/latest/api/API_PublishBatch.html)        |
| `Message filtering (subscription filter policy)` | ✅ Supported | String/Number attribute value matching via FilterPolicy on SetSubscriptionAttributes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-message-filtering.html)    |
| `Lambda subscription delivery`                   | ✅ Supported | Publish invokes the subscribed function asynchronously with the AWS `Records[].Sns` event; RawMessageDelivery does not apply to `lambda`, matching AWS                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-lambda-as-subscriber.html) |
| `Subscription dead-letter queue (RedrivePolicy)` | ✅ Supported | A delivery that fails is redirected to the SQS queue named by the subscription's RedrivePolicy                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-dead-letter-queues.html)   |
| `FIFO topic Publish parameters`                  | ⚠️ Partial   | MessageGroupId/MessageDeduplicationId are required and validated for a topic named `*.fifo` (or ContentBasedDeduplication substitutes for the dedup ID); actual FIFO ordering, deduplication, and SequenceNumber generation remain unimplemented                                                                                                                                                                                                                                                                                                                                                                                                                                                       | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html)             |

### Platform applications (mobile push)

| Operation                   | Status         | Notes                                                                                                            | AWS Docs                                                                              |
| --------------------------- | -------------- | ---------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| `CreatePlatformApplication` | ❌ Unsupported | APNs, GCM/FCM, ADM                                                                                               | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CreatePlatformApplication.html) |
| `DeletePlatformApplication` | ❌ Unsupported |                                                                                                                  | [docs](https://docs.aws.amazon.com/sns/latest/api/API_DeletePlatformApplication.html) |
| `ListPlatformApplications`  | ❌ Unsupported |                                                                                                                  | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListPlatformApplications.html)  |
| `CreatePlatformEndpoint`    | ❌ Unsupported | Device registration                                                                                              | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CreatePlatformEndpoint.html)    |
| `PublishToEndpoint`         | ❌ Unsupported | via Publish with TargetArn; returns 400 InvalidParameter explicitly rather than a generic missing-TopicArn error | [docs](https://docs.aws.amazon.com/sns/latest/dg/mobile-push-send-directmessage.html) |

### SMS

| Operation                      | Status         | Notes                                                                                                                              | AWS Docs                                                                                     |
| ------------------------------ | -------------- | ---------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `SMS publish`                  | ✅ Supported   | via sms subscription on Publish, or directly via Publish with PhoneNumber (no topic involved); captured in the Inbox with kind=sms | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-mobile-phone-number-as-subscriber.html) |
| `CheckIfPhoneNumberIsOptedOut` | ❌ Unsupported |                                                                                                                                    | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CheckIfPhoneNumberIsOptedOut.html)     |
| `ListPhoneNumbersOptedOut`     | ❌ Unsupported |                                                                                                                                    | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListPhoneNumbersOptedOut.html)         |
| `OptInPhoneNumber`             | ❌ Unsupported |                                                                                                                                    | [docs](https://docs.aws.amazon.com/sns/latest/api/API_OptInPhoneNumber.html)                 |

<!-- END overcast:capabilities -->
