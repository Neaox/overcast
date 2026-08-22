# Resource Tagging Coverage Audit

> Status: audit complete. Every **Axis A** gap is closed (#1195 filled the
> last two, `logs` and `backup`), together with the Axis B gaps in the
> services that closed alongside them. The remaining Axis B and Axis C sets
> are listed under [What remains](#what-remains). A third question — whether
> a service's tag-accepting write actually reaches the shared validator, not
> just whether the operation exists — is #1052's, closed for all seventeen
> services it named; see [Validator reach audit](#validator-reach-audit-1052).
>
> Fixed 2026-08-22 (#1309) — `AWS::CloudTrail::Trail`'s CloudFormation handler
> read the trail name from a template property named `Name`; the real
> resource schema's property is `TrailName`
> (https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-cloudtrail-trail.html).
> Found while writing #1197's tag-reconciliation tests (PR #1308) and tracked
> separately because it is a property-name bug, not a tagging gap. The handler
> now reads `TrailName` (create-only — a change forces replacement, matching
> the schema and CloudTrail's own `UpdateTrail`, which cannot rename a trail);
> `Name` is not accepted as an alias, per the alpha no-shims policy. `Ref` now
> also returns the trail's name rather than its ARN, matching AWS ("Ref
> returns the friendly name of the trail"). This is orthogonal to the Axis C
> tag-propagation work below, which #1308/#1310 track.
>
> Re-verified 2026-08-23 (#1196) — Axis B is closed for all 14 services this
> issue named. Three (`kms`, `appconfig`, `opensearch`) were already fixed
> before this issue was picked up; the rest needed a real fix, several of
> which were double-fixes because the legacy JSON/Query handler and the CBOR
> typed path were two independent implementations, only one of which had ever
> been touched: `athena` (`CreateWorkGroup`), `eventbridge` (`CreateEventBus`,
> `PutRule`), `firehose` (`CreateDeliveryStream`), `ssm` (`PutParameter`),
> `rds` (`CreateDBInstance`/`CreateDBCluster`/`CreateDBSubnetGroup`),
> `elasticache` (`CreateReplicationGroup`), `stepfunctions`
> (`CreateStateMachine`), `cognito` (`CreateUserPool`), `ecs` (`CreateCluster`,
> `CreateService`), and `ec2` (nine more `Create*` operations, using the
> `parseTagSpecifications`/`validateTagSpecifications`/`putResourceTags`
> helpers #1033 already unified). `stepfunctions` `CreateActivity`, `ssm`
> `CreateDocument` and `elbv2` `CreateRule` are not implemented in this
> emulator at all — feature gaps, not tag gaps, and out of #1196's scope. See
> [What #1196 filled](#what-1196-filled) and the updated
> [Axis B table](#axis-b--missing-tag-on-create).
>
> Re-verified 2026-08-22 (#1052) — every one of the 17 services #1052 named
> now routes its tag-accepting writes through `serviceutil.ValidateTags`
> (directly, via `ApplyStoreTags`, or via `ApplyInlineTags`); three
> (`cognito`, `glue`, `stepfunctions`) were already fixed by #1037 and needed
> no change. Full disposition table below.
>
> Re-verified 2026-08-22 (#1195) — movement since the previous update:
> - **`logs` Axis A is closed**: the modern `TagResource` / `UntagResource` /
>   `ListTagsForResource` spelling now sits alongside the legacy
>   `TagLogGroup` / `UntagLogGroup` / `ListTagsLogGroup` trio, resolving a
>   resource ARN to the log group name and sharing the legacy spelling's
>   validation and storage rather than duplicating it.
> - **`backup` is closed on both axes**: `TagResource` (`POST
>   /tags/{ResourceArn}`), `ListTags` (`GET /tags/{ResourceArn}`) and
>   `UntagResource` (`POST /untag/{ResourceArn}`, deliberately not another
>   member of the shared `/tags` dispatcher) are implemented, tags are stored
>   inline on the vault/plan record so they die with it, and
>   `BackupVaultTags`/`BackupPlanTags` now stick at create time instead of
>   being accepted and dropped. The CloudFormation provisioner forwards both
>   members at create and reconciles a tag-only stack update in place via
>   TagResource/UntagResource (`internal/services/cloudformation/provisioner_json_coverage.go`),
>   without forcing the replacement a structural `BackupPlan`/`BackupVaultName`
>   change still requires.
> - Previously re-verified (2026-08-21): two Axis B rows closed —
>   `opensearch` `CreateDomain` (inline `TagList` applied at creation, per its
>   #893 rebind) and `appconfig` creates (tags applied inline since the #899
>   rewrite). The remaining Axis B rows below were spot-checked (kms, rds,
>   elasticache, stepfunctions, ssm, ecs, eventbridge…) and are still open,
>   but re-verify a row against the handler before acting on it. EC2's
>   duplicate `TagSpecification.N` parsers were unified by #1033; the create
>   operations outside RunInstances/NAT/VPN gateways still ignore the member.
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
| `logs` | partial | Log group, log stream, destination | `TagResource` / `UntagResource` / `ListTagsForResource`, plus the deprecated `TagLogGroup` / `UntagLogGroup` / `ListTagsLogGroup` | ~~missing~~ **closed for log groups** (#1195); log streams and destinations are not emulated |
| `kinesis` | partial | Stream | `TagResource` / `UntagResource` / `ListTagsForResource` (ARN-based, alongside the older stream-name ops) | ~~partial~~ **closed** |
| `ses` | partial | Email identity, configuration set | SESv2 `TagResource` / `UntagResource` / `ListTagsForResource` (`POST`/`DELETE`/`GET /v2/email/tags`) | ~~missing~~ **closed for identities**; configuration sets are not emulated |
| `acm` | inert | Certificate | `TagResource` / `UntagResource` / `ListTagsForResource` (modern aliases of `AddTagsToCertificate` / `RemoveTagsFromCertificate` / `ListTagsForCertificate`) | ~~partial~~ **closed** |
| `backup` | inert | Backup vault, backup plan | `TagResource` / `UntagResource` / `ListTags` | ~~missing~~ **closed** (#1195); see [`backup` was not reachable by a real SDK at all](#backup-was-not-reachable-by-a-real-sdk-at-all--since-fixed-and-the-gap-is-now-unblocked) |
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
`eventbridge`, `firehose`, `glue`, `opensearch`, `route53`, `logs`, `backup`.
`appconfigdata`, `cloudformation`, `dynamodbstreams` and `sts` model no
tagging operations at all.

### Axis B — missing tag-on-create

| Service | Tier | Create operation | AWS member | Overcast status |
| --- | --- | --- | --- | --- |
| `sns` | **full** | `CreateTopic` | `Tags` | ~~missing~~ **closed** |
| `kinesis` | partial | `CreateStream` | `Tags` | ~~missing~~ **closed** |
| `kms` | partial | `CreateKey` | `Tags` | ~~missing~~ **closed** (already fixed pre-#1196; re-verified) |
| `rds` | partial | `CreateDBInstance`, `CreateDBCluster`, `CreateDBSubnetGroup` | `Tags` | ~~missing~~ **closed** (#1196) |
| `elasticache` | partial | `CreateCacheCluster`, `CreateReplicationGroup`, `CreateServerlessCache` | `Tags` | ~~missing~~ **closed** (#1196; `CreateCacheCluster`/`CreateServerlessCache` were already fixed, `CreateReplicationGroup` — including its independent CBOR typed path — was the remaining gap) |
| `stepfunctions` | partial | `CreateStateMachine`, `CreateActivity` | `tags` | ~~missing~~ **closed for `CreateStateMachine`** (#1196; the request modeled no `tags` member at all). `CreateActivity` is not implemented in this emulator — a feature gap, not a tag gap |
| `ssm` | partial | `PutParameter`, `CreateDocument` | `Tags` | ~~missing~~ **closed for `PutParameter`** (#1196; tags apply only when the call creates a new parameter, per AWS's own documented behavior). `CreateDocument` is not implemented (returns 501) — a feature gap, not a tag gap |
| `cognito` | partial | `CreateUserPool` | `UserPoolTags` | ~~missing~~ **closed** (#1196; the request modeled no `UserPoolTags` member at all) |
| `ecs` | partial | `CreateCluster`, `CreateService`, `RegisterTaskDefinition` | `tags` | ~~missing~~ **closed** (#1196 for `CreateCluster`/`CreateService`; `RegisterTaskDefinition` was already fixed via `storeTaskDefinitionTags`) |
| `ses` | partial | `CreateEmailIdentity` | `Tags` | ~~missing~~ **closed** |
| `ec2` | partial | most `Create*` and `RunInstances` | `TagSpecifications` | ~~partial~~ **closed** (#1196) — `CreateVpc`, `CreateSubnet`, `CreateSecurityGroup`, `CreateInternetGateway`, `CreateRouteTable`, `CreateVpcEndpoint`, `CreateVpcPeeringConnection`, `CreateNetworkInterface` and `CreateKeyPair` now parse `TagSpecification.N` through the same `parseTagSpecifications`/`validateTagSpecifications`/`putResourceTags` helpers `RunInstances`, `CreateNatGateway` and `CreateVpnGateway` already used (unified by #1033) |
| `acm` | inert | `RequestCertificate` | `Tags` | ~~missing~~ **closed** |
| `athena` | inert | `CreateWorkGroup` | `Tags` | ~~missing~~ **closed** (#1196) |
| `appconfig` | inert | `CreateApplication`, `CreateEnvironment`, `CreateConfigurationProfile` | `Tags` | ~~missing~~ **closed** (already fixed since the #899 rewrite; re-verified) |
| `backup` | inert | `CreateBackupVault`, `CreateBackupPlan` | `BackupVaultTags` / `BackupPlanTags` | ~~missing~~ **closed** (#1195); both members are stored at create time, and the CloudFormation provisioner forwards them and reconciles a tag-only update in place |
| `cloudtrail` | inert | `CreateTrail` | `TagsList` | ~~missing~~ **closed** |
| `elbv2` | inert | `CreateLoadBalancer`, `CreateTargetGroup`, `CreateRule` | `Tags` | ~~missing~~ **closed for `CreateLoadBalancer`/`CreateTargetGroup`** (already fixed; re-verified). `CreateRule` is not implemented in this emulator (`StatusUnsupported`) — a feature gap, not a tag gap |
| `eventbridge` | inert | `CreateEventBus`, `PutRule` | `Tags` | ~~missing~~ **closed** (#1196; `PutRule`'s tags merge with the rule's existing tags, matching AWS's documented merge semantic) |
| `firehose` | inert | `CreateDeliveryStream` | `Tags` | ~~missing~~ **closed** (#1196) |
| `iam` | inert | `CreateUser`, `CreateRole`, `CreatePolicy`, `CreateInstanceProfile` | `Tags` | ~~missing~~ **closed** |
| `opensearch` | inert | `CreateDomain` | `TagList` | ~~missing~~ **closed** (already fixed per its #893 rebind; re-verified) |
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
blocker in both axes was therefore lifted, and #1195 has now done the tagging
work itself:

- **Axis A** — `TagResource` (`POST /tags/{ResourceArn}`), `ListTags`
  (`GET /tags/{ResourceArn}`) and `UntagResource` (`POST /untag/{ResourceArn}`)
  are implemented (`internal/services/backup/handler_tags.go`). `UntagResource`
  unbinds tags at `/untag`, not with `DELETE /tags`, so it is not another
  member of the shared `/tags` dispatcher; it is registered directly by
  Backup's own `RegisterRoutes` instead. `TagResource` and `ListTags` do share
  `/tags`, joining it through a `TagsRouter()` recorded as a `dispatchMount`,
  exactly as Scheduler's do — registering competing `/tags` patterns from
  `RegisterRoutes` is what `internal/router/router.go`'s ARN-dispatcher exists
  to prevent. Tags are stored inline on the vault/plan record rather than in a
  namespace of their own, so they die with the resource in the same store
  write that deletes it, matching #1037's tags-die-with-their-resource rule.
- **Axis B** — `BackupVaultTags` on `CreateBackupVault` and `BackupPlanTags` on
  `CreateBackupPlan` are validated (via the shared `serviceutil.ValidateTags`)
  and stored at create time, so they are readable through `ListTags`
  immediately. `UpdateBackupPlan` models no tags member and Backup models no
  `UpdateBackupVault` at all, so a plan or vault's tags can only change
  in-place through a follow-up `TagResource`/`UntagResource` — which is also
  what the CloudFormation provisioner now does for a tag-only stack update,
  rather than forcing the replacement a structural change still requires.

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

`internal/services/scheduler/**` was fenced during the original audit branch
(issue #793); it has no tagging gap. `internal/services/cloudwatch/**` was
fenced for issue #794 at the same time, which is why the `logs` Axis A gap —
the highest-tier hard gap the audit found — was reported rather than fixed
there. That fence was long lifted before #1195 closed the gap directly (see
the Status note at the top of this document).

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

## What #1196 filled

One commit per service, re-verifying each against current code before
touching it — three (`kms`, `appconfig`, `opensearch`) turned out to already
be fixed and needed no change.

| Commit | Service | Create operation(s) fixed | Notes |
| --- | --- | --- | --- |
| `feat(athena)` | `athena` | `CreateWorkGroup` | Legacy JSON handler was a full duplicate of the typed path; now delegates to it |
| `feat(eventbridge)` | `eventbridge` | `CreateEventBus`, `PutRule` | Same legacy/typed duplication; `PutRule`'s tags merge with the rule's existing tags |
| `feat(firehose)` | `firehose` | `CreateDeliveryStream` | Same legacy/typed duplication |
| `feat(ssm)` | `ssm` | `PutParameter` | Same legacy/typed duplication; tags apply only when creating a new parameter, not on `Overwrite` |
| `feat(rds)` | `rds` | `CreateDBInstance`, `CreateDBCluster`, `CreateDBSubnetGroup` | Single (Query-protocol) implementation per operation; no decoder change needed — the Query codec already supports `Tags.Tag.N.Key` for any slice field |
| `feat(elasticache)` | `elasticache` | `CreateReplicationGroup` | `CreateCacheCluster`/`CreateServerlessCache` were already fixed; `CreateReplicationGroup` had its own independent CBOR typed path too |
| `feat(stepfunctions)` | `stepfunctions` | `CreateStateMachine` | Request modeled no `tags` member at all; fixed a pre-existing region bug in the typed path while unifying it with the legacy handler |
| `feat(cognito)` | `cognito` | `CreateUserPool` | Request modeled no `UserPoolTags` member at all; legacy/typed duplication |
| `feat(ecs)` | `ecs` | `CreateCluster`, `CreateService` | `RegisterTaskDefinition` was already fixed; both remaining operations had legacy/typed duplication |
| `feat(ec2)` | `ec2` | 9 operations (see the Axis B table) | Single implementation per operation (EC2's Query protocol has no legacy/typed split for these); `CreateVpcEndpoint`/`CreateVpcPeeringConnection` previously rendered no `TagSet` in any response at all |

Left as feature gaps, not tag gaps, and out of scope: `stepfunctions`
`CreateActivity`, `ssm` `CreateDocument` (both return 501 — not implemented),
and `elbv2` `CreateRule` (`StatusUnsupported` — not implemented).

## What remains

**Axis A — closed.** Both services this section used to track — `logs`
(fenced for issue #794 at the time of the original audit) and `backup`
(blocked on the REST-rebind #815 did) — were closed by #1195. See the Status
note at the top of this document.

**Axis B — closed.** All 14 services #1196 named are closed; see
[What #1196 filled](#what-1196-filled) above and the updated
[Axis B table](#axis-b--missing-tag-on-create).

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

## Validator reach audit (#1052)

A third, orthogonal question the two axes above do not answer: for a service
that *has* a tag-accepting operation (Axis A) or a create-time `Tags` member
(Axis B), does the incoming tag set actually reach `serviceutil.ValidateTags`
before it is stored? #1052 found seventeen services where it did not — the
operation existed and answered success, but never checked AWS's own tag
constraints (the 128/256-character key/value limits, the reserved `aws:` key
prefix, the 50-tag cap), so a tag set real AWS refuses was silently accepted
and stored here.

Re-verified against current code — three of the seventeen turned out to be
already fixed by #1037's tagging-architecture review, and the rest needed a
real change:

| Service | Disposition |
| --- | --- |
| `acm` | Fixed — `RequestCertificate`'s inline `Tags`, `AddTagsToCertificate`/`TagResource` (one shared implementation via `serveTyped`) |
| `apigateway` | Fixed — `TagResource`, `TagV2Resource`, and nine `Create*` inline-tag call sites (REST APIs, HTTP APIs, domain names ×2, VPC links ×2, API keys, usage plans, stages ×2) |
| `appregistry` | Fixed — `CreateApplication`, `CreateAttributeGroup`, on both the JSON1.1 REST path and the CBOR typed path (both are genuinely reachable — the JSON path is a real `POST` route, not merely a codec variant of the typed one). `TagResource` was already covered: it is apigateway's own shared ARN-keyed handler, not a separate implementation |
| `appsync` | Fixed — retired a locally re-implemented duplicate validator (`validateTagMap`, `tagKeyPattern`, `tagValuePattern`) that never called the shared one and was *more* restrictive than AWS's documented pattern: its key regex rejected any digit at all, which no AWS documentation supports. `CreateGraphqlApi`/`TagResource` already enforced equivalent (if buggier) rules; `UpdateGraphqlApi` enforced none, because `validateGraphqlAPIInput`'s `allowTags` parameter skipped the tag check there entirely — real AWS models no tags member on `UpdateGraphqlApiInput`, but this handler decodes into the same `GraphqlAPI` struct either way, so a client-supplied `tags` on update was stored unvalidated |
| `cloudformation` | Fixed — `CreateStack`, `UpdateStack`, `CreateChangeSet`, `ExecuteChangeSet`, on both the Query/XML legacy path and the CBOR typed path (each pair is a genuine standalone duplicate, not a codec variant of one implementation) |
| `cloudfront` | Fixed — `TagResource`, `CreateDistributionWithTags` |
| `cognito` | Already fixed (#1037) — `TagResource` already validates via `ApplyInlineTags`; `CreateUserPool` models no `Tags`/`UserPoolTags` request field in this emulator to bypass (a feature gap tracked under Axis B, not a validator-reach gap) |
| `dynamodb` | Fixed (narrow) — `TagResource` already validated via `dynamoTagCfg`/`ApplyInlineTags`; `CreateTable`'s inline `Tags` did not |
| `ecr` | Fixed — `CreateRepository`, `TagResource`, on both the JSON1.1 legacy path (lowercase `createRepository`/`tagResource` — a genuine second copy, easy to miss by name alone) and the CBOR typed path |
| `efs` | Fixed — `CreateFileSystem`, `CreateAccessPoint`, `TagResource`, all three funneling through one shared `mergeTags` store helper that now validates the merged set before saving |
| `glue` | Already fixed (#1037) — `TagResource` for both taggable resource types (database, table) already routes through `ApplyInlineTags` with a declared `glueTagCfg`; neither's `Create*` models a `Tags` member to bypass |
| `iam` | Fixed — all four taggable resource types (user, role, managed policy, instance profile): both their `Tag*` operation and their `Create*`'s inline `Tags`, on both the Query/XML legacy path and the CBOR typed path. `TagPolicy`/`TagInstanceProfile` already delegated to the typed path via a `typedHandler` adapter; `TagRole`/`TagUser` and all four `Create*` were genuine standalone Query/XML duplicates that had to be patched separately |
| `kinesis` | Fixed — `CreateStream`, `AddTagsToStream` (a genuine legacy-path duplicate that skipped `addTagsToStreamTyped` entirely), `TagResource` |
| `kms` | Fixed — `CreateKey`, `TagResource`, on both the JSON1.1 legacy path and the CBOR typed path |
| `msk` | Fixed — `CreateCluster`, `CreateClusterV2`, `TagResource` |
| `secretsmanager` | Fixed — `CreateSecret`'s inline `Tags`; `TagResource` now shares `serviceutil.ApplyInlineTags` (`Secret` already implements `Taggable`, so this replaced a hand-rolled merge rather than adding one) |
| `stepfunctions` | Already fixed (#1037) — `TagResource` already routes through `ApplyInlineTags`; `CreateStateMachine`/`CreateActivity` model no `tags` request member in this emulator to bypass |

Every fix keeps the shared, conservative charset
(`^(?!aws:)[\p{L}\p{Z}\p{N}_.:/=+\-@]*$`, enforced by `serviceutil.ValidateTags`)
rather than inventing a per-service rule. The one place a per-service pattern
existed before this pass — AppSync's `tagKeyPattern` — was strictly *more*
restrictive than AWS actually documents and rejected a real, legal tag-key
character (`@`); it is retired here, not preserved alongside the shared one.

A recurring shape worth naming: several of the "already had a tag operation"
services turned out to have *two* implementations of it — a JSON1.1 or
Query/XML legacy handler and a CBOR typed-operation handler — where only one
of the two had been wired to the shared validator, or neither had. `ecr`'s
legacy `createRepository`/`tagResource` (lowercase, easy to miss grepping for
`CreateRepository`) is the sharpest example. Each such pair is patched
separately rather than collapsed into one delegating implementation, the same
choice #1037 made for its own duplicated legacy/typed pairs — a larger
refactor left for its own review.

Left for later issues: the fourteen Axis B services (#1196) and the eight
CloudFormation resource types (#1197) this file's own
[What remains](#what-remains) section already tracks are unaffected by this
pass — a service still missing its create-time `Tags` member entirely has
nothing for a validator to reach yet.
