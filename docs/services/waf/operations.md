---
title: "WAF v2 operations"
description: "Every WAF v2 operation Overcast declares — 7 of 7 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - services
  - waf
---

<!-- BEGIN overcast:capabilities -->

# WAF v2 operations

All 7 listed operations are implemented. Back to [WAF v2](../waf.md).

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| Web ACLs | 7            |

---

## Endpoints

### Web ACLs

| Operation             | Status       | Notes                                 | AWS Docs                                                                                 |
| --------------------- | ------------ | ------------------------------------- | ---------------------------------------------------------------------------------------- |
| `CreateWebACL`        | ✅ Supported | Returns Summary with Id/LockToken     | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_CreateWebACL.html)        |
| `GetWebACL`           | ✅ Supported |                                       | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_GetWebACL.html)           |
| `ListWebACLs`         | ✅ Supported |                                       | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_ListWebACLs.html)         |
| `DeleteWebACL`        | ✅ Supported | LockToken accepted but not checked    | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_DeleteWebACL.html)        |
| `TagResource`         | ✅ Supported | Adds/merges tags by WebACL ARN        | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes tags by key from a WebACL ARN | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | ✅ Supported | Returns tags for a WebACL ARN         | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_ListTagsForResource.html) |

## Related

- [WAF v2](../waf.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
