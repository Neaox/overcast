---
title: "Organizations — AWS Organizations"
description: "Policies are stored and returned faithfully; DescribeOrganization is a fixed stub, and nothing is ever attached or enforced."
section: "Service Reference"
tags:
  - aws
  - docs
  - organizations
  - services
---

# Organizations — AWS Organizations

> AWS docs: https://docs.aws.amazon.com/organizations/latest/APIReference/Welcome.html

Organizations is emulated at the inert tier: policy metadata is stored and
returned faithfully — identifiers, ARNs, tags, pagination and the modeled
errors — and nothing a policy describes ever takes effect.

## Summary

Policies have a full CRUD and tagging surface. `DescribeOrganization` returns a
fixed organization so that CDK bootstrap gets past it. Everything else —
accounts, organizational units, roots, handshakes, delegated administrators —
returns 501 Not Implemented.

## Behavior Notes

- A policy's ID is derived from its name, so it is stable across restarts and
  across a state export/import. Creating a second policy with the same name
  returns `DuplicatePolicyException`.
- Renaming a policy through `UpdatePolicy` leaves its ID (and so its ARN)
  unchanged, matching AWS. One divergence follows from deriving the ID from the
  name: after a rename, the original name stays taken, so recreating it returns
  `DuplicatePolicyException` where AWS would allow it.
- `TagResource`, `UntagResource` and `ListTagsForResource` accept policy IDs
  only. Roots, OUs and accounts are not stored, so tagging one returns
  `TargetNotFoundException` rather than reporting a success that did not happen.
- Attaching a policy is not emulated — `AttachPolicy` and `DetachPolicy` return
  501 — so no policy is ever in effect and `DeletePolicy` never reports
  `PolicyInUseException`.
- `DescribeOrganization` returns a hardcoded organization with ID `o-overcast`
  and master account ID `000000000000`, for compatibility with CDK bootstrap
  operations that probe for its availability.

<!-- BEGIN overcast:capabilities -->

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

<!-- END overcast:capabilities -->
