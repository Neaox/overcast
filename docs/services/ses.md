---
title: "SES — Simple Email Service"
description: "SES supports both the v1 Query protocol (form-encoded POST with Action field, XML responses) and the v2 REST-JSON protocol (path-based routing, JSON request/response bodies)."
section: "Service Reference"
tags:
  - docs
  - email
  - service
  - services
  - ses
  - simple
---

# SES — Simple Email Service

> AWS docs: https://docs.aws.amazon.com/ses/latest/APIReference/Welcome.html
> SES v2 docs: https://docs.aws.amazon.com/ses/latest/APIReference-V2/Welcome.html

SES supports both the v1 Query protocol (form-encoded POST with `Action` field,
XML responses) and the v2 REST-JSON protocol (path-based routing, JSON
request/response bodies).

> [!NOTE]
> Emails are **not delivered** — all outbound messages are captured and visible in the
> Mail page of the web console. All identities are automatically verified; there is no
> real verification flow.

---

## SDK compatibility

| SDK            | Client                        | Status      |
| -------------- | ----------------------------- | ----------- |
| Go v2          | `aws-sdk-go-v2/service/ses`   | ✅ Tested   |
| Go v2          | `aws-sdk-go-v2/service/sesv2` | ✅ Tested   |
| Python (boto3) | `boto3.client("ses")`         | ✅ Expected |
| Python (boto3) | `boto3.client("sesv2")`       | ✅ Expected |
| JS/TS v3       | `@aws-sdk/client-ses`         | ✅ Expected |
| JS/TS v3       | `@aws-sdk/client-sesv2`       | ✅ Expected |

## Web console

The SES page (`/ses`) shows all verified identities with the ability to add
new email/domain identities and delete existing ones. Emails sent via SES
appear in the Mail page.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category            | ✅ Supported | ❌ Unsupported |
| ------------------- | ------------ | -------------- |
| SES v1 — Sending    | 2            |                |
| SES v1 — Identities | 9            | 7              |
| SES v1 — Templates  | 6            |                |
| SES v1 — Other      | 2            | 10             |
| SES v2 — Sending    | 1            |                |
| SES v2 — Identities | 4            |                |
| SES v2 — Other      |              | 1              |

---

## Endpoints

### SES v1 — Sending

| Operation      | Status       | Notes                                            | AWS Docs                                                                          |
| -------------- | ------------ | ------------------------------------------------ | --------------------------------------------------------------------------------- |
| `SendEmail`    | ✅ Supported | `POST /v2/email/outbound-emails`; simple content | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_SendEmail.html)    |
| `SendRawEmail` | ✅ Supported | Delivers raw MIME to mail capture                | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_SendRawEmail.html) |

### SES v1 — Identities

| Operation                              | Status         | Notes                              | AWS Docs                                                                                                  |
| -------------------------------------- | -------------- | ---------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `VerifyEmailIdentity`                  | ✅ Supported   | Auto-verified                      | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_VerifyEmailIdentity.html)                  |
| `VerifyDomainIdentity`                 | ✅ Supported   | Auto-verified; returns dummy token | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_VerifyDomainIdentity.html)                 |
| `ListIdentities`                       | ✅ Supported   | Supports `IdentityType` filter     | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_ListIdentities.html)                       |
| `ListVerifiedEmailAddresses`           | ✅ Supported   |                                    | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_ListVerifiedEmailAddresses.html)           |
| `GetIdentityVerificationAttributes`    | ✅ Supported   | Always returns `Success` status    | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_GetIdentityVerificationAttributes.html)    |
| `DeleteIdentity`                       | ✅ Supported   |                                    | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_DeleteIdentity.html)                       |
| `VerifyEmailAddress`                   | ✅ Supported   |                                    | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_VerifyEmailAddress.html)                   |
| `DeleteVerifiedEmailAddress`           | ✅ Supported   |                                    | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_DeleteVerifiedEmailAddress.html)           |
| `SetIdentityFeedbackForwardingEnabled` | ✅ Supported   |                                    | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_SetIdentityFeedbackForwardingEnabled.html) |
| `GetIdentityDkimAttributes`            | ❌ Unsupported | stub; returns 501                  | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_GetIdentityDkimAttributes.html)            |
| `GetIdentityMailFromDomainAttributes`  | ❌ Unsupported | stub; returns 501                  | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_GetIdentityMailFromDomainAttributes.html)  |
| `GetIdentityNotificationAttributes`    | ❌ Unsupported | stub; returns 501                  | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_GetIdentityNotificationAttributes.html)    |
| `SetIdentityDkimEnabled`               | ❌ Unsupported | stub; returns 501                  | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_SetIdentityDkimEnabled.html)               |
| `SetIdentityMailFromDomain`            | ❌ Unsupported | stub; returns 501                  | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_SetIdentityMailFromDomain.html)            |
| `SetIdentityNotificationTopic`         | ❌ Unsupported | stub; returns 501                  | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_SetIdentityNotificationTopic.html)         |
| `VerifyDomainDkim`                     | ❌ Unsupported | stub; returns 501                  | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_VerifyDomainDkim.html)                     |

