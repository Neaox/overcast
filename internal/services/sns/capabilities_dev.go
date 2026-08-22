//go:build dev

package sns

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Topics
		capabilities.Capability{Service: "sns", Operation: "CreateTopic", Category: "Topics", Status: capabilities.StatusSupported, Notes: "Idempotent; attributes stored; inline `Tags` applied at creation and left untouched by a repeat call"},
		capabilities.Capability{Service: "sns", Operation: "DeleteTopic", Category: "Topics", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "GetTopicAttributes", Category: "Topics", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "SetTopicAttributes", Category: "Topics", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "ListTopics", Category: "Topics", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "TagResource", Category: "Topics", Status: capabilities.StatusSupported, Notes: "Tags are stored on the topic; member-indexed form encoding"},
		capabilities.Capability{Service: "sns", Operation: "UntagResource", Category: "Topics", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "ListTagsForResource", Category: "Topics", Status: capabilities.StatusSupported},
		// Subscriptions
		capabilities.Capability{Service: "sns", Operation: "Subscribe", Category: "Subscriptions", Status: capabilities.StatusSupported, Notes: "Protocols: `sqs`, `sms`, `email`, `email-json`, `http`, `https`, `lambda`. `application` and `firehose` return 400."},
		capabilities.Capability{Service: "sns", Operation: "ConfirmSubscription", Category: "Subscriptions", Status: capabilities.StatusSupported, Notes: "Emulator auto-confirms; any token accepted"},
		capabilities.Capability{Service: "sns", Operation: "Unsubscribe", Category: "Subscriptions", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "ListSubscriptions", Category: "Subscriptions", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "ListSubscriptionsByTopic", Category: "Subscriptions", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "GetSubscriptionAttributes", Category: "Subscriptions", Status: capabilities.StatusSupported, Notes: "Returns SubscriptionArn, TopicArn, Protocol, Endpoint, Owner + custom attributes"},
		capabilities.Capability{Service: "sns", Operation: "SetSubscriptionAttributes", Category: "Subscriptions", Status: capabilities.StatusSupported, Notes: "Stores any attribute; FilterPolicy drives message filtering, RedrivePolicy the subscription dead-letter queue"},
		// Publishing
		capabilities.Capability{Service: "sns", Operation: "Publish", Category: "Publishing", Status: capabilities.StatusSupported, Notes: "TopicArn/PhoneNumber/TargetArn destination selection (exactly one required); MessageStructure=json per-protocol payload selection; Message (256 KB) and Subject (<100 chars, no control/line-break chars) validation; async fan-out to `sqs`, `lambda`, `email`, `email-json`, `http`, `https`, and `sms` subscribers; records AWS/SNS CloudWatch metrics NumberOfMessagesPublished and PublishSize on accept, and NumberOfNotificationsDelivered/NumberOfNotificationsFailed per subscription delivery attempt (service-metrics-platform.md phase 2) — a delivery whose protocol dependency was never wired into this instance (nil enqueuer/mailer/smsSender/outbound) is treated as a delivery failure like any other protocol's: failDelivery runs (recording NumberOfNotificationsFailed, redirecting to the subscription's DLQ when configured), with a one-time Warn per (topic, protocol) telling the operator what to wire (#1306)"},
		capabilities.Capability{Service: "sns", Operation: "PublishBatch", Category: "Publishing", Status: capabilities.StatusSupported, Notes: "Up to 10 messages; each entry independently validated (Message/Subject/MessageStructure=json), a bad entry fails without aborting the batch; records NumberOfMessagesPublished/PublishSize per successful entry"},
		capabilities.Capability{Service: "sns", Operation: "Message filtering (subscription filter policy)", Category: "Publishing", Status: capabilities.StatusSupported, Notes: "String/Number attribute value matching via FilterPolicy on SetSubscriptionAttributes", DocsURL: "[docs](https://docs.aws.amazon.com/sns/latest/dg/sns-message-filtering.html)", DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "Lambda subscription delivery", Category: "Publishing", Status: capabilities.StatusSupported, Notes: "Publish invokes the subscribed function asynchronously with the AWS `Records[].Sns` event; RawMessageDelivery does not apply to `lambda`, matching AWS", DocsURL: "[docs](https://docs.aws.amazon.com/sns/latest/dg/sns-lambda-as-subscriber.html)", DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "Subscription dead-letter queue (RedrivePolicy)", Category: "Publishing", Status: capabilities.StatusSupported, Notes: "A delivery that fails is redirected to the SQS queue named by the subscription's RedrivePolicy", DocsURL: "[docs](https://docs.aws.amazon.com/sns/latest/dg/sns-dead-letter-queues.html)", DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "FIFO topic Publish parameters", Category: "Publishing", Status: capabilities.StatusPartial, Notes: "MessageGroupId/MessageDeduplicationId are required and validated for a topic named `*.fifo` (or ContentBasedDeduplication substitutes for the dedup ID); actual FIFO ordering, deduplication, and SequenceNumber generation remain unimplemented", DocsURL: "[docs](https://docs.aws.amazon.com/sns/latest/api/API_Publish.html)", DocOnly: true},
		// Platform applications (mobile push)
		capabilities.Capability{Service: "sns", Operation: "CreatePlatformApplication", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, Notes: "APNs, GCM/FCM, ADM", DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "DeletePlatformApplication", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "ListPlatformApplications", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "CreatePlatformEndpoint", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, Notes: "Device registration", DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "PublishToEndpoint", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, Notes: "via Publish with TargetArn; returns 400 InvalidParameter explicitly rather than a generic missing-TopicArn error", DocsURL: "[docs](https://docs.aws.amazon.com/sns/latest/dg/mobile-push-send-directmessage.html)", DocOnly: true},
		// SMS
		capabilities.Capability{Service: "sns", Operation: "SMS publish", Category: "SMS", Status: capabilities.StatusSupported, Notes: "via sms subscription on Publish, or directly via Publish with PhoneNumber (no topic involved); captured in the Inbox with kind=sms", DocsURL: "[docs](https://docs.aws.amazon.com/sns/latest/dg/sns-mobile-phone-number-as-subscriber.html)", DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "CheckIfPhoneNumberIsOptedOut", Category: "SMS", Status: capabilities.StatusUnsupported, DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "ListPhoneNumbersOptedOut", Category: "SMS", Status: capabilities.StatusUnsupported, DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "OptInPhoneNumber", Category: "SMS", Status: capabilities.StatusUnsupported, DocOnly: true},
	)
}
