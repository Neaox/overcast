# CloudFront — Amazon CloudFront

> AWS docs: https://docs.aws.amazon.com/cloudfront/latest/APIReference/Welcome.html

CloudFront uses a REST API with XML request/response bodies.
All endpoints use the `/2020-05-31` path prefix.

---

## Summary

| Category      | ✅ Supported | ⚠️ Partial | 🚧 WIP | ❌ Unsupported |
| ------------- | ------------ | ---------- | ------ | -------------- |
| Distributions | 7            | 0          | 0      | 0              |
| Invalidations | 3            | 0          | 0      | 0              |
| OAC / OAI     | 11           | 0          | 0      | 0              |
| Tagging       | 3            | 0          | 0      | 0              |
| Policies      | 18           | 0          | 0      | 0              |
| Proxy         | 1            | 0          | 0      | 0              |
| Functions     | 8            | 0          | 0      | 0              |
| Keys & Crypto | 12           | 0          | 0      | 0              |
| Monitoring    | 8            | 0          | 0      | 0              |
| FLE           | 12           | 0          | 0      | 0              |
| Deployment    | 6            | 0          | 0      | 0              |

---

## Distribution operations

| Operation                  | Status | Notes                                                             |
| -------------------------- | ------ | ----------------------------------------------------------------- |
| CreateDistribution         | ✅     | CallerReference idempotency; Status always "Deployed"             |
| GetDistribution            | ✅     | Returns ETag header                                               |
| GetDistributionConfig      | ✅     | Returns DistributionConfig portion + ETag                         |
| UpdateDistribution         | ✅     | Requires If-Match ETag; bumps version                             |
| DeleteDistribution         | ✅     | Requires If-Match + Enabled=false; cascade TODO                   |
| ListDistributions          | ✅     | Marker/MaxItems pagination via serviceutil.Paginate               |
| CreateDistributionWithTags | ✅     | Creates distribution + tags atomically; `_custom_id_` tag support |

## Invalidation operations

