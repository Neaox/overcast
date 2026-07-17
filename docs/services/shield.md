---
title: "Shield — AWS Shield"
description: "AWS Shield (DDoS protection) uses the application/x-amz-json-1.1 protocol. Operations are identified by the X-Amz-Target header with the prefix AWSShield_20160616.."
section: "Service Reference"
tags:
  - aws
  - docs
  - services
  - shield
---

# Shield — AWS Shield

> AWS docs: https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/Welcome.html

AWS Shield (DDoS protection) uses the `application/x-amz-json-1.1` protocol.
Operations are identified by the `X-Amz-Target` header with the prefix
`AWSShield_20160616.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: AWSShield_20160616.<Operation>`.
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Protection resources are stored but Shield Advanced features (e.g. DDoS cost protection, response team) are not emulated.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category     | ✅ Supported |
| ------------ | ------------ |
| Subscription | 1            |
| Protections  | 4            |

---

## Endpoints

### Subscription

| Operation              | Status       | Notes                                        | AWS Docs                                                                                      |
| ---------------------- | ------------ | -------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `DescribeSubscription` | ✅ Supported | Returns a minimal active subscription object | [docs](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/API_DescribeSubscription.html) |

### Protections

| Operation            | Status       | Notes                                       | AWS Docs                                                                                    |
| -------------------- | ------------ | ------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `CreateProtection`   | ✅ Supported | Creates a protection; requires Name and ARN | [docs](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/API_CreateProtection.html)   |
| `DescribeProtection` | ✅ Supported | Lookup by ProtectionId or ResourceArn       | [docs](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/API_DescribeProtection.html) |
| `ListProtections`    | ✅ Supported | Lists all protections                       | [docs](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/API_ListProtections.html)    |
| `DeleteProtection`   | ✅ Supported | Deletes a protection by ID                  | [docs](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/API_DeleteProtection.html)   |

<!-- END overcast:capabilities -->
