//go:build dev

package ses

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// SES v1 — Sending
		// The v1 SendEmail is the Query-protocol action, not the v2 REST route the
		// row below it describes; AWS models it with no HTTP binding at all.
		capabilities.Capability{Service: "ses", Operation: "SendEmail", Category: "SES v1 — Sending", Status: capabilities.StatusSupported, Notes: "Query `Action=SendEmail`; simple content"},
		capabilities.Capability{Service: "ses", Operation: "SendRawEmail", Category: "SES v1 — Sending", Status: capabilities.StatusSupported, Notes: "Delivers raw MIME to mail capture"},
		// SES v1 — Identities
		capabilities.Capability{Service: "ses", Operation: "VerifyEmailIdentity", Category: "SES v1 — Identities", Status: capabilities.StatusSupported, Notes: "Auto-verified"},
		capabilities.Capability{Service: "ses", Operation: "VerifyDomainIdentity", Category: "SES v1 — Identities", Status: capabilities.StatusSupported, Notes: "Auto-verified; returns dummy token"},
		capabilities.Capability{Service: "ses", Operation: "ListIdentities", Category: "SES v1 — Identities", Status: capabilities.StatusSupported, Notes: "Supports `IdentityType` filter"},
		capabilities.Capability{Service: "ses", Operation: "ListVerifiedEmailAddresses", Category: "SES v1 — Identities", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "GetIdentityVerificationAttributes", Category: "SES v1 — Identities", Status: capabilities.StatusSupported, Notes: "Always returns `Success` status"},
		capabilities.Capability{Service: "ses", Operation: "DeleteIdentity", Category: "SES v1 — Identities", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "VerifyEmailAddress", Category: "SES v1 — Identities", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "DeleteVerifiedEmailAddress", Category: "SES v1 — Identities", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "SetIdentityFeedbackForwardingEnabled", Category: "SES v1 — Identities", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "GetIdentityDkimAttributes", Category: "SES v1 — Identities", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "GetIdentityMailFromDomainAttributes", Category: "SES v1 — Identities", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "GetIdentityNotificationAttributes", Category: "SES v1 — Identities", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "SetIdentityDkimEnabled", Category: "SES v1 — Identities", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "SetIdentityMailFromDomain", Category: "SES v1 — Identities", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "SetIdentityNotificationTopic", Category: "SES v1 — Identities", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "VerifyDomainDkim", Category: "SES v1 — Identities", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		// SES v1 — Templates
		capabilities.Capability{Service: "ses", Operation: "CreateTemplate", Category: "SES v1 — Templates", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "GetTemplate", Category: "SES v1 — Templates", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "UpdateTemplate", Category: "SES v1 — Templates", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "ListTemplates", Category: "SES v1 — Templates", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "DeleteTemplate", Category: "SES v1 — Templates", Status: capabilities.StatusSupported},
		capabilities.Capability{Service: "ses", Operation: "SendTemplatedEmail", Category: "SES v1 — Templates", Status: capabilities.StatusSupported, Notes: "`{{key}}` variable substitution"},
		// SES v1 — Other
		capabilities.Capability{Service: "ses", Operation: "GetSendQuota", Category: "SES v1 — Other", Status: capabilities.StatusSupported, Notes: "Returns unlimited quota"},
		capabilities.Capability{Service: "ses", Operation: "GetSendStatistics", Category: "SES v1 — Other", Status: capabilities.StatusSupported, Notes: "Returns empty data points"},
		capabilities.Capability{Service: "ses", Operation: "CreateConfigurationSet", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "DeleteConfigurationSet", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "ListConfigurationSets", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "CreateReceiptRule", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "CreateReceiptRuleSet", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "DeleteReceiptRule", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "DeleteReceiptRuleSet", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "DescribeReceiptRule", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "DescribeReceiptRuleSet", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},
		capabilities.Capability{Service: "ses", Operation: "ListReceiptRuleSets", Category: "SES v1 — Other", Status: capabilities.StatusUnsupported, Notes: "stub; returns 501"},

		// SES v2 — Sending
		//
		// Every V2* row below except V2Other is dispatched, by a REST route in
		// RegisterRoutes rather than by v1's action table. Each Notes field names the
		// method and path that route is registered on, and those must stay identical to
		// the operation's binding in the pinned models — #862 shipped a row claiming
		// `PUT /v2/email/identities` for an operation AWS binds to POST, which is both
		// how the wrong route got written and why the docs went on advertising it.
		//
		// DocOnly is therefore untrue of all eight, and kept anyway for now, because it
		// is the only way capgen accepts these rows. Its manifest check resolves an
		// operation name under the `ses` service key, where SES v2 is modeled as `sesv2`
		// and carries no V2 prefix, so every row reports UNKNOWN_MODEL_OPERATION without
		// it; its ORPHAN check looks only at the v1 action table, so every row reports
		// ORPHAN too. Both need capgen to learn about REST-routed operations — that is
		// #864's work, and the flag comes off these eight rows with it.
		//
		// DisplayName uses the canonical AWS operation name (without the V2 prefix used
		// internally to avoid clashes with v1).
		capabilities.Capability{Service: "ses", Operation: "V2SendEmail", Category: "SES v2 — Sending", Status: capabilities.StatusSupported, Notes: "`POST /v2/email/outbound-emails`; simple content", DisplayName: "SendEmail", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_SendEmail.html)"},

		// SES v2 — Identities
		capabilities.Capability{Service: "ses", Operation: "V2CreateEmailIdentity", Category: "SES v2 — Identities", Status: capabilities.StatusSupported, Notes: "`POST /v2/email/identities`; auto-verified; inline `Tags` applied at creation", DisplayName: "CreateEmailIdentity", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_CreateEmailIdentity.html)"},
		capabilities.Capability{Service: "ses", Operation: "V2ListEmailIdentities", Category: "SES v2 — Identities", Status: capabilities.StatusSupported, Notes: "`GET /v2/email/identities`", DisplayName: "ListEmailIdentities", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_ListEmailIdentities.html)"},
		capabilities.Capability{Service: "ses", Operation: "V2GetEmailIdentity", Category: "SES v2 — Identities", Status: capabilities.StatusSupported, Notes: "`GET /v2/email/identities/{EmailIdentity}`; reports the identity's `Tags`", DisplayName: "GetEmailIdentity", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_GetEmailIdentity.html)"},
		capabilities.Capability{Service: "ses", Operation: "V2DeleteEmailIdentity", Category: "SES v2 — Identities", Status: capabilities.StatusSupported, Notes: "`DELETE /v2/email/identities/{EmailIdentity}`", DisplayName: "DeleteEmailIdentity", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_DeleteEmailIdentity.html)"},

		// SES v2 — Tags
		capabilities.Capability{Service: "ses", Operation: "V2TagResource", Category: "SES v2 — Tags", Status: capabilities.StatusSupported, Notes: "`POST /v2/email/tags`; email identity ARNs — configuration sets are not emulated", DisplayName: "TagResource", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_TagResource.html)"},
		capabilities.Capability{Service: "ses", Operation: "V2UntagResource", Category: "SES v2 — Tags", Status: capabilities.StatusSupported, Notes: "`DELETE /v2/email/tags?ResourceArn=…&TagKeys=…`", DisplayName: "UntagResource", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_UntagResource.html)"},
		capabilities.Capability{Service: "ses", Operation: "V2ListTagsForResource", Category: "SES v2 — Tags", Status: capabilities.StatusSupported, Notes: "`GET /v2/email/tags?ResourceArn=…`", DisplayName: "ListTagsForResource", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_ListTagsForResource.html)"},

		// SES v2 — Other
		capabilities.Capability{Service: "ses", Operation: "V2Other", Category: "SES v2 — Other", Status: capabilities.StatusUnsupported, Notes: "Returns `NotImplemented`", DisplayName: "All other v2 operations", DocOnly: true},
	)
}