| Operation          | Status | Notes                                                   |
| ------------------ | ------ | ------------------------------------------------------- |
| CreateInvalidation | ✅     | Supports path and tag (#tag) invalidations; Status instantly "Completed" |
| GetInvalidation    | ✅     | Returns invalidation by distribution + invalidation ID  |
| ListInvalidations  | ✅     | Marker/MaxItems pagination                              |

## Origin Access Control operations

| Operation                 | Status | Notes                                 |
| ------------------------- | ------ | ------------------------------------- |
| CreateOriginAccessControl | ✅     | Generates ID, returns ETag            |
| GetOriginAccessControl    | ✅     | Returns OAC by ID with ETag           |
| UpdateOriginAccessControl | ✅     | Requires If-Match ETag; bumps version |
| DeleteOriginAccessControl | ✅     | Requires If-Match ETag                |
| ListOriginAccessControls  | ✅     | Marker/MaxItems pagination            |

## Origin Access Identity (legacy) operations

| Operation                               | Status | Notes                                                 |
| --------------------------------------- | ------ | ----------------------------------------------------- |
| CreateCloudFrontOriginAccessIdentity    | ✅     | CallerReference required; generates S3CanonicalUserId |
| GetCloudFrontOriginAccessIdentity       | ✅     | Returns OAI by ID with ETag                           |
| GetCloudFrontOriginAccessIdentityConfig | ✅     | Returns config portion + ETag                         |
| UpdateCloudFrontOriginAccessIdentity    | ✅     | Requires If-Match ETag; bumps version                 |
| DeleteCloudFrontOriginAccessIdentity    | ✅     | Requires If-Match ETag                                |
| ListCloudFrontOriginAccessIdentities    | ✅     | Marker/MaxItems pagination                            |

## Tagging operations

| Operation           | Status | Notes                         |
| ------------------- | ------ | ----------------------------- |
| ListTagsForResource | ✅     | Returns tags by resource ARN  |
| TagResource         | ✅     | Merges tags into existing set |
| UntagResource       | ✅     | Removes specified tag keys    |

## Cache Policy operations

| Operation            | Status | Notes                                 |
| -------------------- | ------ | ------------------------------------- |
| CreateCachePolicy    | ✅     | Generates ID, returns ETag            |
| GetCachePolicy       | ✅     | Returns policy by ID with ETag        |
| GetCachePolicyConfig | ✅     | Returns config portion + ETag         |
| UpdateCachePolicy    | ✅     | Requires If-Match ETag; bumps version |
| DeleteCachePolicy    | ✅     | Requires If-Match ETag                |
| ListCachePolicies    | ✅     | Marker/MaxItems pagination            |

## Origin Request Policy operations

| Operation                    | Status | Notes                                 |
| ---------------------------- | ------ | ------------------------------------- |
| CreateOriginRequestPolicy    | ✅     | Generates ID, returns ETag            |
| GetOriginRequestPolicy       | ✅     | Returns policy by ID with ETag        |
| GetOriginRequestPolicyConfig | ✅     | Returns config portion + ETag         |
| UpdateOriginRequestPolicy    | ✅     | Requires If-Match ETag; bumps version |
| DeleteOriginRequestPolicy    | ✅     | Requires If-Match ETag                |
| ListOriginRequestPolicies    | ✅     | Marker/MaxItems pagination            |

## Response Headers Policy operations

| Operation                      | Status | Notes                                 |
| ------------------------------ | ------ | ------------------------------------- |
| CreateResponseHeadersPolicy    | ✅     | Generates ID, returns ETag            |
| GetResponseHeadersPolicy       | ✅     | Returns policy by ID with ETag        |
| GetResponseHeadersPolicyConfig | ✅     | Returns config portion + ETag         |
| UpdateResponseHeadersPolicy    | ✅     | Requires If-Match ETag; bumps version |
| DeleteResponseHeadersPolicy    | ✅     | Requires If-Match ETag                |
| ListResponseHeadersPolicies    | ✅     | Marker/MaxItems pagination            |

## Origin Proxy (emulator extension)

| Operation    | Status | Notes                                                                         |
| ------------ | ------ | ----------------------------------------------------------------------------- |
| ProxyRequest | ✅     | `/_cloudfront/{distId}/*` — forwards to configured origins with path matching |

The origin proxy is an emulator-only extension (not part of the real CloudFront API). It forwards HTTP requests through a distribution's configured origins:

- S3 origins are rewritten to the local emulator endpoint
- Custom origins are forwarded to their configured domain
- DefaultRootObject is applied for `/` requests
- CacheBehavior path patterns are matched to select the correct origin
- CloudFront response headers (X-Amz-Cf-Pop, X-Amz-Cf-Id, Via, X-Cache) are added

## CloudFront Functions operations

| Operation        | Status | Notes                                                 |
| ---------------- | ------ | ----------------------------------------------------- |
| CreateFunction   | ✅     | Stores code + config; Stage=DEVELOPMENT; returns ETag |
| DescribeFunction | ✅     | Returns FunctionSummary with metadata                 |
| GetFunction      | ✅     | Returns raw function code (base64) with ETag          |
| UpdateFunction   | ✅     | Requires If-Match ETag; bumps version                 |
| DeleteFunction   | ✅     | Requires If-Match ETag                                |
| ListFunctions    | ✅     | Filters by Stage query param; MaxItems pagination     |
| TestFunction     | ✅     | Returns mock success result (no JS execution)         |
| PublishFunction  | ✅     | Promotes DEVELOPMENT → LIVE stage                     |

## Key Group & Public Key operations

| Operation          | Status | Notes                                      |
| ------------------ | ------ | ------------------------------------------ |
| CreateKeyGroup     | ✅     | Generates ID, returns ETag                 |
| GetKeyGroup        | ✅     | Returns key group by ID with ETag          |
| GetKeyGroupConfig  | ✅     | Returns config portion + ETag              |
| UpdateKeyGroup     | ✅     | Requires If-Match ETag; bumps version      |
| DeleteKeyGroup     | ✅     | Requires If-Match ETag                     |
| ListKeyGroups      | ✅     | MaxItems pagination                        |
| CreatePublicKey    | ✅     | CallerReference dedup; generates ID + ETag |
| GetPublicKey       | ✅     | Returns public key by ID with ETag         |
| GetPublicKeyConfig | ✅     | Returns config portion + ETag              |
| UpdatePublicKey    | ✅     | Requires If-Match ETag; bumps version      |
| DeletePublicKey    | ✅     | Requires If-Match ETag                     |
| ListPublicKeys     | ✅     | MaxItems pagination                        |

## Monitoring & Realtime operations

| Operation                    | Status | Notes                                            |
| ---------------------------- | ------ | ------------------------------------------------ |
| CreateMonitoringSubscription | ✅     | Per-distribution; requires existing distribution |
| GetMonitoringSubscription    | ✅     | Returns subscription by distribution ID          |
| DeleteMonitoringSubscription | ✅     | Removes subscription for distribution            |
| CreateRealtimeLogConfig      | ✅     | Name-based; generates ARN; duplicate name check  |
| GetRealtimeLogConfig         | ✅     | Lookup by Name or ARN in request body            |
| UpdateRealtimeLogConfig      | ✅     | Updates by Name in request body                  |
| DeleteRealtimeLogConfig      | ✅     | Deletes by Name or ARN in request body           |
| ListRealtimeLogConfigs       | ✅     | MaxItems pagination                              |

## Field-Level Encryption operations

| Operation                            | Status | Notes                                  |
| ------------------------------------ | ------ | -------------------------------------- |
| CreateFieldLevelEncryptionConfig     | ✅     | CallerReference required; generates ID |
| GetFieldLevelEncryption              | ✅     | Returns FLE config by ID with ETag     |
| GetFieldLevelEncryptionConfig        | ✅     | Returns config portion + ETag          |
| UpdateFieldLevelEncryptionConfig     | ✅     | Requires If-Match ETag; bumps version  |
| DeleteFieldLevelEncryption           | ✅     | Requires If-Match ETag                 |
| ListFieldLevelEncryptionConfigs      | ✅     | MaxItems pagination                    |
| CreateFieldLevelEncryptionProfile    | ✅     | CallerReference required; generates ID |
| GetFieldLevelEncryptionProfile       | ✅     | Returns FLE profile by ID with ETag    |
| GetFieldLevelEncryptionProfileConfig | ✅     | Returns config portion + ETag          |
| UpdateFieldLevelEncryptionProfile    | ✅     | Requires If-Match ETag; bumps version  |
| DeleteFieldLevelEncryptionProfile    | ✅     | Requires If-Match ETag                 |
| ListFieldLevelEncryptionProfiles     | ✅     | MaxItems pagination                    |

## Continuous Deployment Policy operations

| Operation                           | Status | Notes                                 |
| ----------------------------------- | ------ | ------------------------------------- |
| CreateContinuousDeploymentPolicy    | ✅     | Generates ID, returns ETag            |
| GetContinuousDeploymentPolicy       | ✅     | Returns policy by ID with ETag        |
| GetContinuousDeploymentPolicyConfig | ✅     | Returns config portion + ETag         |
| UpdateContinuousDeploymentPolicy    | ✅     | Requires If-Match ETag; bumps version |
| DeleteContinuousDeploymentPolicy    | ✅     | Requires If-Match ETag                |
| ListContinuousDeploymentPolicies    | ✅     | MaxItems pagination                   |

---

## Notes

- Error responses use the XML format matching the real CloudFront API.
- Distributions set `Status: "Deployed"` immediately — no async provisioning delay.
- DomainName is synthetic: `{id}.cloudfront.net` (not routable).
- ETag is a quoted version counter (`"1"`, `"2"`, etc.) — not a hash.
- CallerReference idempotency: same ref + identical config returns existing distribution.
- Delete requires `Enabled: false` + matching ETag (`If-Match` header).
- Tag-based invalidation: paths prefixed with `#` (e.g. `#product:electronics`) invalidate cached objects by cache tag. Tags are parsed from the origin response header specified in `CacheTagConfig.HeaderName` and must be ASCII visible characters (33-126), max 256 chars, no spaces/commas. Path and tag invalidations can be mixed in a single batch.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category      | ✅ Supported |
| ------------- | ------------ |
| Distributions | 7            |
| Invalidations | 3            |
| OAC / OAI     | 11           |
| Tagging       | 3            |
| Policies      | 18           |
| Proxy         | 1            |
| Functions     | 8            |
| Keys & Crypto | 12           |
| Monitoring    | 8            |
| FLE           | 12           |
| Deployment    | 6            |

---

## Endpoints

### Distributions

| Operation                    | Status       | Notes                                                           | AWS Docs                                                                                               |
| ---------------------------- | ------------ | --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| `CreateDistribution`         | ✅ Supported | CallerReference idempotency; Status always "Deployed"           | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateDistribution.html)         |
| `GetDistribution`            | ✅ Supported | Returns ETag header                                             | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetDistribution.html)            |
| `GetDistributionConfig`      | ✅ Supported | Returns DistributionConfig portion + ETag                       | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetDistributionConfig.html)      |
| `UpdateDistribution`         | ✅ Supported | Requires If-Match ETag; bumps version                           | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateDistribution.html)         |
| `DeleteDistribution`         | ✅ Supported | Requires If-Match + Enabled=false                               | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteDistribution.html)         |
| `ListDistributions`          | ✅ Supported | Marker/MaxItems pagination via serviceutil.Paginate             | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListDistributions.html)          |
| `CreateDistributionWithTags` | ✅ Supported | Creates distribution + tags atomically; _custom_id_ tag support | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateDistributionWithTags.html) |

