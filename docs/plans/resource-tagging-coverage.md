# Resource Tagging Coverage Audit

> Status: audit complete; gap-fill in progress on `feat/tagging-coverage-audit`.
>
> Goal: **every resource at service tier `inert` or above that is taggable in real
> AWS must be taggable in Overcast.**

## Method

1. Scope is every service at tier `inert`, `partial` or `full` in
   [internal/router/tiers.go](../../internal/router/tiers.go) — 45 services.
2. The authority for "what AWS offers" is the pinned model manifest,
   `internal/awsapi/manifest.gen.go` (revision in `models/aws/VERSION`), read
   through the `serviceAliases` table in `internal/awsapi/registry_data.go` to
   map a model service id onto an Overcast service name.
3. The authority for "what Overcast offers" is the **handler and dispatch code**,
   not `docs/services/*.md` and not the capability declarations. Both of those
   have been found to overstate reality — issue #794 is a case where the docs say
   supported, the capability is declared, the handler exists, and the operation is
   still unreachable because it is missing from one of two protocol dispatch
   tables. Capability declarations were used only to *shortlist*; every gap below
   was confirmed by reading the service package.

### Two axes, and why both count

AWS treats tag-on-create and tag-after-create as separate, separately
IAM-authorized behaviours (`aws:RequestTag` / `aws:TagKeys` on the create call
vs. the `Tag*` action). A resource that can only be tagged after creation is
not fully taggable. The audit therefore reports:

- **Axis A — tagging operations.** Dedicated `TagResource`/`UntagResource`/
  `ListTagsForResource`, or the service-specific spelling
  (`AddTagsToResource`, `CreateTags`, `TagQueue`, `TagLogGroup`, `AddTags`, …).
- **Axis B — tag-on-create.** A `Tags` (or `TagList`/`TagSpecifications`/
  `BackupVaultTags`/`UserPoolTags`) member on the create call that AWS applies at
  creation time.

### Known limits of this audit

The pinned manifest carries operation **names**, protocols and HTTP bindings —
not member shapes. Axis A is therefore machine-derivable and exhaustive across
all 45 services. Axis B is not: it was established by reading each service's
create-request structs and comparing against the AWS API reference for the
resources that service actually implements. Axis B is complete for the services
listed below but should not be read as proof that no other service has a
tag-on-create gap in a create operation it does not yet implement.

Name-matching on `tag` produces false positives that are **not** gaps and are
excluded throughout: anything containing "S**tag**e" or "S**tag**ing"
(`CreateStage`, `FlushStageCache`, `UpdateSecretVersionStage`,
`UpdateDistributionWithStagingConfig`), "Li**stAg**greements"/
"Li**stAg**gregate…", EC2's Capacity Manager tag-key configuration
(`GetCapacityManagerMonitoredTagKeys`), and ECR's `PutImageTagMutability`, which
is about container image tags, not resource tags.

## Gap table

Tier is the Overcast service tier. "Status" is Overcast's, verified in code.

### Axis A — missing tagging operations

| Service | Tier | Resource | AWS mechanism | Overcast status |
| --- | --- | --- | --- | --- |
| `logs` | partial | Log group, log stream, destination | `TagResource` / `UntagResource` / `ListTagsForResource`, plus the deprecated `TagLogGroup` / `UntagLogGroup` / `ListTagsLogGroup` | **missing** — no tag operation of any spelling; log groups cannot be tagged at all |
| `kinesis` | partial | Stream | `TagResource` / `UntagResource` / `ListTagsForResource` (ARN-based, alongside the older stream-name ops) | **partial** — `AddTagsToStream` / `ListTagsForStream` / `RemoveTagsFromStream` present; the three ARN-based operations missing |
| `ses` | partial | Email identity, configuration set | SESv2 `TagResource` / `UntagResource` / `ListTagsForResource` (`POST`/`DELETE`/`GET /v2/email/tags`) | **missing** — identities cannot be tagged at all |
| `acm` | inert | Certificate | `TagResource` / `UntagResource` / `ListTagsForResource` (modern aliases of `AddTagsToCertificate` / `RemoveTagsFromCertificate` / `ListTagsForCertificate`) | **partial** — only the `*Certificate` spellings exist |
| `backup` | inert | Backup vault, backup plan | `TagResource` / `UntagResource` / `ListTags` | **missing** |
| `cloudtrail` | inert | Trail | `AddTags` / `RemoveTags` / `ListTags` | **missing** |
| `iam` | inert | Managed policy | `TagPolicy` / `UntagPolicy` / `ListPolicyTags` | **missing** |
| `iam` | inert | Instance profile | `TagInstanceProfile` / `UntagInstanceProfile` / `ListInstanceProfileTags` | **missing** |
| `transfer` | inert | Server, user | `TagResource` / `UntagResource` / `ListTagsForResource` | **missing** |
| `cloudfront` | inert | Streaming distribution | `CreateStreamingDistributionWithTags` | **out of scope** — Overcast implements no streaming (RTMP) distribution at all, so there is no resource to tag |
| `iam` | inert | SAML provider, OIDC provider, server certificate, MFA device | `TagSAMLProvider`, `TagOpenIDConnectProvider`, `TagServerCertificate`, `TagMFADevice` (+ `Untag*`/`List*Tags`) | **out of scope** — none of these resource types is implemented |

