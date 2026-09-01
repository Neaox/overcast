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

Metadata-only AWS Transfer Family implementation.

## What works
Supports Transfer Family server and user control-plane CRUD.

## Behavior Notes

- No real SFTP/FTPS/FTP daemon is launched.
- Endpoint/server state is metadata only.
- User resources are stored and listed but no data-plane authentication occurs.

<!-- BEGIN overcast:capabilities -->

## Operations

All 13 listed operations are implemented.
Per-operation status, notes and AWS API links: [Transfer Family operations](transfer/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/transfer/latest/userguide/API_Reference.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