### Invalidations

| Operation            | Status       | Notes                                                                    | AWS Docs                                                                                       |
| -------------------- | ------------ | ------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `CreateInvalidation` | ✅ Supported | Supports path and tag invalidations (#tag); Status instantly "Completed" | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateInvalidation.html) |
| `GetInvalidation`    | ✅ Supported | Returns invalidation by distribution + invalidation ID                   | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetInvalidation.html)    |
| `ListInvalidations`  | ✅ Supported | Marker/MaxItems pagination                                               | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListInvalidations.html)  |

### OAC / OAI

| Operation                                 | Status       | Notes                                                 | AWS Docs                                                                                                            |
| ----------------------------------------- | ------------ | ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `CreateOriginAccessControl`               | ✅ Supported | Generates ID, returns ETag                            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateOriginAccessControl.html)               |
| `GetOriginAccessControl`                  | ✅ Supported | Returns OAC by ID with ETag                           | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetOriginAccessControl.html)                  |
| `UpdateOriginAccessControl`               | ✅ Supported | Requires If-Match ETag; bumps version                 | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateOriginAccessControl.html)               |
| `DeleteOriginAccessControl`               | ✅ Supported | Requires If-Match ETag                                | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteOriginAccessControl.html)               |
| `ListOriginAccessControls`                | ✅ Supported | Marker/MaxItems pagination                            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListOriginAccessControls.html)                |
| `CreateCloudFrontOriginAccessIdentity`    | ✅ Supported | CallerReference required; generates S3CanonicalUserId | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateCloudFrontOriginAccessIdentity.html)    |
| `GetCloudFrontOriginAccessIdentity`       | ✅ Supported | Returns OAI by ID with ETag                           | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetCloudFrontOriginAccessIdentity.html)       |
| `GetCloudFrontOriginAccessIdentityConfig` | ✅ Supported | Returns config portion + ETag                         | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetCloudFrontOriginAccessIdentityConfig.html) |
| `UpdateCloudFrontOriginAccessIdentity`    | ✅ Supported | Requires If-Match ETag; bumps version                 | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateCloudFrontOriginAccessIdentity.html)    |
| `DeleteCloudFrontOriginAccessIdentity`    | ✅ Supported | Requires If-Match ETag                                | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteCloudFrontOriginAccessIdentity.html)    |
| `ListCloudFrontOriginAccessIdentities`    | ✅ Supported | Marker/MaxItems pagination                            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListCloudFrontOriginAccessIdentities.html)    |

