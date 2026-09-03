---
title: "Transfer Family — AWS Transfer Family"
description: "Quick start, the server, user and tag records that are stored, and why there is no data plane: no protocol served, no storage binding, no authentication."
section: "Service Reference"
tags:
  - aws
  - docs
  - family
  - services
  - transfer
---

# Transfer Family — AWS Transfer Family

Servers and users are stored as records; no SFTP, FTPS or FTP daemon is ever
started, so there is nothing to connect to.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

SERVER=$(aws transfer create-server --query ServerId --output text)
aws transfer create-user \
  --server-id "$SERVER" \
  --user-name alice \
  --role arn:aws:iam::000000000000:role/transfer-access \
  --home-directory /uploads
aws transfer list-users --server-id "$SERVER"
```

## What works

| Area | Behaviour |
| --- | --- |
| Servers | Create, describe, list, update, delete; `State` is `ONLINE` immediately |
| Defaults | `EndpointType` defaults to `PUBLIC` and `IdentityProviderType` to `SERVICE_MANAGED` |
| Users | Create, describe, list, update, delete, with `Role`, `HomeDirectory` and `Policy` stored |
| Cascade delete | Deleting a server deletes its users |
| Tags | Inline `Tags` on create, plus `TagResource`, `UntagResource` and `ListTagsForResource` on server and user ARNs |

## Differences from AWS

| Area                            | Overcast                                                                                                                                                                                                                                                                                                               |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| No data plane                   | No file-transfer protocol is served, so no client can log in and no file moves                                                                                                                                                                                                                                         |
| No S3 or EFS binding            | `HomeDirectory` is a stored string that maps to nothing                                                                                                                                                                                                                                                                |
| Server fields dropped           | `Protocols`, `EndpointDetails`, `LoggingRole`, `Domain` and `SecurityPolicyName` are accepted and discarded; only `EndpointType` and `IdentityProviderType` are kept                                                                                                                                                    |
| User fields dropped             | `HomeDirectoryType`, `HomeDirectoryMappings`, `PosixProfile` and `SshPublicKeyBody` are accepted and discarded; only `Role`, `HomeDirectory` and `Policy` are kept                                                                                                                                                      |
| No authentication               | SSH public keys, service-managed credentials and custom identity providers are not exercised                                                                                                                                                                                                                           |
| No endpoint address             | `DescribeServer` reports no hostname, VPC endpoint or Elastic IP                                                                                                                                                                                                                                                       |
| Sequential server ids           | Ids are minted as `s-00000001`, `s-00000002`, … rather than AWS's random ids                                                                                                                                                                                                                                           |
| Other resources absent          | Agreements, certificates, connectors, profiles and workflows are not emulated, and their ARNs are refused by the tag operations                                                                                                                                                                                        |

<!-- BEGIN overcast:capabilities -->

## Operations

All 13 listed operations are implemented.
Per-operation status, notes and AWS API links: [Transfer Family operations](transfer/operations.md).

<!-- END overcast:capabilities -->

## Related

- [S3](./s3.md) — the storage a real Transfer server would front
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/transfer/latest/userguide/API_Reference.html)
