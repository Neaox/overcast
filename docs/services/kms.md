---
title: "KMS — Key Management Service"
description: "Real symmetric and asymmetric cryptography — AES-256-GCM encrypt/decrypt, data keys, RSA sign/verify and HMAC — over keys that live only in this emulator."
section: "Service Reference"
tags:
  - docs
  - key
  - kms
  - management
  - service
  - services
---

# KMS — Key Management Service

Encryption here is real: `Encrypt` returns AES-256-GCM ciphertext that only
`Decrypt` can read back. The keys are local and unprotected.

**Status:** ⚠️ Partial

## Quick start

```bash
export AWS_ENDPOINT_URL=http://localhost:4566

KEY=$(aws kms create-key --query KeyMetadata.KeyId --output text)
aws kms create-alias --alias-name alias/app --target-key-id "$KEY"

aws kms encrypt --key-id alias/app --plaintext 'hunter2' \
  --query CiphertextBlob --output text
```

## What works

| Area              | Behaviour                                                                                     |
| ----------------- | --------------------------------------------------------------------------------------------- |
| Key lifecycle     | `CreateKey` (symmetric and RSA specs), enable/disable, description updates, schedule/cancel deletion |
| Aliases           | Create, update, delete and list; any operation taking `KeyId` accepts a UUID, an ARN or an alias |
| Symmetric crypto  | `Encrypt`, `Decrypt`, `ReEncrypt`, `GenerateDataKey` (`AES_256`/`AES_128`), `GenerateDataKeyWithoutPlaintext`, `GenerateDataKeyPair` |
| Asymmetric crypto | `Sign` / `Verify` (RSA-2048, `RSASSA_PKCS1_V1_5_SHA_256`), `GetPublicKey` (DER), `VerifyMac` (HMAC-SHA-256/384/512) |
| Key policies      | `PutKeyPolicy` validates structure, principals and caller-lockout safety before it mutates      |
| Grants and tags   | Full CRUD, including `ListRetirableGrants`                                                     |

The ciphertext envelope carries the key ID, so `Decrypt` resolves the key
without being told which one to use — as on AWS.

## Differences from AWS

| Area                  | On AWS                                                | Overcast                                                                           |
| --------------------- | ----------------------------------------------------- | ---------------------------------------------------------------------------------- |
| `EncryptionContext`   | Bound into the ciphertext; a mismatch fails `Decrypt` | Not bound — a `Decrypt` with a different context succeeds                          |
| Key policies          | Evaluated on every call                               | Validated and stored; never evaluated                                              |
| Grants                | Constrain what a grantee may do                       | Stored and listed; never evaluated                                                 |
| `ScheduleKeyDeletion` | The key is destroyed when the window elapses          | The key goes to `PendingDeletion` and stays there; `CancelKeyDeletion` still works |
| `ListKeys`            | Paginated                                             | Returns everything except `PendingDeletion` keys, `Truncated: false`               |
| Multi-Region keys     | `MultiRegion=true` replicates                         | Rejected at `CreateKey` rather than silently ignored                               |
| External key material | `Origin=EXTERNAL` / `AWS_CLOUDHSM`                    | Rejected at `CreateKey`; only `AWS_KMS` is emulated                                |
| Automatic rotation    | `EnableKeyRotation` and friends                       | Not implemented — `501 Not Implemented`                                            |

## Gotchas

> [!CAUTION]
> Key material is stored in emulator state, not an HSM, and nothing gates
> access to it. Never put a real secret behind a local KMS key.

<!-- BEGIN overcast:capabilities -->

## Operations

All 33 listed operations are implemented.
Per-operation status, notes and AWS API links: [KMS operations](kms/operations.md).

<!-- END overcast:capabilities -->

## Related

- [Secrets Manager](./secretsmanager.md) — records `KmsKeyId`, but does not encrypt through it
- [All service pages](./README.md)
- [Service names and state overrides](../configuration.md#service-names)
- [AWS API reference](https://docs.aws.amazon.com/kms/latest/APIReference/Welcome.html)