### Tagging

| Operation             | Status       | Notes                         | AWS Docs                                                                                        |
| --------------------- | ------------ | ----------------------------- | ----------------------------------------------------------------------------------------------- |
| `ListTagsForResource` | ✅ Supported | Returns tags by resource ARN  | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListTagsForResource.html) |
| `TagResource`         | ✅ Supported | Merges tags into existing set | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_TagResource.html)         |
| `UntagResource`       | ✅ Supported | Removes specified tag keys    | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UntagResource.html)       |

### Policies

| Operation                        | Status       | Notes                                 | AWS Docs                                                                                                   |
| -------------------------------- | ------------ | ------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `CreateCachePolicy`              | ✅ Supported | Generates ID, returns ETag            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateCachePolicy.html)              |
| `GetCachePolicy`                 | ✅ Supported | Returns policy by ID with ETag        | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetCachePolicy.html)                 |
| `GetCachePolicyConfig`           | ✅ Supported | Returns config portion + ETag         | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetCachePolicyConfig.html)           |
| `UpdateCachePolicy`              | ✅ Supported | Requires If-Match ETag; bumps version | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateCachePolicy.html)              |
| `DeleteCachePolicy`              | ✅ Supported | Requires If-Match ETag                | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteCachePolicy.html)              |
| `ListCachePolicies`              | ✅ Supported | Marker/MaxItems pagination            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListCachePolicies.html)              |
| `CreateOriginRequestPolicy`      | ✅ Supported | Generates ID, returns ETag            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateOriginRequestPolicy.html)      |
| `GetOriginRequestPolicy`         | ✅ Supported | Returns policy by ID with ETag        | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetOriginRequestPolicy.html)         |
| `GetOriginRequestPolicyConfig`   | ✅ Supported | Returns config portion + ETag         | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetOriginRequestPolicyConfig.html)   |
| `UpdateOriginRequestPolicy`      | ✅ Supported | Requires If-Match ETag; bumps version | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateOriginRequestPolicy.html)      |
| `DeleteOriginRequestPolicy`      | ✅ Supported | Requires If-Match ETag                | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteOriginRequestPolicy.html)      |
| `ListOriginRequestPolicies`      | ✅ Supported | Marker/MaxItems pagination            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListOriginRequestPolicies.html)      |
| `CreateResponseHeadersPolicy`    | ✅ Supported | Generates ID, returns ETag            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateResponseHeadersPolicy.html)    |
| `GetResponseHeadersPolicy`       | ✅ Supported | Returns policy by ID with ETag        | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetResponseHeadersPolicy.html)       |
| `GetResponseHeadersPolicyConfig` | ✅ Supported | Returns config portion + ETag         | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetResponseHeadersPolicyConfig.html) |
| `UpdateResponseHeadersPolicy`    | ✅ Supported | Requires If-Match ETag; bumps version | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateResponseHeadersPolicy.html)    |
| `DeleteResponseHeadersPolicy`    | ✅ Supported | Requires If-Match ETag                | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteResponseHeadersPolicy.html)    |
| `ListResponseHeadersPolicies`    | ✅ Supported | Marker/MaxItems pagination            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListResponseHeadersPolicies.html)    |

