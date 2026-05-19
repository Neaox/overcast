//go:build dev

package ses

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// SES v1 — Sending
		capabilities.Capability{Service: "ses", Operation: "SendEmail", Category: "SES v1 — Sending", Status: capabilities.StatusSupported, Notes: "`POST /v2/email/outbound-emails`; simple content"},
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
		// SES v2 uses REST routing (path-based), not action-dispatch. DocOnly exempts these
		// entries from the ORPHAN handler check. DisplayName uses the canonical AWS operation
		// name (without the V2 prefix used internally to avoid clashes with v1).
		capabilities.Capability{Service: "ses", Operation: "V2SendEmail", Category: "SES v2 — Sending", Status: capabilities.StatusSupported, Notes: "`POST /v2/email/outbound-emails`; simple content", DisplayName: "SendEmail", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_SendEmail.html)"},

		// SES v2 — Identities
		capabilities.Capability{Service: "ses", Operation: "V2CreateEmailIdentity", Category: "SES v2 — Identities", Status: capabilities.StatusSupported, Notes: "`PUT /v2/email/identities`; auto-verified", DisplayName: "CreateEmailIdentity", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_CreateEmailIdentity.html)"},
		capabilities.Capability{Service: "ses", Operation: "V2ListEmailIdentities", Category: "SES v2 — Identities", Status: capabilities.StatusSupported, Notes: "`GET /v2/email/identities`", DisplayName: "ListEmailIdentities", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_ListEmailIdentities.html)"},
		capabilities.Capability{Service: "ses", Operation: "V2GetEmailIdentity", Category: "SES v2 — Identities", Status: capabilities.StatusSupported, Notes: "`GET /v2/email/identities/{EmailIdentity}`", DisplayName: "GetEmailIdentity", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_GetEmailIdentity.html)"},
		capabilities.Capability{Service: "ses", Operation: "V2DeleteEmailIdentity", Category: "SES v2 — Identities", Status: capabilities.StatusSupported, Notes: "`DELETE /v2/email/identities/{EmailIdentity}`", DisplayName: "DeleteEmailIdentity", DocOnly: true, DocsURL: "[docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_DeleteEmailIdentity.html)"},

		// SES v2 — Other
		capabilities.Capability{Service: "ses", Operation: "V2Other", Category: "SES v2 — Other", Status: capabilities.StatusUnsupported, Notes: "Returns `NotImplemented`", DisplayName: "All other v2 operations", DocOnly: true},
	)
}
