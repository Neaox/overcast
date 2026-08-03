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
- `email` and `email-json` subscriptions are captured in the Inbox (`/_overcast/inbox`),
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
- A delivery that fails is not silently discarded. It is logged, published on the
  event stream as `sns:DeliveryFailed`, and — when the subscription's
  `RedrivePolicy` names a `deadLetterTargetArn` — written to that SQS queue.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category                            | ✅ Supported | ❌ Unsupported |
| ----------------------------------- | ------------ | -------------- |
| Topics                              | 5            |                |
| Subscriptions                       | 7            |                |
| Publishing                          | 5            |                |
| Platform applications (mobile push) |              | 5              |
| SMS                                 | 1            | 3              |

---

## Endpoints

### Topics

| Operation            | Status       | Notes                         | AWS Docs                                                                       |
| -------------------- | ------------ | ----------------------------- | ------------------------------------------------------------------------------ |
| `CreateTopic`        | ✅ Supported | Idempotent; attributes stored | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CreateTopic.html)        |
| `DeleteTopic`        | ✅ Supported |                               | [docs](https://docs.aws.amazon.com/sns/latest/api/API_DeleteTopic.html)        |
| `GetTopicAttributes` | ✅ Supported |                               | [docs](https://docs.aws.amazon.com/sns/latest/api/API_GetTopicAttributes.html) |
| `SetTopicAttributes` | ✅ Supported |                               | [docs](https://docs.aws.amazon.com/sns/latest/api/API_SetTopicAttributes.html) |
| `ListTopics`         | ✅ Supported |                               | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListTopics.html)         |

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

| Operation                                        | Status       | Notes                                                                                                                                                  | AWS Docs                                                                        |
| ------------------------------------------------ | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------- |
| `Publish`                                        | ✅ Supported | Async fan-out to `sqs`, `lambda`, `email`, `email-json`, `http`, `https`, and `sms` subscribers                                                        | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html)             |
| `PublishBatch`                                   | ✅ Supported | Up to 10 messages                                                                                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_PublishBatch.html)        |
| `Message filtering (subscription filter policy)` | ✅ Supported | String/Number attribute value matching via FilterPolicy on SetSubscriptionAttributes                                                                   | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-message-filtering.html)    |
| `Lambda subscription delivery`                   | ✅ Supported | Publish invokes the subscribed function asynchronously with the AWS `Records[].Sns` event; RawMessageDelivery does not apply to `lambda`, matching AWS | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-lambda-as-subscriber.html) |
| `Subscription dead-letter queue (RedrivePolicy)` | ✅ Supported | A delivery that fails is redirected to the SQS queue named by the subscription's RedrivePolicy                                                         | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-dead-letter-queues.html)   |

### Platform applications (mobile push)

| Operation                   | Status         | Notes                      | AWS Docs                                                                              |
| --------------------------- | -------------- | -------------------------- | ------------------------------------------------------------------------------------- |
| `CreatePlatformApplication` | ❌ Unsupported | APNs, GCM/FCM, ADM         | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CreatePlatformApplication.html) |
| `DeletePlatformApplication` | ❌ Unsupported |                            | [docs](https://docs.aws.amazon.com/sns/latest/api/API_DeletePlatformApplication.html) |
| `ListPlatformApplications`  | ❌ Unsupported |                            | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListPlatformApplications.html)  |
| `CreatePlatformEndpoint`    | ❌ Unsupported | Device registration        | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CreatePlatformEndpoint.html)    |
| `PublishToEndpoint`         | ❌ Unsupported | via Publish with TargetArn | [docs](https://docs.aws.amazon.com/sns/latest/dg/mobile-push-send-directmessage.html) |

### SMS

| Operation                      | Status         | Notes                                                                | AWS Docs                                                                                     |
| ------------------------------ | -------------- | -------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `SMS publish`                  | ✅ Supported   | via sms subscription on Publish; captured in the Inbox with kind=sms | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-mobile-phone-number-as-subscriber.html) |
| `CheckIfPhoneNumberIsOptedOut` | ❌ Unsupported |                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CheckIfPhoneNumberIsOptedOut.html)     |
| `ListPhoneNumbersOptedOut`     | ❌ Unsupported |                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListPhoneNumbersOptedOut.html)         |
| `OptInPhoneNumber`             | ❌ Unsupported |                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_OptInPhoneNumber.html)                 |

<!-- END overcast:capabilities -->
