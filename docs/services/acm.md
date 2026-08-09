---
title: "ACM — AWS Certificate Manager"
description: "AWS Certificate Manager uses the application/x-amz-json-1.1 protocol. Operations are identified by the X-Amz-Target header with the prefix CertificateManager.."
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

> AWS docs: https://docs.aws.amazon.com/acm/latest/APIReference/

AWS Certificate Manager uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`CertificateManager.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: CertificateManager.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Certificates are immediately issued with status `ISSUED` — no DNS or email validation is performed.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category     | ✅ Supported |
| ------------ | ------------ |
| Certificates | 4            |
| Tags         | 6            |

---

## Endpoints

### Certificates

| Operation             | Status       | Notes                                                                        | AWS Docs                                                                                 |
| --------------------- | ------------ | ---------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `RequestCertificate`  | ✅ Supported | Creates a certificate; immediately ISSUED; inline `Tags` applied at creation | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_RequestCertificate.html)  |
| `DescribeCertificate` | ✅ Supported | Returns certificate details                                                  | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_DescribeCertificate.html) |
| `ListCertificates`    | ✅ Supported | Lists all certificates                                                       | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_ListCertificates.html)    |
| `DeleteCertificate`   | ✅ Supported | Deletes a certificate by ARN                                                 | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_DeleteCertificate.html)   |

### Tags

| Operation                   | Status       | Notes                                                                               | AWS Docs                                                                                       |
| --------------------------- | ------------ | ----------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `ListTagsForCertificate`    | ✅ Supported | Lists tags for a certificate                                                        | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_ListTagsForCertificate.html)    |
| `AddTagsToCertificate`      | ✅ Supported | Adds tags to a certificate                                                          | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_AddTagsToCertificate.html)      |
| `RemoveTagsFromCertificate` | ✅ Supported | Removes tags from a certificate                                                     | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_RemoveTagsFromCertificate.html) |
| `TagResource`               | ✅ Supported | Modern alias of `AddTagsToCertificate`, addressing the certificate by `ResourceArn` | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_TagResource.html)               |
| `UntagResource`             | ✅ Supported | Takes `TagKeys`, where `RemoveTagsFromCertificate` takes a `Tags` list              | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_UntagResource.html)             |
| `ListTagsForResource`       | ✅ Supported | Modern alias of `ListTagsForCertificate`                                            | [docs](https://docs.aws.amazon.com/acm/latest/APIReference/API_ListTagsForResource.html)       |

<!-- END overcast:capabilities -->
