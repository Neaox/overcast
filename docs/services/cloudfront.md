---
title: "CloudFront — Amazon CloudFront"
description: "CloudFront uses a REST API with XML request/response bodies. All endpoints use the /2020-05-31 path prefix."
section: "Service Reference"
tags:
  - amazon
  - cloudfront
  - docs
  - services
---

# CloudFront — Amazon CloudFront

CloudFront uses a REST API with XML request/response bodies.
All endpoints use the `/2020-05-31` path prefix.

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
| ProxyRequest | ✅     | `/_overcast/cloudfront/distributions/{distId}/*` — forwards to configured origins with path matching |

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

## Operations

All 89 listed operations are implemented.
Per-operation status, notes and AWS API links: [CloudFront operations](cloudfront/operations.md).

<!-- END overcast:capabilities -->

## Related

- [AWS API reference](https://docs.aws.amazon.com/cloudfront/latest/APIReference/Welcome.html)
- [All service pages](README.md)
- [Service names and state overrides](../configuration.md#service-names)
