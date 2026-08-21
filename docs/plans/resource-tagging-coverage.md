# Resource Tagging Coverage Audit

> Status: audit complete. Every **Axis A** gap outside the fenced `logs`
> service is closed, together with the Axis B gaps in the same services. The
> remaining Axis B and Axis C sets are listed under
> [What remains](#what-remains).
>
> Re-verified 2026-08-21 — movement since the audit:
> - **`logs` Axis A is mostly closed**: `TagLogGroup` / `UntagLogGroup` /
>   `ListTagsLogGroup` shipped (the #794 fence is long lifted); the modern
>   `TagResource` / `UntagResource` / `ListTagsForResource` spelling is still
>   absent from `cloudwatch-logs`.
> - **`backup` is still open on both axes**: #815/#904 rebound the service to
>   its modeled REST paths, but the capability notes still read
>   "`BackupVaultTags` is accepted and dropped — Backup has no tag operations
>   yet".
> - **Two Axis B rows have since closed**: `opensearch` `CreateDomain` (inline
>   `TagList` applied at creation, per its #893 rebind) and `appconfig`
>   creates (tags applied inline since the #899 rewrite). The remaining Axis B
>   rows below were spot-checked (kms, rds, elasticache, stepfunctions, ssm,
>   ecs, eventbridge…) and are still open, but re-verify a row against the
>   handler before acting on it.
> - EC2's duplicate `TagSpecification.N` parsers were unified by #1033 (per the
>   tagging architecture review, closed with every finding fixed and its plan
>   doc deleted 2026-08-21); the create operations outside RunInstances/NAT/VPN
>   gateways still ignore the member.
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
| `kinesis` | partial | Stream | `TagResource` / `UntagResource` / `ListTagsForResource` (ARN-based, alongside the older stream-name ops) | ~~partial~~ **closed** |
| `ses` | partial | Email identity, configuration set | SESv2 `TagResource` / `UntagResource` / `ListTagsForResource` (`POST`/`DELETE`/`GET /v2/email/tags`) | ~~missing~~ **closed for identities**; configuration sets are not emulated |
| `acm` | inert | Certificate | `TagResource` / `UntagResource` / `ListTagsForResource` (modern aliases of `AddTagsToCertificate` / `RemoveTagsFromCertificate` / `ListTagsForCertificate`) | ~~partial~~ **closed** |
| `backup` | inert | Backup vault, backup plan | `TagResource` / `UntagResource` / `ListTags` | **missing — not filled**; the protocol blocker is gone since #815, the tagging is not done, see [`backup` was not reachable by a real SDK at all](#backup-was-not-reachable-by-a-real-sdk-at-all--since-fixed-and-the-gap-is-now-unblocked) |
| `cloudtrail` | inert | Trail | `AddTags` / `RemoveTags` / `ListTags` | ~~missing~~ **closed** |
| `iam` | inert | Managed policy | `TagPolicy` / `UntagPolicy` / `ListPolicyTags` | ~~missing~~ **closed** |
| `iam` | inert | Instance profile | `TagInstanceProfile` / `UntagInstanceProfile` / `ListInstanceProfileTags` | ~~missing~~ **closed** |
| `transfer` | inert | Server, user | `TagResource` / `UntagResource` / `ListTagsForResource` | ~~missing~~ **closed** |
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
| `sns` | **full** | `CreateTopic` | `Tags` | ~~missing~~ **closed** |
| `kinesis` | partial | `CreateStream` | `Tags` | ~~missing~~ **closed** |
| `kms` | partial | `CreateKey` | `Tags` | **missing** |
| `rds` | partial | `CreateDBInstance`, `CreateDBCluster`, `CreateDBSubnetGroup` | `Tags` | **missing** |
| `elasticache` | partial | `CreateCacheCluster`, `CreateReplicationGroup`, `CreateServerlessCache` | `Tags` | **missing** |
| `stepfunctions` | partial | `CreateStateMachine`, `CreateActivity` | `tags` | **missing** |
| `ssm` | partial | `PutParameter`, `CreateDocument` | `Tags` | **missing** |
| `cognito` | partial | `CreateUserPool` | `UserPoolTags` | **missing** |
| `ecs` | partial | `CreateCluster`, `CreateService`, `RegisterTaskDefinition` | `tags` | **missing** (capacity providers and task sets do honour `tags`) |
| `ses` | partial | `CreateEmailIdentity` | `Tags` | ~~missing~~ **closed** |
| `ec2` | partial | most `Create*` and `RunInstances` | `TagSpecifications` | **partial** — only `RunInstances`, `CreateNatGateway` and `CreateVpnGateway` parse `TagSpecification.N`; the other create operations ignore it. Two near-identical parsers exist for it (`parseTagSpecifications` in `handler_instances.go`, `collectTagSpecifications` in `handler_natgw.go`) |
| `acm` | inert | `RequestCertificate` | `Tags` | ~~missing~~ **closed** |
| `athena` | inert | `CreateWorkGroup` | `Tags` | **missing** |
| `appconfig` | inert | `CreateApplication`, `CreateEnvironment`, `CreateConfigurationProfile` | `Tags` | **missing** |
| `backup` | inert | `CreateBackupVault`, `CreateBackupPlan` | `BackupVaultTags` / `BackupPlanTags` | **missing — not filled**, same as Axis A: unblocked by #815, still not done; both members are accepted and dropped |
| `cloudtrail` | inert | `CreateTrail` | `TagsList` | ~~missing~~ **closed** |
| `elbv2` | inert | `CreateLoadBalancer`, `CreateTargetGroup`, `CreateRule` | `Tags` | **missing** |
| `eventbridge` | inert | `CreateEventBus`, `PutRule` | `Tags` | **missing** |
| `firehose` | inert | `CreateDeliveryStream` | `Tags` | **missing** |
| `iam` | inert | `CreateUser`, `CreateRole`, `CreatePolicy`, `CreateInstanceProfile` | `Tags` | ~~missing~~ **closed** |
| `opensearch` | inert | `CreateDomain` | `TagList` | **missing** |
| `transfer` | inert | `CreateServer`, `CreateUser` | `Tags` | ~~missing~~ **closed** |

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

### `backup` was not reachable by a real SDK at all — since fixed, and the gap is now unblocked

AWS Backup is a REST-JSON service: `PUT /backup-vaults/{name}`,
`GET /tags/{ResourceArn}`, and so on. Overcast's `backup` service registered
**no chi routes** (`RegisterRoutes` was empty) and was dispatched purely off
`X-Amz-Target: AWSBackup.<Operation>`, a target prefix AWS Backup does not use.
Nothing in `aws-sdk-*/service/backup` would ever have reached it. Adding tagging
in the existing `X-Amz-Target` style would have produced operations that were
equally unreachable, so the tagging gap could not be honestly closed without
first re-binding the service to its modeled REST paths. That is a
service-shaped change, not a tagging change, and was left out of this branch
deliberately.

**#815 has since done it**: all nine operations are served under
`/backup-vaults` and `/backup/plans`, and the target prefix is gone. The
blocker in both axes is therefore lifted — but the tagging work itself is still
outstanding, and #815 did not attempt it. What remains:

- **Axis A** — `TagResource` (`POST /tags/{ResourceArn}`), `ListTags`
  (`GET /tags/{ResourceArn}`) and `UntagResource` (`POST /untag/{ResourceArn}`).
  Note the third: Backup unbinds tags at `/untag`, not with `DELETE /tags`, so
  it is not simply another member of the shared `/tags` dispatcher. The first
  two are, and must join it through a `TagsRouter()` recorded as a
  `dispatchMount`, exactly as Scheduler's do — registering competing `/tags`
  patterns from `RegisterRoutes` is what `internal/router/router.go`'s
  ARN-dispatcher exists to prevent.
- **Axis B** — `BackupVaultTags` on `CreateBackupVault` and `BackupPlanTags` on
  `CreateBackupPlan`. Both members are accepted and dropped today, which the
  capability rows and `docs/services/backup.md` say plainly.

### Duplicate legacy/typed tag handlers

Services built on the `dispatchLegacy` + `typedOps` pair (`athena`, `transfer`,
`cloudtrail`, and others — `backup` was one until #815 deleted its half, the
pinned model giving AWS Backup no RPC binding to serve) implement each
operation twice: once as an
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

## AWS behaviour that had to be decided rather than looked up

Three forks were resolved by judgement. Each is called out here because none of
them is verifiable from the pinned models, which carry operation names,
protocols and HTTP bindings but **not member shapes**.

- **ACM's modern aliases use `ResourceArn`.** `TagResource` / `UntagResource` /
  `ListTagsForResource` were implemented reading `ResourceArn`, not reusing
  ACM's older `CertificateArn`. Every AWS service with an operation literally
  named `TagResource` spells the member `ResourceArn`, and ACM's own
  `*Certificate` operations keep `CertificateArn`. If this is wrong the failure
  is loud — an SDK call arrives with an empty ARN and gets an error — rather
  than silent.
- **SNS `CreateTopic` does not retag an existing topic.** The call is
  idempotent and AWS returns the existing ARN "without creating a new topic",
  so a repeat call with different tags leaves the topic's tags alone.
  `TagResource` is the way to change them.
- **CloudTrail `RemoveTags` matches on `Key` alone.** Its input is a `TagsList`,
  not a list of keys; the implementation ignores each entry's `Value` when
  deciding what to remove.

## Fenced services

`internal/services/cloudwatch/**` is owned by another task (issue #794) for the
duration of this branch, and `internal/services/cloudwatch/logs/` sits inside
that path. The `logs` gap in Axis A is therefore **reported, not fixed**, even
though it is the highest-tier hard gap in the audit. `internal/services/scheduler/**`
is likewise fenced (issue #793); it has no tagging gap.

## What this branch filled

One commit per service, each standing alone, highest tier first.

| Commit | Service | Tier | Axis A | Axis B |
| --- | --- | --- | --- | --- |
| `feat(sns)` | `sns` | full | — (already complete) | `CreateTopic` |
| `feat(kinesis)` | `kinesis` | partial | `TagResource`, `UntagResource`, `ListTagsForResource` | `CreateStream` |
| `feat(ses)` | `ses` | partial | `TagResource`, `UntagResource`, `ListTagsForResource` | `CreateEmailIdentity` |
| `feat(transfer)` | `transfer` | inert | `TagResource`, `UntagResource`, `ListTagsForResource` | `CreateServer`, `CreateUser` |
| `feat(cloudtrail)` | `cloudtrail` | inert | `AddTags`, `RemoveTags`, `ListTags` | `CreateTrail` |
| `feat(iam)` | `iam` | inert | the policy and instance-profile triples | `CreateUser`, `CreateRole`, `CreatePolicy`, `CreateInstanceProfile` |
| `feat(acm)` | `acm` | inert | `TagResource`, `UntagResource`, `ListTagsForResource` | `RequestCertificate` |

Two pre-existing bugs surfaced and were fixed alongside ACM's gap, because they
sat in the code the gap sat in: tagging a certificate that does not exist
succeeded and stranded the tags under an unowned ARN, and
`ListTagsForCertificate` returned a different tag order on every call.

## What remains

**Axis A — one service, fenced.** `logs` (tier partial) has no tag operation of
any spelling: log groups cannot be tagged at all. It is the highest-tier hard
gap in this audit and it is untouched, because `internal/services/cloudwatch/logs/`
sits inside the `internal/services/cloudwatch/**` path fenced for issue #794.
It needs `TagResource` / `UntagResource` / `ListTagsForResource` plus the
deprecated `TagLogGroup` / `UntagLogGroup` / `ListTagsLogGroup`;
`CreateLogGroup` already stores its inline tags, so the tags exist and are
merely unreadable.

**Axis A — one service, open.** `backup`. It was blocked for the protocol
reason above; #815 removed the block and did not close the gap, so this is now
ordinary tagging work with nothing in front of it.

**Axis B — fourteen services.** Every one of these already has working tagging
operations; what is missing is only the inline tags on the create call.

| Service | Tier | Create operations |
| --- | --- | --- |
| `kms` | partial | `CreateKey` |
| `rds` | partial | `CreateDBInstance`, `CreateDBCluster`, `CreateDBSubnetGroup` |
| `elasticache` | partial | `CreateCacheCluster`, `CreateReplicationGroup`, `CreateServerlessCache` |
| `stepfunctions` | partial | `CreateStateMachine`, `CreateActivity` |
| `ssm` | partial | `PutParameter`, `CreateDocument` |
| `cognito` | partial | `CreateUserPool` (`UserPoolTags`) |
| `ecs` | partial | `CreateCluster`, `CreateService`, `RegisterTaskDefinition` |
| `ec2` | partial | the create operations that ignore `TagSpecifications` |
| `athena` | inert | `CreateWorkGroup` |
| `appconfig` | inert | `CreateApplication`, `CreateEnvironment`, `CreateConfigurationProfile` |
| `elbv2` | inert | `CreateLoadBalancer`, `CreateTargetGroup`, `CreateRule` |
| `eventbridge` | inert | `CreateEventBus`, `PutRule` |
| `firehose` | inert | `CreateDeliveryStream` |
| `opensearch` | inert | `CreateDomain` (`TagList`) |

**Axis C — CloudFormation passthrough, eight resource types.** A third gap that
only becomes reachable now that the services accept the tags. These handlers in
[provisioner.go](../../internal/services/cloudformation/provisioner.go) build
their create call from named properties and never read `Tags`, so a template
carrying `Tags:` on one of them has them silently dropped:
`AWS::Kinesis::Stream`, `AWS::CloudTrail::Trail`, `AWS::Transfer::Server`,
`AWS::Transfer::User`, `AWS::IAM::Role`, `AWS::IAM::User`,
`AWS::IAM::ManagedPolicy`, `AWS::IAM::InstanceProfile`. Each needs one property
forwarded to the create call it already dispatches — `AWS::SNS::Topic` is the
worked example, though it applies tags with a follow-up `TagResource` rather
than inline. Left out of this branch to keep it reviewable; it is a single
coherent follow-up rather than eight scattered ones.

**Also proposed, not done:** move the typed-operation JSON adapter into
`serviceutil` (see above), and consider whether `serviceutil` should grow a
list-shaped tag helper — three services in this branch (`transfer`,
`cloudtrail`, `acm`) each convert between AWS' `[{Key,Value}]` wire shape and
the `map[string]string` the existing `ValidateTags` works in.
