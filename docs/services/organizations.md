---
title: "Organizations — AWS Organizations"
description: "Quick start, the policy CRUD, tagging and stable IDs that work, and what a policy never does: nothing is attached, and nothing is enforced."
section: "Service Reference"
tags:
  - aws
  - docs
  - organizations
  - services
---

# Organizations — AWS Organizations

Policy metadata is stored and returned faithfully — identifiers, ARNs, tags,
pagination, modelled errors. Nothing a policy describes ever takes effect.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws organizations create-policy \
  --name deny-expensive --type SERVICE_CONTROL_POLICY \
  --description "example" \
  --content '{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"ec2:RunInstances","Resource":"*"}]}'

aws organizations list-policies --filter SERVICE_CONTROL_POLICY
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

## What works

| Area                   | Behaviour                                                                            |
| ---------------------- | ------------------------------------------------------------------------------------ |
| Policies               | Full CRUD: `CreatePolicy`, `DescribePolicy`, `UpdatePolicy`, `DeletePolicy`, `ListPolicies` |
| Tags                   | `TagResource`, `UntagResource`, `ListTagsForResource` — policy IDs only               |
| `DescribeOrganization` | A fixed organization (`o-overcast`, master account `000000000000`), so CDK bootstrap gets past it |
| Stable IDs             | A policy's ID derives from its name, so it survives restarts and a state export/import |

## Differences from AWS

| Area                                                       | On AWS                                     | Overcast                                                               |
| ---------------------------------------------------------- | ------------------------------------------ | ---------------------------------------------------------------------- |
| Policy enforcement                                         | An attached SCP constrains member accounts | Nothing is attached; `AttachPolicy` / `DetachPolicy` return 501        |
| `DeletePolicy`                                             | `PolicyInUseException` while attached      | Never in use, so never refused                                         |
| Renaming a policy                                          | The old name becomes free again            | The old name stays taken — recreating it is `DuplicatePolicyException` |
| Tagging a root/OU/account                                  | Succeeds                                   | `TargetNotFoundException` — those entities are not stored              |
| Accounts, OUs, roots, handshakes, delegated administrators | Full API                                   | Not implemented — `501 Not Implemented`                                |

## Gotchas

> [!NOTE]
> A policy's ID comes from its name, which is why a rename keeps its ARN
> stable (as on AWS) but leaves the original name occupied (unlike AWS).

<!-- BEGIN overcast:capabilities -->

## Operations

All 9 listed operations are implemented.
Per-operation status, notes and AWS API links: [Organizations operations](organizations/operations.md).

<!-- END overcast:capabilities -->

## Related

- [IAM](./iam.md) — the policy language that is actually evaluated here
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/organizations/latest/APIReference/Welcome.html)
