---
title: "Organizations operations"
description: "Every Organizations operation Overcast declares — 9 of 9 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - organizations
  - services
---

<!-- BEGIN overcast:capabilities -->

# Organizations operations

All 9 listed operations are implemented. Back to [Organizations](../organizations.md).

## Summary

| Category          | 🧊 Inert |
| ----------------- | -------- |
| Operations        | 1        |
| Policy operations | 5        |
| Tag operations    | 3        |

---

## Endpoints

### Operations

| Operation              | Status   | Notes | AWS Docs                                                                                            |
| ---------------------- | -------- | ----- | --------------------------------------------------------------------------------------------------- |
| `DescribeOrganization` | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_DescribeOrganization.html) |

### Policy operations

| Operation        | Status   | Notes                                                                                                 | AWS Docs                                                                                      |
| ---------------- | -------- | ----------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `CreatePolicy`   | 🧊 Inert | Stores the policy and derives its ID and ARN. The document is never evaluated.                        | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_CreatePolicy.html)   |
| `DescribePolicy` | 🧊 Inert |                                                                                                       | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_DescribePolicy.html) |
| `UpdatePolicy`   | 🧊 Inert | Merges the members the caller sent; the policy ARN is stable across a rename.                         | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_UpdatePolicy.html)   |
| `DeletePolicy`   | 🧊 Inert | PolicyInUseException is unreachable: attaching a policy is not emulated, so no policy can be in use.  | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_DeletePolicy.html)   |
| `ListPolicies`   | 🧊 Inert | Filters by the required policy type and paginates. An invalid NextToken is rejected, never restarted. | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_ListPolicies.html)   |

### Tag operations

| Operation             | Status   | Notes                                                                                                        | AWS Docs                                                                                           |
| --------------------- | -------- | ------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------- |
| `TagResource`         | 🧊 Inert | Policies only. A root, OU or account ID returns TargetNotFoundException, since none of those are stored yet. | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | 🧊 Inert | Policies only, as for TagResource.                                                                           | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | 🧊 Inert | Policies only, as for TagResource.                                                                           | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_ListTagsForResource.html) |

## Related

- [Organizations](../organizations.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