Every other in-scope service's tagging operations are present and reachable:
`s3`, `sqs`, `sns`, `lambda`, `apigateway`, `autoscaling`, `cognito`,
`dynamodb`, `ec2`, `ecr`, `ecs`, `efs`, `eks`, `elasticache`, `kms`, `msk`,
`pipes`, `rds`, `scheduler`, `secretsmanager`, `ssm`, `stepfunctions`,
`appconfig`, `appregistry`, `appsync`, `athena`, `cloudwatch`, `elbv2`,
`eventbridge`, `firehose`, `glue`, `opensearch`, `route53`. `appconfigdata`,
`cloudformation`, `dynamodbstreams` and `sts` model no tagging operations at
all.

### Axis B — missing tag-on-create

| Service | Tier | Create operation | AWS member | Overcast status |
| --- | --- | --- | --- | --- |
| `sns` | **full** | `CreateTopic` | `Tags` | **missing** — accepted by the wire decoder's tolerance, silently dropped |
| `kinesis` | partial | `CreateStream` | `Tags` | **missing** |
| `kms` | partial | `CreateKey` | `Tags` | **missing** |
| `rds` | partial | `CreateDBInstance`, `CreateDBCluster`, `CreateDBSubnetGroup` | `Tags` | **missing** |
| `elasticache` | partial | `CreateCacheCluster`, `CreateReplicationGroup`, `CreateServerlessCache` | `Tags` | **missing** |
| `stepfunctions` | partial | `CreateStateMachine`, `CreateActivity` | `tags` | **missing** |
| `ssm` | partial | `PutParameter`, `CreateDocument` | `Tags` | **missing** |
| `cognito` | partial | `CreateUserPool` | `UserPoolTags` | **missing** |
| `ecs` | partial | `CreateCluster`, `CreateService`, `RegisterTaskDefinition` | `tags` | **missing** (capacity providers and task sets do honour `tags`) |
| `ses` | partial | `CreateEmailIdentity` | `Tags` | **missing** |
| `ec2` | partial | most `Create*` and `RunInstances` | `TagSpecifications` | **partial** — only `RunInstances`, `CreateNatGateway` and `CreateVpnGateway` parse `TagSpecification.N`; the other create operations ignore it. Two near-identical parsers exist for it (`parseTagSpecifications` in `handler_instances.go`, `collectTagSpecifications` in `handler_natgw.go`) |
| `acm` | inert | `RequestCertificate` | `Tags` | **missing** |
| `athena` | inert | `CreateWorkGroup` | `Tags` | **missing** |
| `appconfig` | inert | `CreateApplication`, `CreateEnvironment`, `CreateConfigurationProfile` | `Tags` | **missing** |
| `backup` | inert | `CreateBackupVault`, `CreateBackupPlan` | `BackupVaultTags` / `BackupPlanTags` | **missing** |
| `cloudtrail` | inert | `CreateTrail` | `TagsList` | **missing** |
| `elbv2` | inert | `CreateLoadBalancer`, `CreateTargetGroup`, `CreateRule` | `Tags` | **missing** |
| `eventbridge` | inert | `CreateEventBus`, `PutRule` | `Tags` | **missing** |
| `firehose` | inert | `CreateDeliveryStream` | `Tags` | **missing** |
| `iam` | inert | `CreateUser`, `CreateRole`, `CreatePolicy`, `CreateInstanceProfile` | `Tags` | **missing** |
| `opensearch` | inert | `CreateDomain` | `TagList` | **missing** |
| `transfer` | inert | `CreateServer`, `CreateUser` | `Tags` | **missing** |

