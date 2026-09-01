---
title: "STS operations"
description: "Every STS operation Overcast declares — 5 of 11 implemented — with status, behaviour notes and a link to the AWS API reference for each."
section: "Service Reference"
tags:
  - docs
  - operations
  - services
  - sts
---

<!-- BEGIN overcast:capabilities -->

# STS operations

5 of 11 listed operations are implemented. Back to [STS](../sts.md).

## Summary

| Category    | ✅ Supported | ❌ Unsupported |
| ----------- | ------------ | -------------- |
| General     | 5            |                |
| Unsupported |              | 6              |

---

## Endpoints

### General

| Operation                   | Status       | Notes | AWS Docs                                                                                       |
| --------------------------- | ------------ | ----- | ---------------------------------------------------------------------------------------------- |
| `AssumeRole`                | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRole.html)                |
| `AssumeRoleWithWebIdentity` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithWebIdentity.html) |
| `GetCallerIdentity`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetCallerIdentity.html)         |
| `GetFederationToken`        | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetFederationToken.html)        |
| `GetSessionToken`           | ✅ Supported |       | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetSessionToken.html)           |

### Unsupported

| Operation                    | Status         | Notes                  | AWS Docs                                                                                        |
| ---------------------------- | -------------- | ---------------------- | ----------------------------------------------------------------------------------------------- |
| `AssumeRoleWithSAML`         | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithSAML.html)         |
| `AssumeRoot`                 | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoot.html)                 |
| `DecodeAuthorizationMessage` | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_DecodeAuthorizationMessage.html) |
| `GetAccessKeyInfo`           | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetAccessKeyInfo.html)           |
| `GetDelegatedAccessToken`    | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetDelegatedAccessToken.html)    |
| `GetWebIdentityToken`        | ❌ Unsupported | Returns NotImplemented | [docs](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetWebIdentityToken.html)        |

<!-- END overcast:capabilities -->
