---
title: "Transfer Family — AWS Transfer Family"
description: "Metadata-only AWS Transfer Family implementation."
section: "Service Reference"
tags:
  - aws
  - docs
  - family
  - services
  - transfer
---

# Transfer Family — AWS Transfer Family

> AWS docs: https://docs.aws.amazon.com/transfer/latest/userguide/API_Reference.html

Metadata-only AWS Transfer Family implementation.

## What's covered

Supports Transfer Family server and user control-plane CRUD. For the
operation count, see [Summary](#summary) at the bottom of this page.

## Behavior Notes

- No real SFTP/FTPS/FTP daemon is launched.
- Endpoint/server state is metadata only.
- User resources are stored and listed but no data-plane authentication occurs.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category   | 🧊 Inert |
| ---------- | -------- |
| Operations | 13       |

---

## Endpoints

### Operations

| Operation             | Status   | Notes                                                                      | AWS Docs                                                                                   |
| --------------------- | -------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| `CreateServer`        | 🧊 Inert | Inline `Tags` applied at creation                                          | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_CreateServer.html)        |
| `DescribeServer`      | 🧊 Inert |                                                                            | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_DescribeServer.html)      |
| `ListServers`         | 🧊 Inert |                                                                            | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_ListServers.html)         |
| `UpdateServer`        | 🧊 Inert |                                                                            | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_UpdateServer.html)        |
| `DeleteServer`        | 🧊 Inert |                                                                            | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_DeleteServer.html)        |
| `CreateUser`          | 🧊 Inert | Inline `Tags` applied at creation                                          | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_CreateUser.html)          |
| `DescribeUser`        | 🧊 Inert |                                                                            | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_DescribeUser.html)        |
| `ListUsers`           | 🧊 Inert |                                                                            | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_ListUsers.html)           |
| `UpdateUser`          | 🧊 Inert |                                                                            | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_UpdateUser.html)          |
| `DeleteUser`          | 🧊 Inert |                                                                            | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_DeleteUser.html)          |
| `TagResource`         | 🧊 Inert | Server and user ARNs; Transfer's other taggable resources are not emulated | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_TagResource.html)         |
| `UntagResource`       | 🧊 Inert |                                                                            | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_UntagResource.html)       |
| `ListTagsForResource` | 🧊 Inert | No pagination; every tag is returned in one page                           | [docs](https://docs.aws.amazon.com/transfer/latest/userguide/API_ListTagsForResource.html) |

<!-- END overcast:capabilities -->