### Proxy

| Operation      | Status       | Notes                                                                 | AWS Docs                                                                                 |
| -------------- | ------------ | --------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `ProxyRequest` | ✅ Supported | Emulator extension: forwards to configured origins with path matching | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ProxyRequest.html) |

### Functions

| Operation          | Status       | Notes                                                 | AWS Docs                                                                                     |
| ------------------ | ------------ | ----------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `CreateFunction`   | ✅ Supported | Stores code + config; Stage=DEVELOPMENT; returns ETag | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateFunction.html)   |
| `DescribeFunction` | ✅ Supported | Returns FunctionSummary with metadata                 | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DescribeFunction.html) |
| `GetFunction`      | ✅ Supported | Returns raw function code (base64) with ETag          | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetFunction.html)      |
| `UpdateFunction`   | ✅ Supported | Requires If-Match ETag; bumps version                 | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateFunction.html)   |
| `DeleteFunction`   | ✅ Supported | Requires If-Match ETag                                | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteFunction.html)   |
| `ListFunctions`    | ✅ Supported | Filters by Stage query param; MaxItems pagination     | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListFunctions.html)    |
| `TestFunction`     | ✅ Supported | Returns mock success result (no JS execution)         | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_TestFunction.html)     |
| `PublishFunction`  | ✅ Supported | Promotes DEVELOPMENT → LIVE stage                     | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_PublishFunction.html)  |