### SES v1 — Templates

| Operation            | Status       | Notes                           | AWS Docs                                                                                |
| -------------------- | ------------ | ------------------------------- | --------------------------------------------------------------------------------------- |
| `CreateTemplate`     | ✅ Supported |                                 | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_CreateTemplate.html)     |
| `GetTemplate`        | ✅ Supported |                                 | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_GetTemplate.html)        |
| `UpdateTemplate`     | ✅ Supported |                                 | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_UpdateTemplate.html)     |
| `ListTemplates`      | ✅ Supported |                                 | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_ListTemplates.html)      |
| `DeleteTemplate`     | ✅ Supported |                                 | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_DeleteTemplate.html)     |
| `SendTemplatedEmail` | ✅ Supported | `{{key}}` variable substitution | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_SendTemplatedEmail.html) |

### SES v1 — Other

| Operation                | Status         | Notes                     | AWS Docs                                                                                    |
| ------------------------ | -------------- | ------------------------- | ------------------------------------------------------------------------------------------- |
| `GetSendQuota`           | ✅ Supported   | Returns unlimited quota   | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_GetSendQuota.html)           |
| `GetSendStatistics`      | ✅ Supported   | Returns empty data points | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_GetSendStatistics.html)      |
| `CreateConfigurationSet` | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_CreateConfigurationSet.html) |
| `DeleteConfigurationSet` | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_DeleteConfigurationSet.html) |
| `ListConfigurationSets`  | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_ListConfigurationSets.html)  |
| `CreateReceiptRule`      | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_CreateReceiptRule.html)      |
| `CreateReceiptRuleSet`   | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_CreateReceiptRuleSet.html)   |
| `DeleteReceiptRule`      | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_DeleteReceiptRule.html)      |
| `DeleteReceiptRuleSet`   | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_DeleteReceiptRuleSet.html)   |
| `DescribeReceiptRule`    | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_DescribeReceiptRule.html)    |
| `DescribeReceiptRuleSet` | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_DescribeReceiptRuleSet.html) |
| `ListReceiptRuleSets`    | ❌ Unsupported | stub; returns 501         | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_ListReceiptRuleSets.html)    |

### SES v2 — Sending

| Operation   | Status       | Notes                                            | AWS Docs                                                                          |
| ----------- | ------------ | ------------------------------------------------ | --------------------------------------------------------------------------------- |
| `SendEmail` | ✅ Supported | `POST /v2/email/outbound-emails`; simple content | [docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_SendEmail.html) |

### SES v2 — Identities

| Operation             | Status       | Notes                                         | AWS Docs                                                                                    |
| --------------------- | ------------ | --------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `CreateEmailIdentity` | ✅ Supported | `PUT /v2/email/identities`; auto-verified     | [docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_CreateEmailIdentity.html) |
| `ListEmailIdentities` | ✅ Supported | `GET /v2/email/identities`                    | [docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_ListEmailIdentities.html) |
| `GetEmailIdentity`    | ✅ Supported | `GET /v2/email/identities/{EmailIdentity}`    | [docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_GetEmailIdentity.html)    |
| `DeleteEmailIdentity` | ✅ Supported | `DELETE /v2/email/identities/{EmailIdentity}` | [docs](https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_DeleteEmailIdentity.html) |

### SES v2 — Other

| Operation                 | Status         | Notes                    | AWS Docs                                                                     |
| ------------------------- | -------------- | ------------------------ | ---------------------------------------------------------------------------- |
| `All other v2 operations` | ❌ Unsupported | Returns `NotImplemented` | [docs](https://docs.aws.amazon.com/ses/latest/APIReference/API_V2Other.html) |

<!-- END overcast:capabilities -->