Services that already apply tags at creation: `sqs` (`CreateQueue`), `lambda`
(`CreateFunction`, and event source mappings, whose tags are deliberately stored
out-of-band because `EventSourceMappingConfiguration` has no `Tags` member),
`dynamodb` (`CreateTable`), `ecr` (`CreateRepository`), `efs`, `eks`, `msk`,
`pipes`, `scheduler`, `secretsmanager`, `logs` (`CreateLogGroup` — the tags are
stored but unreadable, since Axis A has no way to list them),
`cloudformation`, `autoscaling`, `apigateway`, `appregistry`, `appsync`
(`CreateGraphqlApi`), `cloudwatch`, `s3` (`PutBucketTagging` only; AWS has no
tag member on `CreateBucket`).

## Findings that are not tagging gaps but block them

### `backup` is not reachable by a real SDK at all

AWS Backup is a REST-JSON service: `PUT /backup-vaults/{name}`,
`GET /tags/{ResourceArn}`, and so on. Overcast's `backup` service registers **no
chi routes** (`RegisterRoutes` is empty) and is dispatched purely off
`X-Amz-Target: AWSBackup.<Operation>`, a target prefix AWS Backup does not use.
Nothing in `aws-sdk-*/service/backup` will ever reach it. Adding tagging in the
existing `X-Amz-Target` style would produce operations that are equally
unreachable, so the tagging gap here cannot be honestly closed without first
re-binding the service to its modeled REST paths. That is a service-shaped
change, not a tagging change, and is left out of this branch deliberately.

### Duplicate legacy/typed tag handlers

Services built on the `dispatchLegacy` + `typedOps` pair (`athena`, `backup`,
`transfer`, `cloudtrail`, and others) implement each operation twice: once as an
`http.HandlerFunc` for the JSON 1.0/1.1 path and once as a typed function for the
Smithy RPCv2 CBOR path. For tag operations this is ~120 duplicated lines per
service with the two copies free to drift. New tag operations added by this
branch are written **once**, as typed functions, with the legacy switch invoking
the typed operation through the request's codec. Retrofitting the existing
duplicated operations is a separate, larger change and is not attempted here.

Closing those gaps needed a small adapter — run a typed operation over a plain
JSON 1.0/1.1 request — in each service whose legacy switch had to reach the new
typed handlers. It now exists once in `transfer` and once in `cloudtrail`, each
using its own package's decode and error conventions. **Two copies of a
fifteen-line generic is the point at which it should move to `serviceutil`**;
that is proposed rather than done here, because it is a shared-surface change
that belongs in its own review rather than riding along inside a per-service
tagging commit.

`serviceutil/tags.go` already carries the shared merge/remove/validate helpers
(`ApplyTags`, `RemoveTags`, `ValidateTags`, `TagValidationConfig`,
`ApplyTagsToStore`, `NSStore`). New work uses them; no new tagging abstraction
was introduced.

## Fenced services

`internal/services/cloudwatch/**` is owned by another task (issue #794) for the
duration of this branch, and `internal/services/cloudwatch/logs/` sits inside
that path. The `logs` gap in Axis A is therefore **reported, not fixed**, even
though it is the highest-tier hard gap in the audit. `internal/services/scheduler/**`
is likewise fenced (issue #793); it has no tagging gap.

## Work order

Highest tier first, one commit per service, each standing alone.

1. `sns` — `CreateTopic` tag-on-create (tier full)
2. `kinesis` — ARN-based tag operations + `CreateStream` tags (tier partial)
3. `ses` — SESv2 identity tagging + `CreateEmailIdentity` tags (tier partial)
4. `acm` — modern tag operation aliases + `RequestCertificate` tags (tier inert)
5. `iam` — managed policy and instance profile tagging + tag-on-create for
   users, roles, policies and instance profiles (tier inert)
6. `transfer` — server and user tagging + tag-on-create (tier inert)
7. `cloudtrail` — trail tagging + `CreateTrail` tags (tier inert)
