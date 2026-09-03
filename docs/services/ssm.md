---
title: "SSM Parameter Store — AWS Systems Manager"
description: "Parameter Store only: put, get, path queries, history and tags. SecureString values are masked without WithDecryption but are never actually encrypted."
section: "Service Reference"
tags:
  - docs
  - parameter
  - services
  - ssm
  - store
  - systems-manager
---

# SSM Parameter Store — AWS Systems Manager

Parameter Store, and nothing else from Systems Manager. Documents, automation
and run command all return `501 Not Implemented`.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

aws ssm put-parameter --name /app/db/url --type String --value postgres://localhost/app
aws ssm get-parameter --name /app/db/url --query Parameter.Value --output text
```

Any credentials work; with none configured, run `eval "$(overcast env)"` first
— see [Using AWS SDKs and CLI](../sdk-cli.md#credentials).

## What works

| Area         | Behaviour                                                                                          |
| ------------ | ---------------------------------------------------------------------------------------------------- |
| Writes       | `PutParameter` with `String`, `StringList` and `SecureString`; `Overwrite` creates a new version      |
| Reads        | `GetParameter`, `GetParameters` (unknown names come back in `InvalidParameters`), `GetParameterHistory` |
| Path queries | `GetParametersByPath`, recursive or direct children only                                             |
| Listing      | `DescribeParameters` with a name `BeginsWith` filter                                                 |
| Pagination   | `MaxResults` + `NextToken` on `GetParametersByPath` and `DescribeParameters`                         |
| Tags         | `AddTagsToResource`, `ListTagsForResource`, `RemoveTagsFromResource`                                 |
| Deletes      | `DeleteParameter`, `DeleteParameters`                                                                |

Every `PutParameter` — overwrite included — creates a new version, and
`GetParameterHistory` returns them all.

## Differences from AWS

| Area                        | On AWS                                                                | Overcast                                                                   |
| --------------------------- | --------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `SecureString`              | Encrypted with a KMS key                                              | Stored in plaintext; masked in the response unless `WithDecryption` is set |
| Version labels              | `LabelParameterVersion` / `UnlabelParameterVersion`                   | Not implemented — `501 Not Implemented`                                    |
| The rest of Systems Manager | Documents, automation, run command, patch baselines, service settings | Not implemented — `501 Not Implemented`                                    |

## Gotchas

> [!CAUTION]
> A `SecureString` is not secure here. `WithDecryption: true` returns the
> plaintext because the plaintext is what was stored.

<!-- BEGIN overcast:capabilities -->

## Operations

11 of 18 listed operations are implemented.
Per-operation status, notes and AWS API links: [SSM operations](ssm/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Secrets Manager](./secretsmanager.md) — versioning, rotation and resource policies
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/systems-manager/latest/APIReference/Welcome.html)
