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

Organizations is emulated at the inert tier: policy metadata is stored and
returned faithfully — identifiers, ARNs, tags, pagination and the modeled
errors — and nothing a policy describes ever takes effect.

## What works
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

## Operations

All 9 listed operations are implemented.
Per-operation status, notes and AWS API links: [Organizations operations](organizations/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/organizations/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
