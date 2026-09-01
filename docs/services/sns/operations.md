---
title: "SNS operations"
description: "Every SNS operation Overcast declares — 22 of 30 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - services
  - sns
---

<!-- BEGIN overcast:capabilities -->

# SNS operations

22 of 30 listed operations are implemented. Back to [SNS](../sns.md).

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

| Operation                                        | Status       | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 | AWS Docs                                                                        |
| ------------------------------------------------ | ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| `Publish`                                        | ✅ Supported | TopicArn/PhoneNumber/TargetArn destination selection (exactly one required); MessageStructure=json per-protocol payload selection; Message (256 KB) and Subject (<100 chars, no control/line-break chars) validation; async fan-out to `sqs`, `lambda`, `email`, `email-json`, `http`, `https`, and `sms` subscribers; records AWS/SNS CloudWatch metrics NumberOfMessagesPublished and PublishSize on accept, and NumberOfNotificationsDelivered/NumberOfNotificationsFailed per subscription delivery attempt — a delivery whose protocol dependency was never wired into this instance (nil enqueuer/mailer/smsSender/outbound) is treated as a delivery failure like any other protocol's: failDelivery runs (recording NumberOfNotificationsFailed, redirecting to the subscription's DLQ when configured), with a one-time Warn per (topic, protocol) telling the operator what to wire (#1306) | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html)             |
| `PublishBatch`                                   | ✅ Supported | Up to 10 messages; each entry independently validated (Message/Subject/MessageStructure=json), a bad entry fails without aborting the batch; records NumberOfMessagesPublished/PublishSize per successful entry                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       | [docs](https://docs.aws.amazon.com/sns/latest/api/API_PublishBatch.html)        |
| `Message filtering (subscription filter policy)` | ✅ Supported | String/Number attribute value matching via FilterPolicy on SetSubscriptionAttributes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-message-filtering.html)    |
| `Lambda subscription delivery`                   | ✅ Supported | Publish invokes the subscribed function asynchronously with the AWS `Records[].Sns` event; RawMessageDelivery does not apply to `lambda`, matching AWS                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-lambda-as-subscriber.html) |
| `Subscription dead-letter queue (RedrivePolicy)` | ✅ Supported | A delivery that fails is redirected to the SQS queue named by the subscription's RedrivePolicy                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-dead-letter-queues.html)   |
| `FIFO topic Publish parameters`                  | ⚠️ Partial   | MessageGroupId/MessageDeduplicationId are required and validated for a topic named `*.fifo` (or ContentBasedDeduplication substitutes for the dedup ID); actual FIFO ordering, deduplication, and SequenceNumber generation remain unimplemented                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html)             |

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
