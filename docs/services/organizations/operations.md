---
title: "Organizations operations"
description: "Every Organizations operation Overcast declares — 15 of 15 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - organizations
  - services
---

<!-- BEGIN overcast:capabilities -->

# Organizations operations

All 15 listed operations are implemented. Back to [Organizations](../organizations.md).

## Summary

| Category                       | 🧊 Inert |
| ------------------------------ | -------- |
| Operations                     | 1        |
| Policy operations              | 5        |
| Root operations                | 1        |
| Organizational unit operations | 5        |
| Tag operations                 | 3        |

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

### Root operations

| Operation   | Status   | Notes                                                                                                                              | AWS Docs                                                                                 |
| ----------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `ListRoots` | 🧊 Inert | One root per organization, with an ID derived from the organization ID. PolicyTypes is always empty: enabling one is not emulated. | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_ListRoots.html) |

### Organizational unit operations

| Operation                          | Status   | Notes                                                                                                                        | AWS Docs                                                                                                        |
| ---------------------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `CreateOrganizationalUnit`         | 🧊 Inert | The parent must be the root or an existing unit. Derives the ID, ARN and Path; a duplicate name under one parent is refused. | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_CreateOrganizationalUnit.html)         |
| `DescribeOrganizationalUnit`       | 🧊 Inert |                                                                                                                              | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_DescribeOrganizationalUnit.html)       |
| `UpdateOrganizationalUnit`         | 🧊 Inert | Renames the unit. Its ID, ARN and Path are stable across the rename, and a sibling's name is refused.                        | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_UpdateOrganizationalUnit.html)         |
| `DeleteOrganizationalUnit`         | 🧊 Inert | OrganizationalUnitNotEmptyException covers child units only; accounts are not emulated, so none can be in the way.           | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_DeleteOrganizationalUnit.html)         |
| `ListOrganizationalUnitsForParent` | 🧊 Inert | Direct children only, paginated. An unknown parent is ParentNotFoundException, not an empty list.                            | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_ListOrganizationalUnitsForParent.html) |

### Tag operations

| Operation             | Status   | Notes                                                                                                                      | AWS Docs                                                                                           |
| --------------------- | -------- | -------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `TagResource`         | 🧊 Inert | Policies, organizational units and the root. An account ID returns TargetNotFoundException, since accounts are not stored. | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | 🧊 Inert | Policies, organizational units and the root, as for TagResource.                                                           | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_UntagResource.html)       |
| `ListTagsForResource` | 🧊 Inert | Policies, organizational units and the root, as for TagResource.                                                           | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_ListTagsForResource.html) |

## Related

- [Organizations](../organizations.md) — quick start, what works, and the differences from AWS
- [All service pages](../README.md)

<!-- END overcast:capabilities -->
