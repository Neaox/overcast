---
title: "Transfer Family — AWS Transfer Family"
description: "Transfer Family servers and users as control-plane records. No SFTP, FTPS or FTP daemon is started, so nothing can connect to a server this creates."
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

| Difference | Detail |
| --- | --- |
| No data plane | No file-transfer protocol is served, so no client can log in and no file moves |
| No S3 or EFS binding | `HomeDirectory` is a stored string that maps to nothing |
| Most request fields are dropped | A server keeps only `EndpointType` and `IdentityProviderType` — `Protocols`, `EndpointDetails`, `LoggingRole`, `Domain` and `SecurityPolicyName` are accepted and discarded. A user keeps `Role`, `HomeDirectory` and `Policy`, not `HomeDirectoryType`, `HomeDirectoryMappings`, `PosixProfile` or `SshPublicKeyBody` |
| No authentication | SSH public keys, service-managed credentials and custom identity providers are not exercised |
| No endpoint address | `DescribeServer` reports no hostname, VPC endpoint or Elastic IP |
| Sequential server ids | Ids are minted as `s-00000001`, `s-00000002`, … rather than AWS's random ids |
| Other resources absent | Agreements, certificates, connectors, profiles and workflows are not emulated, and their ARNs are refused by the tag operations |

<!-- BEGIN overcast:capabilities -->

## Operations

All 13 listed operations are implemented.
Per-operation status, notes and AWS API links: [Transfer Family operations](transfer/operations.md).

<!-- END overcast:capabilities -->

## Related

- [S3](s3.md) — the storage a real Transfer server would front
- [AWS API reference](https://docs.aws.amazon.com/transfer/latest/userguide/API_Reference.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