### Keys & Crypto

| Operation            | Status       | Notes                                      | AWS Docs                                                                                       |
| -------------------- | ------------ | ------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `CreateKeyGroup`     | ✅ Supported | Generates ID, returns ETag                 | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateKeyGroup.html)     |
| `GetKeyGroup`        | ✅ Supported | Returns key group by ID with ETag          | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetKeyGroup.html)        |
| `GetKeyGroupConfig`  | ✅ Supported | Returns config portion + ETag              | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetKeyGroupConfig.html)  |
| `UpdateKeyGroup`     | ✅ Supported | Requires If-Match ETag; bumps version      | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateKeyGroup.html)     |
| `DeleteKeyGroup`     | ✅ Supported | Requires If-Match ETag                     | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteKeyGroup.html)     |
| `ListKeyGroups`      | ✅ Supported | MaxItems pagination                        | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListKeyGroups.html)      |
| `CreatePublicKey`    | ✅ Supported | CallerReference dedup; generates ID + ETag | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreatePublicKey.html)    |
| `GetPublicKey`       | ✅ Supported | Returns public key by ID with ETag         | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetPublicKey.html)       |
| `GetPublicKeyConfig` | ✅ Supported | Returns config portion + ETag              | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetPublicKeyConfig.html) |
| `UpdatePublicKey`    | ✅ Supported | Requires If-Match ETag; bumps version      | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdatePublicKey.html)    |
| `DeletePublicKey`    | ✅ Supported | Requires If-Match ETag                     | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeletePublicKey.html)    |
| `ListPublicKeys`     | ✅ Supported | MaxItems pagination                        | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListPublicKeys.html)     |

### Monitoring

| Operation                      | Status       | Notes                                            | AWS Docs                                                                                                 |
| ------------------------------ | ------------ | ------------------------------------------------ | -------------------------------------------------------------------------------------------------------- |
| `CreateMonitoringSubscription` | ✅ Supported | Per-distribution; requires existing distribution | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateMonitoringSubscription.html) |
| `GetMonitoringSubscription`    | ✅ Supported | Returns subscription by distribution ID          | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetMonitoringSubscription.html)    |
| `DeleteMonitoringSubscription` | ✅ Supported | Removes subscription for distribution            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteMonitoringSubscription.html) |
| `CreateRealtimeLogConfig`      | ✅ Supported | Name-based; generates ARN; duplicate name check  | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateRealtimeLogConfig.html)      |
| `GetRealtimeLogConfig`         | ✅ Supported | Lookup by Name or ARN in request body            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetRealtimeLogConfig.html)         |
| `UpdateRealtimeLogConfig`      | ✅ Supported | Updates by Name in request body                  | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateRealtimeLogConfig.html)      |
| `DeleteRealtimeLogConfig`      | ✅ Supported | Deletes by Name or ARN in request body           | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteRealtimeLogConfig.html)      |
| `ListRealtimeLogConfigs`       | ✅ Supported | MaxItems pagination                              | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListRealtimeLogConfigs.html)       |

