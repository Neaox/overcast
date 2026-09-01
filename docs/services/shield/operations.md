---
title: "Shield operations"
description: "Every Shield operation Overcast declares — 8 of 8 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - services
  - shield
---

<!-- BEGIN overcast:capabilities -->

# Shield operations

All 8 listed operations are implemented. Back to [Shield](../shield.md).

## Summary

| Category     | ✅ Supported |
| ------------ | ------------ |
| Subscription | 1            |
| Protections  | 4            |
| Tags         | 3            |

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

### Tags

| Operation             | Status       | Notes                              | AWS Docs                                                                                     |
| --------------------- | ------------ | ---------------------------------- | -------------------------------------------------------------------------------------------- |
| `TagResource`         | ✅ Supported | Adds/updates tags on a protection  | [docs](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes tag keys from a protection | [docs](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Lists tags on a protection         | [docs](https://docs.aws.amazon.com/waf/latest/DDOSAPIReference/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
