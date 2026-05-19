# KMS — Key Management Service

> AWS docs: https://docs.aws.amazon.com/kms/latest/APIReference/Welcome.html

KMS accepts AWS JSON 1.1 requests at `POST /` with `X-Amz-Target:
TrentService.<OperationName>` and Smithy RPC v2 CBOR requests at
`/service/kms/operation/<OperationName>` with `Smithy-Protocol: rpc-v2-cbor`.

---

<!-- BEGIN overcast:capabilities -->

## Summary

| Category          | ✅ Supported |
| ----------------- | ------------ |
| Key lifecycle     | 7            |
| Aliases           | 4            |
| Symmetric crypto  | 6            |
| Asymmetric crypto | 4            |
| Tags              | 3            |
| Key policies      | 3            |
| Grants            | 5            |

---

## Endpoints

### Key lifecycle

| Operation             | Status       | Notes                                                                | AWS Docs                                                                                 |
| --------------------- | ------------ | -------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `CreateKey`           | ✅ Supported | `SYMMETRIC_DEFAULT` (AES-256-GCM) and `RSA_2048` key specs supported | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateKey.html)           |
| `DescribeKey`         | ✅ Supported | Lookup by UUID, ARN, or alias                                        | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_DescribeKey.html)         |
| `ListKeys`            | ✅ Supported | Excludes `PendingDeletion` keys; no pagination (Truncated=false)     | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_ListKeys.html)            |
| `EnableKey`           | ✅ Supported |                                                                      | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_EnableKey.html)           |
| `DisableKey`          | ✅ Supported |                                                                      | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_DisableKey.html)          |
| `ScheduleKeyDeletion` | ✅ Supported | `PendingWindowInDays` honoured; defaults to 30 days                  | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html) |
| `CancelKeyDeletion`   | ✅ Supported | Restores key to `Disabled` state                                     | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_CancelKeyDeletion.html)   |

### Aliases

| Operation     | Status       | Notes                                      | AWS Docs                                                                         |
| ------------- | ------------ | ------------------------------------------ | -------------------------------------------------------------------------------- |
| `CreateAlias` | ✅ Supported | `alias/` prefix required                   | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateAlias.html) |
| `DeleteAlias` | ✅ Supported |                                            | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_DeleteAlias.html) |
| `ListAliases` | ✅ Supported | Optional `KeyId` filter (UUID, ARN, alias) | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_ListAliases.html) |
| `UpdateAlias` | ✅ Supported | Updates target key for an existing alias   | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_UpdateAlias.html) |

### Symmetric crypto

| Operation                         | Status       | Notes                                                        | AWS Docs                                                                                             |
| --------------------------------- | ------------ | ------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------- |
| `Encrypt`                         | ✅ Supported | AES-256-GCM; ciphertext envelope includes key ID             | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_Encrypt.html)                         |
| `Decrypt`                         | ✅ Supported | Extracts key ID from ciphertext envelope                     | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_Decrypt.html)                         |
| `GenerateDataKey`                 | ✅ Supported | `AES_256` and `AES_128` specs; returns plaintext + encrypted | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKey.html)                 |
| `GenerateDataKeyWithoutPlaintext` | ✅ Supported | Returns encrypted data key only                              | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKeyWithoutPlaintext.html) |
| `ReEncrypt`                       | ✅ Supported | Decrypts and re-encrypts ciphertext with destination key     | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_ReEncrypt.html)                       |
| `GenerateDataKeyPair`             | ✅ Supported | RSA_2048, RSA_3072, RSA_4096 key pair specs                  | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateDataKeyPair.html)             |

### Asymmetric crypto

| Operation      | Status       | Notes                                       | AWS Docs                                                                          |
| -------------- | ------------ | ------------------------------------------- | --------------------------------------------------------------------------------- |
| `Sign`         | ✅ Supported | RSA_2048 with RSASSA_PKCS1_V1_5_SHA_256     | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_Sign.html)         |
| `Verify`       | ✅ Supported | Returns `SignatureValid: true/false`        | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_Verify.html)       |
| `GetPublicKey` | ✅ Supported | Returns DER-encoded public key for RSA keys | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_GetPublicKey.html) |
| `VerifyMac`    | ✅ Supported | HMAC_SHA_256, HMAC_SHA_384, HMAC_SHA_512    | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_VerifyMac.html)    |

### Tags

| Operation          | Status       | Notes                      | AWS Docs                                                                              |
| ------------------ | ------------ | -------------------------- | ------------------------------------------------------------------------------------- |
| `TagResource`      | ✅ Supported | Add tags to a KMS key      | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_TagResource.html)      |
| `UntagResource`    | ✅ Supported | Remove tags from a KMS key | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_UntagResource.html)    |
| `ListResourceTags` | ✅ Supported | List tags for a KMS key    | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_ListResourceTags.html) |

### Key policies

| Operation         | Status       | Notes                                | AWS Docs                                                                             |
| ----------------- | ------------ | ------------------------------------ | ------------------------------------------------------------------------------------ |
| `GetKeyPolicy`    | ✅ Supported | Returns default or custom key policy | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_GetKeyPolicy.html)    |
| `PutKeyPolicy`    | ✅ Supported | Attaches a key policy document       | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_PutKeyPolicy.html)    |
| `ListKeyPolicies` | ✅ Supported | Returns list of policy names         | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_ListKeyPolicies.html) |

### Grants

| Operation             | Status       | Notes                                                                   | AWS Docs                                                                                 |
| --------------------- | ------------ | ----------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `CreateGrant`         | ✅ Supported | Creates a grant with optional constraints and retiring principal        | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateGrant.html)         |
| `ListGrants`          | ✅ Supported | Lists grants with optional KeyId, GrantId, and GranteePrincipal filters | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_ListGrants.html)          |
| `RevokeGrant`         | ✅ Supported | Revokes a grant by ID                                                   | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_RevokeGrant.html)         |
| `RetireGrant`         | ✅ Supported | Retires a grant by ID or token                                          | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_RetireGrant.html)         |
| `ListRetirableGrants` | ✅ Supported | Lists grants retirable by a principal                                   | [docs](https://docs.aws.amazon.com/kms/latest/APIReference/API_ListRetirableGrants.html) |

<!-- END overcast:capabilities -->
