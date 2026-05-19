//go:build dev

package sns

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// Topics
		capabilities.Capability{Service: "sns", Operation: "CreateTopic", Category: "Topics", Status: capabilities.StatusSupported, Notes: "Idempotent; attributes stored"},
		capabilities.Capability{Service: "sns", Operation: "DeleteTopic", Category: "Topics", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "GetTopicAttributes", Category: "Topics", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "SetTopicAttributes", Category: "Topics", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "ListTopics", Category: "Topics", Status: capabilities.StatusSupported},
		// Subscriptions
		capabilities.Capability{Service: "sns", Operation: "Subscribe", Category: "Subscriptions", Status: capabilities.StatusSupported, Notes: "Protocols: `sqs`, `sms`, `email`, `email-json`, `http`, `https`, `lambda`. `application` and `firehose` return 400."},
		capabilities.Capability{Service: "sns", Operation: "ConfirmSubscription", Category: "Subscriptions", Status: capabilities.StatusSupported, Notes: "Emulator auto-confirms; any token accepted"},
		capabilities.Capability{Service: "sns", Operation: "Unsubscribe", Category: "Subscriptions", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "ListSubscriptions", Category: "Subscriptions", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "ListSubscriptionsByTopic", Category: "Subscriptions", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "sns", Operation: "GetSubscriptionAttributes", Category: "Subscriptions", Status: capabilities.StatusSupported, Notes: "Returns SubscriptionArn, TopicArn, Protocol, Endpoint, Owner + custom attributes"},
		capabilities.Capability{Service: "sns", Operation: "SetSubscriptionAttributes", Category: "Subscriptions", Status: capabilities.StatusSupported, Notes: "Stores any attribute; FilterPolicy used for message filtering"},
		// Publishing
		capabilities.Capability{Service: "sns", Operation: "Publish", Category: "Publishing", Status: capabilities.StatusSupported, Notes: "Async fan-out to `sqs`, `email`, `email-json`, and `sms` subscribers"},
		capabilities.Capability{Service: "sns", Operation: "PublishBatch", Category: "Publishing", Status: capabilities.StatusSupported, Notes: "Up to 10 messages"},
		capabilities.Capability{Service: "sns", Operation: "Message filtering (subscription filter policy)", Category: "Publishing", Status: capabilities.StatusSupported, Notes: "String/Number attribute value matching via FilterPolicy on SetSubscriptionAttributes", DocsURL: "[docs](https://docs.aws.amazon.com/sns/latest/dg/sns-message-filtering.html)", DocOnly: true},
		// Platform applications (mobile push)
		capabilities.Capability{Service: "sns", Operation: "CreatePlatformApplication", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, Notes: "APNs, GCM/FCM, ADM", DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "DeletePlatformApplication", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "ListPlatformApplications", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "CreatePlatformEndpoint", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, Notes: "Device registration", DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "PublishToEndpoint", Category: "Platform applications (mobile push)", Status: capabilities.StatusUnsupported, Notes: "via Publish with TargetArn", DocsURL: "[docs](https://docs.aws.amazon.com/sns/latest/dg/mobile-push-send-directmessage.html)", DocOnly: true},
		// SMS
		capabilities.Capability{Service: "sns", Operation: "SMS publish", Category: "SMS", Status: capabilities.StatusSupported, Notes: "via sms subscription on Publish; captured in the Inbox with kind=sms", DocsURL: "[docs](https://docs.aws.amazon.com/sns/latest/dg/sns-mobile-phone-number-as-subscriber.html)", DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "CheckIfPhoneNumberIsOptedOut", Category: "SMS", Status: capabilities.StatusUnsupported, DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "ListPhoneNumbersOptedOut", Category: "SMS", Status: capabilities.StatusUnsupported, DocOnly: true},
		capabilities.Capability{Service: "sns", Operation: "OptInPhoneNumber", Category: "SMS", Status: capabilities.StatusUnsupported, DocOnly: true},
	)
}
