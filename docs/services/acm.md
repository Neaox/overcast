---
title: "ACM — AWS Certificate Manager"
description: "Certificate records for stacks that need an ARN. A requested certificate is ISSUED immediately — no DNS or email validation, and no key material is generated."
section: "Service Reference"
tags:
  - acm
  - aws
  - certificate
  - docs
  - manager
  - services
---

# ACM — AWS Certificate Manager

Certificate records for stacks that need a certificate ARN to reference.
Nothing is issued cryptographically: a requested certificate is `ISSUED` on
return.

**Status:** ⚠️ Partial

## Quick start

```sh
export AWS_ENDPOINT_URL=http://localhost:4566

CERT=$(aws acm request-certificate \
  --domain-name example.com \
  --validation-method DNS \
  --query CertificateArn --output text)

aws acm describe-certificate --certificate-arn "$CERT"
```

## What works

| Area         | Behaviour                                                                                                               |
| ------------ | ----------------------------------------------------------------------------------------------------------------------- |
| Certificates | `RequestCertificate`, `DescribeCertificate`, `ListCertificates`, `DeleteCertificate`                                    |
| Tags         | The legacy `AddTagsToCertificate` family and the modern `TagResource` / `UntagResource` / `ListTagsForResource` aliases |
| Inline tags  | `Tags` supplied on `RequestCertificate` are applied at creation                                                         |

## Differences from AWS

| Behaviour            | On AWS                                                       | Here                                            |
| -------------------- | ------------------------------------------------------------ | ----------------------------------------------- |
| Validation           | DNS or email round-trip; `PENDING_VALIDATION` first           | Skipped; the certificate is `ISSUED` on return  |
| Certificate material | A real X.509 chain is issued                                  | No key or chain is generated                    |
| Import and renewal   | `ImportCertificate`, `RenewCertificate`, `ExportCertificate` | Not implemented — `501 Not Implemented`          |

> [!TIP]
> ACM issues nothing you can actually serve. For browser-trusted TLS in front
> of Overcast itself, see [HTTPS](../https.md).

<!-- BEGIN overcast:capabilities -->

## Operations

All 10 listed operations are implemented.
Per-operation status, notes and AWS API links: [ACM operations](acm/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/acm/latest/APIReference/)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