### FLE

| Operation                              | Status       | Notes                                  | AWS Docs                                                                                                         |
| -------------------------------------- | ------------ | -------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `CreateFieldLevelEncryptionConfig`     | ✅ Supported | CallerReference required; generates ID | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateFieldLevelEncryptionConfig.html)     |
| `GetFieldLevelEncryption`              | ✅ Supported | Returns FLE config by ID with ETag     | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetFieldLevelEncryption.html)              |
| `GetFieldLevelEncryptionConfig`        | ✅ Supported | Returns config portion + ETag          | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetFieldLevelEncryptionConfig.html)        |
| `UpdateFieldLevelEncryptionConfig`     | ✅ Supported | Requires If-Match ETag; bumps version  | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateFieldLevelEncryptionConfig.html)     |
| `DeleteFieldLevelEncryption`           | ✅ Supported | Requires If-Match ETag                 | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteFieldLevelEncryption.html)           |
| `ListFieldLevelEncryptionConfigs`      | ✅ Supported | MaxItems pagination                    | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListFieldLevelEncryptionConfigs.html)      |
| `CreateFieldLevelEncryptionProfile`    | ✅ Supported | CallerReference required; generates ID | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateFieldLevelEncryptionProfile.html)    |
| `GetFieldLevelEncryptionProfile`       | ✅ Supported | Returns FLE profile by ID with ETag    | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetFieldLevelEncryptionProfile.html)       |
| `GetFieldLevelEncryptionProfileConfig` | ✅ Supported | Returns config portion + ETag          | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetFieldLevelEncryptionProfileConfig.html) |
| `UpdateFieldLevelEncryptionProfile`    | ✅ Supported | Requires If-Match ETag; bumps version  | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateFieldLevelEncryptionProfile.html)    |
| `DeleteFieldLevelEncryptionProfile`    | ✅ Supported | Requires If-Match ETag                 | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteFieldLevelEncryptionProfile.html)    |
| `ListFieldLevelEncryptionProfiles`     | ✅ Supported | MaxItems pagination                    | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListFieldLevelEncryptionProfiles.html)     |

### Deployment

| Operation                             | Status       | Notes                                 | AWS Docs                                                                                                        |
| ------------------------------------- | ------------ | ------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `CreateContinuousDeploymentPolicy`    | ✅ Supported | Generates ID, returns ETag            | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_CreateContinuousDeploymentPolicy.html)    |
| `GetContinuousDeploymentPolicy`       | ✅ Supported | Returns policy by ID with ETag        | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetContinuousDeploymentPolicy.html)       |
| `GetContinuousDeploymentPolicyConfig` | ✅ Supported | Returns config portion + ETag         | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_GetContinuousDeploymentPolicyConfig.html) |
| `UpdateContinuousDeploymentPolicy`    | ✅ Supported | Requires If-Match ETag; bumps version | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_UpdateContinuousDeploymentPolicy.html)    |
| `DeleteContinuousDeploymentPolicy`    | ✅ Supported | Requires If-Match ETag                | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_DeleteContinuousDeploymentPolicy.html)    |
| `ListContinuousDeploymentPolicies`    | ✅ Supported | MaxItems pagination                   | [docs](https://docs.aws.amazon.com/cloudfront/latest/APIReference/API_ListContinuousDeploymentPolicies.html)    |

<!-- END overcast:capabilities -->
