---
title: "Organizations — AWS Organizations"
description: "Quick start, the policy and organizational-unit CRUD, tagging and stable IDs that work, and what they never do: nothing is attached, enforced or placed in a unit."
section: "Service Reference"
tags:
  - aws
  - docs
  - organizations
  - services
---

# Organizations — AWS Organizations

Policy and organizational-unit metadata is stored and returned faithfully —
identifiers, ARNs, tags, pagination, modelled errors. Nothing a policy
describes ever takes effect, and nothing is ever placed in a unit.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws organizations create-policy \
  --name deny-expensive --type SERVICE_CONTROL_POLICY \
  --description "example" \
  --content '{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"ec2:RunInstances","Resource":"*"}]}'

aws organizations list-policies --filter SERVICE_CONTROL_POLICY

aws organizations create-organizational-unit   --parent-id "$(aws organizations list-roots --query 'Roots[0].Id' --output text)"   --name workloads
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

## What works

| Area                   | Behaviour                                                                            |
| ---------------------- | ------------------------------------------------------------------------------------ |
| Policies               | Full CRUD: `CreatePolicy`, `DescribePolicy`, `UpdatePolicy`, `DeletePolicy`, `ListPolicies` |
| Organizational units   | Full CRUD under a real tree: `CreateOrganizationalUnit`, `DescribeOrganizationalUnit`, `UpdateOrganizationalUnit`, `DeleteOrganizationalUnit`, `ListOrganizationalUnitsForParent` |
| Roots                  | `ListRoots` — one root per organization, and the only legal parent for a top-level unit |
| Tags                   | `TagResource`, `UntagResource`, `ListTagsForResource` — policies, units and the root  |
| `DescribeOrganization` | A single organization per account, with an AWS-shaped ID (`o-` plus ten characters, derived deterministically from the account ID) so CDK bootstrap gets past it |
| Stable IDs             | Every identifier is derived rather than minted — the organization's from the account, the root's from the organization, a unit's from its parent and name — so all of them survive restarts and a state export/import |

## Differences from AWS

| Area                                            | On AWS                                     | Overcast                                                               |
| ----------------------------------------------- | ------------------------------------------ | ---------------------------------------------------------------------- |
| Policy enforcement                              | An attached SCP constrains member accounts | Nothing is attached; `AttachPolicy` / `DetachPolicy` return 501        |
| `DeletePolicy`                                  | `PolicyInUseException` while attached      | Never in use, so never refused                                         |
| Renaming a policy or unit                       | The old name becomes free again            | The old name stays taken — recreating it is a duplicate                |
| What is inside a unit                           | Accounts and child units                   | Child units only; `OrganizationalUnitNotEmptyException` counts those   |
| `Root.PolicyTypes`                              | Lists the types enabled on the root        | Always empty — `EnablePolicyType` returns 501, so none can be enabled  |
| Tagging an account                              | Succeeds                                   | `TargetNotFoundException` — accounts are not stored                    |
| Accounts, handshakes, delegated administrators  | Full API                                   | Not implemented — `501 Not Implemented`                                |

## Gotchas

> [!NOTE]
> A policy's ID comes from its name, and a unit's from its parent and name,
> which is why a rename keeps the ARN stable (as on AWS) but leaves the
> original name occupied (unlike AWS).

A unit's `Path` is derived from the tree it sits in, so it is accurate for
however deep the unit is nested — but nothing moves a unit once created, as
AWS models no operation that does.

<!-- BEGIN overcast:capabilities -->

## Operations

All 15 listed operations are implemented.
Per-operation status, notes and AWS API links: [Organizations operations](organizations/operations.md).

<!-- END overcast:capabilities -->

## Related

- [IAM](./iam.md) — the policy language that is actually evaluated here
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/organizations/latest/APIReference/Welcome.html)
