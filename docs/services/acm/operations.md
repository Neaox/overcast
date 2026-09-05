---
title: "ACM operations"
description: "Every ACM operation Overcast declares — 11 of 11 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - acm
  - docs
  - operations
  - services
---

<!-- BEGIN overcast:capabilities -->

# ACM operations

All 11 listed operations are implemented. Back to [ACM](../acm.md).

## Summary

| Category     | ✅ Supported | ⚠️ Partial |
| ------------ | ------------ | ---------- |
| Certificates | 4            | 1          |
| Tags         | 6            |            |

---

## Endpoints

### Certificates

| Operation                          | Status       | Notes                                                                                                                                                         | AWS Docs                                                                                              |
| ---------------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `RequestCertificate`               | ✅ Supported | Creates a certificate; immediately ISSUED; inline `Tags` applied at creation                                                                                  | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_RequestCertificate.html)               |
| `DescribeCertificate`              | ✅ Supported | Returns certificate details                                                                                                                                   | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_DescribeCertificate.html)              |
| `ListCertificates`                 | ✅ Supported | Lists all certificates                                                                                                                                        | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_ListCertificates.html)                 |
| `ListCertificateDomainValidations` | ⚠️ Partial   | One synthesized SUCCESS entry per DomainName/SAN; no ValidationMethod or DNS/email challenge data — Overcast issues certificates without ever validating them | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_ListCertificateDomainValidations.html) |
| `DeleteCertificate`                | ✅ Supported | Deletes a certificate by ARN                                                                                                                                  | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_DeleteCertificate.html)                |

### Tags

| Operation                   | Status       | Notes                                                                               | AWS Docs                                                                                       |
| --------------------------- | ------------ | ----------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `ListTagsForCertificate`    | ✅ Supported | Lists tags for a certificate                                                        | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_ListTagsForCertificate.html)    |
| `AddTagsToCertificate`      | ✅ Supported | Adds tags to a certificate                                                          | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_AddTagsToCertificate.html)      |
| `RemoveTagsFromCertificate` | ✅ Supported | Removes tags from a certificate                                                     | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_RemoveTagsFromCertificate.html) |
| `TagResource`               | ✅ Supported | Modern alias of `AddTagsToCertificate`, addressing the certificate by `ResourceArn` | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_TagResource.html)               |
| `UntagResource`             | ✅ Supported | Takes `TagKeys`, where `RemoveTagsFromCertificate` takes a `Tags` list              | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_UntagResource.html)             |
| `ListTagsForResource`       | ✅ Supported | Modern alias of `ListTagsForCertificate`                                            | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_ListTagsForResource.html)       |

## Related

- [ACM](../acm.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
