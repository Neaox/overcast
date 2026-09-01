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

## Operations

27 of 45 listed operations are implemented.
Per-operation status, notes and AWS API links: [SES operations](ses/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/ses/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
