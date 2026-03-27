# SNS — Simple Notification Service

> AWS docs: https://docs.aws.amazon.com/sns/latest/api/welcome.html

SNS uses a query-string or JSON API. Topics are identified by ARN:
`arn:aws:sns:us-east-1:000000000000:<topic-name>`

Subscription delivery is handled synchronously in the emulator (i.e. the
message is delivered to the subscriber before `Publish` returns). This differs
from real SNS which delivers asynchronously, but simplifies test assertions.

---

## Summary

| Category | ✅ Supported | ⚠️ Partial | 🚧 WIP | ❌ Unsupported |
|----------|------------|-----------|--------|--------------|
| Topics | 0 | 0 | 0 | 5 |
| Subscriptions | 0 | 0 | 0 | 6 |
| Publishing | 0 | 0 | 0 | 3 |
| Platform applications (mobile push) | 0 | 0 | 0 | 5 |
| SMS | 0 | 0 | 0 | 4 |

---

## Endpoints

### Topics

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `CreateTopic` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CreateTopic.html) |
| `DeleteTopic` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_DeleteTopic.html) |
| `GetTopicAttributes` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_GetTopicAttributes.html) |
| `SetTopicAttributes` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_SetTopicAttributes.html) |
| `ListTopics` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListTopics.html) |

### Subscriptions

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `Subscribe` | ❌ Unsupported | Protocols: `sqs`, `lambda`, `http`, `https`, `email` | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Subscribe.html) |
| `ConfirmSubscription` | ❌ Unsupported | Token-based confirmation flow | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ConfirmSubscription.html) |
| `Unsubscribe` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Unsubscribe.html) |
| `ListSubscriptions` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListSubscriptions.html) |
| `ListSubscriptionsByTopic` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListSubscriptionsByTopic.html) |
| `GetSubscriptionAttributes` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_GetSubscriptionAttributes.html) |
| `SetSubscriptionAttributes` | ❌ Unsupported | Includes filter policies | [docs](https://docs.aws.amazon.com/sns/latest/api/API_SetSubscriptionAttributes.html) |

### Publishing

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `Publish` | ❌ Unsupported | Fan-out to subscriptions | [docs](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html) |
| `PublishBatch` | ❌ Unsupported | Up to 10 messages | [docs](https://docs.aws.amazon.com/sns/latest/api/API_PublishBatch.html) |
| Message filtering (subscription filter policy) | ❌ Unsupported | Attribute-based routing | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-message-filtering.html) |

### Platform applications (mobile push)

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `CreatePlatformApplication` | ❌ Unsupported | APNs, GCM/FCM, ADM | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CreatePlatformApplication.html) |
| `DeletePlatformApplication` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_DeletePlatformApplication.html) |
| `ListPlatformApplications` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListPlatformApplications.html) |
| `CreatePlatformEndpoint` | ❌ Unsupported | Device registration | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CreatePlatformEndpoint.html) |
| `PublishToEndpoint` (via `Publish` with `TargetArn`) | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/dg/mobile-push-send-directmessage.html) |

### SMS

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| SMS publish (via `Publish` with `PhoneNumber`) | ❌ Unsupported | No actual SMS delivery | [docs](https://docs.aws.amazon.com/sns/latest/dg/sns-mobile-phone-number-as-subscriber.html) |
| `CheckIfPhoneNumberIsOptedOut` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_CheckIfPhoneNumberIsOptedOut.html) |
| `ListPhoneNumbersOptedOut` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_ListPhoneNumbersOptedOut.html) |
| `OptInPhoneNumber` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/sns/latest/api/API_OptInPhoneNumber.html) |

---

## Known limitations

- Subscription confirmation (`ConfirmSubscription` token flow) is simplified:
  the emulator auto-confirms `sqs` and `lambda` subscriptions without requiring
  a token round-trip.
- HTTP/HTTPS subscriptions require a reachable URL inside the Docker network.
- Email subscriptions are accepted but no email is sent. Use an HTTP subscription
  pointed at a local mailcatcher for email testing.
- Mobile push and SMS are entirely out of scope for v1 — endpoints exist and
  return `501` with a clear message.
