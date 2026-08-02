# Services that should never be emulated: policy and classification

> Status: proposal, 2026-08-03. Owner: TBD.
> Related: [AWS API operation coverage](./aws-api-operation-coverage.md) (the routing
> guarantee this policy rides on top of), [Inert-tier rollout](./inert-tier-rollout.md)
> (everything **not** on this document's never-list gets metadata-CRUD "inert"
> treatment), [Full-emulation priority](./full-emulation-priority.md) (fine-ranks
> inert-vs-full within whatever this document leaves in scope). The sibling plans
> are being written concurrently with this one; links may 404 briefly.

## 1. Objective

Answer one question with a table an agent can act on without re-deriving it:
**which AWS services should Overcast permanently leave at Tier 0 (protocol-correct
`501`), and which are fair game for at least Tier 1 ("inert": correct shapes,
metadata CRUD, CDK/CloudFormation succeeds, no real-world side effect)?**

This document does not change routing behavior — [aws-api-operation-coverage.md](./aws-api-operation-coverage.md)
already guarantees every modeled operation reaches an implementation or a
protocol-correct `501`, never an S3 fallthrough, regardless of what this policy
says. What this document changes is *aspiration*: which `501`s are a roadmap gap
("not yet") and which are a permanent, intentional non-goal ("never"), and how
that distinction is made visible to humans and tooling instead of being
indistinguishable noise in a coverage report.

Tier vocabulary used throughout (shared with the sibling plans):

| Tier | Name | Means |
| --- | --- | --- |
| 0 | Not implemented | Protocol-correct `501` via the generic registry fallback. No service package. |
| 1 | Inert | Metadata CRUD, correct request/response shapes, CDK/CloudFormation provisioning succeeds, no real side effects (cf. the five fully-`StatusInert` services — `autoscaling`, `transfer`, `backup`, `cloudtrail`, `organizations` — per [inert-tier-rollout.md](./inert-tier-rollout.md) §2.2). |
| 2 | Full | Behaviorally emulated for common usage patterns. |

## 2. The universe: what are we classifying, and at what granularity

### 2.1 The manifest is (almost) all of public AWS, not just the registered 50

[models/aws/VERSION](../../models/aws/VERSION) pins `aws/api-models-aws` revision
`66e973c` (model date 2026-07-27). `internal/awsapi/manifest.gen.go` is generated
from that snapshot and is **50,386 lines** of `Operation` literals. Counting
distinct `Service` fields in that file gives **426 distinct Smithy service
identities** — this is essentially the full public AWS API surface AWS
publishes daily, not a curated subset. `internal/awsapi/versions.go`,
`manifest.go`, and [aws-api-operation-coverage.md §3](./aws-api-operation-coverage.md#3-source-and-scope)
confirm the intent: the manifest is a routing/ownership corpus for *every*
public operation, deliberately decoupled from what Overcast implements.

So the classification universe for this document is **all 426 manifest
services**, not the smaller set Overcast currently registers.

### 2.2 Ground truth for "currently registered," corrected

**STATUS.md's prose tables are stale and must not be used as the registered-service
list.** Ground truth is:

- `internal/services/` — **49 service package directories** (verified by directory
  listing at the time of writing).
- `internal/capabilities/all.gen.go` (the capgen-generated capability snapshot) —
  **50 distinct `Service` keys**, because `internal/services/cloudwatch/` registers
  two service keys, `cloudwatch` (12 ops) and `cloudwatch-logs` (19 ops).

Both sources agree on **50 registered services**, which happens to match
STATUS.md's headline count even though several of STATUS.md's per-service rows
(e.g. its "Comprehensive" and "Core operations" prose tables) are out of sync
with the auto-generated block further down the same file — EFS is a visible
example, present in the generated block but absent from the hand-written
"Comprehensive" table above it. Treat `internal/services/` + `internal/capabilities/all.gen.go`
as authoritative; treat STATUS.md's prose tables as decorative only.

### 2.3 Classification granularity: Smithy service identity, as emitted by the manifest

Classify at the same granularity the router uses for ownership: the manifest's
`Service` field (e.g. `route-53`, `secrets-manager`, `service-catalog-appregistry`),
not Overcast's internal service key. [aws-api-operation-coverage.md §4.1](./aws-api-operation-coverage.md#41-generated-operation-manifest)
already records the nine intentional aliases between manifest identity and
Overcast key (`apigateway`↔`api-gateway`, `appregistry`↔`service-catalog-appregistry`,
`autoscaling`↔`auto-scaling`, `dynamodbstreams`↔`dynamodb-streams`,
`elbv2`↔`elastic-load-balancing-v2`, `msk`↔`kafka`, `route53`↔`route-53`,
`secretsmanager`↔`secrets-manager`, `stepfunctions`↔`sfn`) and notes that
`lambda-core` is a **distinct** modeled service from `lambda`, not an alias.
This document uses manifest identities throughout and cross-references the
Overcast key only where a service is already registered.

This document governs **service-level** scope only: "should Overcast ever have
a package for this at all." It does not touch the existing **operation-level**
`capabilities.Status` enum (`Unsupported`/`WIP`/`Partial`/`Inert`/`Supported` in
[internal/capabilities/capabilities.go](../../internal/capabilities/capabilities.go)),
which already models per-operation quality within an implemented service and is
unaffected by a service being off this never-list.

## 3. Classification rubric

A service is **never-501** only if it fails *every* veto below and matches at
least one positive criterion. Vibes are not a criterion; every verdict in §4
must cite a letter from this table.

### 3.1 Positive criteria (any one makes never-501 plausible)

| # | Criterion | What it means | Representative services |
| --- | --- | --- | --- |
| A | Requires physical/human/external-world infrastructure | The service's core value is mediating a real object outside the emulator's control: a satellite dish, a shipped appliance, a fiber cross-connect, a real on-prem server being migrated, a physical HSM, a real ICANN domain registration. | Ground Station, Snow Family, Outposts, Direct Connect, Storage Gateway, DRS/MGN/Migration Hub, CloudHSM, Route 53 Domains |
| B | Purely commercial/account-administrative, zero dev-loop value | The API's entire purpose is money, contracts, compliance paperwork, or account bookkeeping — nothing a local stack under test would exercise. | Billing, Cost & Usage Report, Marketplace family, Support, Account, Artifact, License Manager, PartnerCentral family |
| D | Pure console/internal-plumbing product with no customer-facing dev-loop API surface | Either a wizard/UI-only product that just calls other (already-covered) services underneath, or an AWS-internal control API not meant to be SDK/CDK-invoked by customers. | Sign-In helper, account UX settings, Launch Wizard, Lambda's internal microVM control plane |
| E | Deprecated, sunset, or de facto frozen | AWS has stopped onboarding new customers, announced an end-of-support date, or has not shipped a feature in a decade. Verified per-service in §4.2, not assumed. | App Mesh (EOL 2026-09-30), Cloud9/CloudSearch/CodeCommit (closed to new customers), Amazon ML (shut down 2019), SimpleDB (frozen since ~2015) |

(Letter C is deliberately absent from this table — it was originally a never
criterion and was downgraded to a *deferral* criterion by an explicit owner
decision on 2026-08-03; see §3.3. The letters are stable identifiers and are not
renumbered.)

### 3.2 Counter-criteria — any one of these **vetoes** a never-501 verdict

| Veto | Why it overrides |
| --- | --- |
| CDK/CloudFormation calls it during an ordinary deploy | STS, ECR, SSM, S3, and CloudFormation itself are untouchable regardless of any other criterion — breaking them breaks every deploy, not just one service's coverage. |
| It appears in common CDK construct libraries (`aws-cdk-lib`) even if rarely used | A construct existing means real IaC can reference the service; "rare" is a priority-ranking fact for [full-emulation-priority.md](./full-emulation-priority.md), not a never-501 fact. |
| A local dev loop plausibly exercises it even without exercising its "real" function | Directory Service is the model case: its actual domain-join semantics can never be Tier 2 locally, but RDS SQL Server / FSx Windows constructs reference a directory ID, so the metadata CRUD has genuine standalone value. Verdict: inert-candidate, not never. |
| An inert stub is needed for another in-scope service's API to make sense | The Shield precedent ([internal/services/shield](../../internal/services/shield/)): CDK/CloudFormation probe Shield during ordinary WAF/CloudFront stacks even when nobody uses DDoS protection locally, so a minimal stub (`DescribeSubscription`, protection CRUD) exists purely to keep those *other* deploys from erroring. Any future service found to play this role for an in-scope service is vetoed out of never-501 the same way. |
| The rubric criterion only makes the service *hard*, not *nonsensical* | ML inference (Rekognition, Comprehend, Textract, Forecast-while-it-existed, Bedrock model invocation) cannot run a real model locally, but returning a deterministic canned response is a completely coherent Tier 1 stub that unblocks unit tests — this is exactly what LocalStack does for the same services. "Cannot be emulated with full fidelity" is a full-emulation-priority question, not a never-501 question. Generic ML/AI services are therefore **inert-candidate by default**, not never, unless a separate criterion (A/B/D/E) also applies. |

### 3.3 Deferral criterion C — third-party SaaS / telephony bridges (not a never criterion)

**C — Consumes third-party SaaS or a real telephony/carrier network.** The
service exists specifically to bridge to a real external network (PSTN, SMS
carriers, WhatsApp/Meta, Slack/Teams, EDI trading partners). There is no "local"
version of a real phone call or a real SMS — but there *is* a coherent
capture-style mock in the SES mould: SES is registered precisely because it
**captures** outbound mail locally instead of sending it, and the same move
(record the outbound call/SMS/message/flow-run as inspectable local state) is
conceivable for every service in this family. A case for that could be made
later; nobody is making it now.

**Verdict for this category: deferred, not never.** These services stay at
Tier 0 for the foreseeable future, are excluded from every planned wave in
[inert-tier-rollout.md](./inert-tier-rollout.md), sit at the very bottom of
[full-emulation-priority.md](./full-emulation-priority.md)'s ranking, and do
**not** get `NeverEmulated` policy entries (§5.3) — absence from the policy map
correctly reads as "not yet". The distinction from never-501 is the promise:
"never" means *don't ask*; "deferred" means *a capture-style mock proposal with
a concrete user need would be entertained*.

## 4. Verdict table

### 4.1 Summary

| Bucket | Count | Meaning |
| --- | --- | --- |
| **never-501** | 95 manifest services | Permanent Tier 0 by policy (§4.2) |
| **deferred** | 24 manifest services | Third-party SaaS/telephony bridges (criterion C, §3.3). Tier 0 indefinitely, bottom of the priority queue, but a capture-style mock case may be made later — not in the `NeverEmulated` policy map (§4.2c) |
| **registered** | 52 manifest identities → 50 Overcast service keys | Already registered under `internal/services/` (the alias table at [internal/awsapi/registry_data.go:71-84](../../internal/awsapi/registry_data.go) merges 52 modeled identities onto 50 capability keys, e.g. `api-gateway`+`apigatewayv2`→`apigateway`); further tiering is [full-emulation-priority.md](./full-emulation-priority.md)'s job (§4.3) |
| **inert-candidate** | 255 manifest services (426 − 95 − 24 − 52) | Everything else. Default bucket — no per-service justification required to be *in* it; [inert-tier-rollout.md](./inert-tier-rollout.md) owns sequencing (§4.4) |

Counts are family-grouped judgment calls, not a hand-audited census of all 426
services individually — see §7 for the specific rows flagged as lower-confidence
and the revisit mechanism in §6.

### 4.2 Never-501: the full list, grouped by rubric criterion

All entries are manifest service keys (§2.3). None collide with a registered
service or its alias (checked against the nine aliases and `lambda-core`/`lambda`
in §2.3).

**A — Physical/human/external-world infrastructure (36)**

`groundstation`, `outposts`, `s3outposts`, `snowball`, `snow-device-management`,
`braket`, `managedblockchain`, `managedblockchain-query`, `iot-wireless`,
`iotfleetwise`, `iotdeviceadvisor`, `iotsecuretunneling`, `iot-managed-integrations`,
`evs`, `appstream`, `gameliftstreams`, `application-discovery-service`,
`migration-hub`, `migration-hub-refactor-spaces`, `migrationhub-config`,
`migrationhuborchestrator`, `migrationhubstrategy`, `mgn`, `drs`,
`backup-gateway`, `storage-gateway`, `direct-connect`, `interconnect`,
`cloudhsm`, `cloudhsm-v2`, `payment-cryptography`, `payment-cryptography-data`,
`odb`, `route-53-domains`, `health`, `workspaces-thin-client`

One-line justification pattern: each mediates a real object the emulator cannot
conjure — a satellite antenna, a shipped Snowball device, a physical rack, a
real on-prem server/VM being discovered or migrated, a dedicated HSM, a real
domain registrar transaction, AWS's own real operational incidents, or physical
thin-client hardware. `interconnect` (13 ops, undocumented in public AWS docs
under that name) is grouped here on the working assumption it is Direct
Connect-adjacent physical cross-connect plumbing — flagged low-confidence in §7.

**B — Purely commercial/account-administrative (37)**

`account`, `artifact`, `billing`, `billingconductor`, `budgets`, `freetier`,
`invoicing`, `taxsettings`, `cost-and-usage-report-service`, `cost-explorer`
(see borderline paragraph, §4.5), `cost-optimization-hub`, `savingsplans`,
`bcm-dashboards`, `bcm-data-exports`, `bcm-pricing-calculator`,
`bcm-recommended-actions`, `pricing`, `support`, `support-app`, `supportauthz`,
`trustedadvisor`, `marketplace-agreement`, `marketplace-catalog`,
`marketplace-commerce-analytics`, `marketplace-deployment`,
`marketplace-discovery`, `marketplace-entitlement-service`,
`marketplace-metering`, `marketplace-reporting`, `partnercentral-account`,
`partnercentral-benefits`, `partnercentral-channel`,
`partnercentral-revenue-measurement`, `partnercentral-selling`,
`license-manager`, `license-manager-linux-subscriptions`,
`license-manager-user-subscriptions`

Justification pattern: money, contracts, procurement, licensing, or compliance
paperwork against a real payer account. None of these gate a `cdk deploy` of
application infrastructure; none appear as dependencies of other services'
control planes the way Shield does for WAF/CloudFront.

**C — (not a never category).** Criterion C — third-party SaaS/telephony
bridges — was downgraded from "never" to "deferred" by owner decision
2026-08-03 and no longer contributes to this list. Its 24 services are
enumerated in §4.2c below; the criterion itself is defined in §3.3.

**D — Pure console/internal-plumbing product with no customer dev-loop API surface (6)**

`signin`, `uxc`, `repostspace`, `launch-wizard`, `lambda-core`, `lambda-microvms`

Justification pattern: `signin` and `uxc` are AWS Console mechanics (federated
sign-in URLs, UI preference toggles), not resources an SDK/CDK app provisions.
`launch-wizard` is a guided deployment wizard whose output is ordinary
EC2/RDS/CFN resources already covered elsewhere. `lambda-core` and
`lambda-microvms` (per [aws-api-operation-coverage.md §4.1](./aws-api-operation-coverage.md#41-generated-operation-manifest),
`lambda-core` is confirmed as a distinct modeled service from `lambda`) read as
AWS-internal Firecracker/microVM control-plane APIs, not customer-facing
surface — flagged low-confidence in §7.

**E — Deprecated, sunset, or de facto frozen (16)**

`app-mesh` (closed to new customers 2024-09-24, full discontinuation
2026-09-30), `cloud9`, `cloudsearch`, `cloudsearch-domain`, `codecommit` (all
three closed to new customers, no announced feature investment), `workmail`,
`workmailmessageflow` (AWS ended WorkMail, 2026-04), `cognito-sync` (frozen
since ~2017, superseded by Cognito Sync's own deprecation in favor of AppSync/
Amplify DataStore patterns), `simpledbv2` (never formally deprecated but no
AWS announcement in over a decade, console-inaccessible), `machine-learning`
(Amazon ML, shut down 2019), `data-pipeline` (maintenance mode for years, AWS
steers users to Step Functions/Glue/MWAA), `swf` (legacy, AWS has recommended
Step Functions as the replacement for years — moderate confidence, no hard EOL
date found), `forecast`, `forecastquery` (discontinued 2024), `frauddetector`
(closed to new customers 2025-11-07), `mturk` (AWS is closing Mechanical Turk
to new customers 2026-07-30; it would otherwise sit in the deferred C bucket).

Sources: web search against AWS's own announcements, current as of 2026-08-03
(see §7 for citations). This category is the one most likely to drift — see §6.

### 4.2c Deferred (criterion C, §3.3): the 24

Not never-501 — deferred indefinitely, bottom of the priority queue:

`connect`, `connect-contact-lens`, `connectcampaigns`, `connectcampaignsv2`,
`connectcases`, `connecthealth`, `connectparticipant`, `chime`,
`chime-sdk-identity`, `chime-sdk-media-pipelines`, `chime-sdk-meetings`,
`chime-sdk-messaging`, `chime-sdk-voice`, `pinpoint`, `pinpoint-email`,
`pinpoint-sms-voice`, `pinpoint-sms-voice-v2`, `socialmessaging`, `appfabric`,
`appflow`, `appintegrations`, `b2bi`, `chatbot`, `wickr`

Pattern: each bridges to a real external network — PSTN telephony, SMS/carrier
gateways, WhatsApp/Meta, a Slack/Teams webhook, EDI trading partners. A
capture-style mock in the SES mould (record the outbound call/SMS/message as
inspectable local state instead of delivering it) is conceivable for all of
them, which is exactly why they are deferred rather than never — but no planned
wave includes them, and `connect` alone is 379 modeled operations, so nothing
here moves without a concrete user need. `pinpoint-email` additionally
duplicates functionality already covered by registered `ses`.

### 4.3 Registered: the 50 Overcast services (52 manifest identities)

These are already past Tier 0 by construction (`internal/services/` has a
package). Listed here as the explicit registered set per §4.1; further
tiering within each service is [full-emulation-priority.md](./full-emulation-priority.md)'s
job, not this document's. Op counts are from `internal/capabilities/all.gen.go`
(ground truth per §2.2), not STATUS.md.

| Service | Ops | Service | Ops | Service | Ops | Service | Ops |
| --- | --- | --- | --- | --- | --- | --- | --- |
| acm | 7 | ec2 | 72 | lambda | 48 | scheduler | 12 |
| apigateway | 105 | ecr | 20 | msk | 29 | secretsmanager | 21 |
| appconfig | 12 | ecs | 48 | opensearch | 8 | ses | 42 |
| appconfigdata | 3 | efs | 31 | organizations | 1 | shield | 5 |
| appregistry | 21 | eks | 52 | pipes | 5 | sns | 24 |
| appsync | 82 | elasticache | 24 | rds | 33 | sqs | 21 |
| athena | 8 | elbv2 | 15 | route53 | 25 | ssm | 18 |
| autoscaling | 19 | eventbridge | 28 | s3 | 47 | stepfunctions | 5 |
| backup | 9 | firehose | 6 | | | sts | 11 |
| bedrock | 2 | glue | 8 | | | transfer | 10 |
| cloudformation | 48 | iam | 61 | | | waf | 4 |
| cloudfront | 89 | kinesis | 17 | | | |
| cloudtrail | 9 | kms | 32 | | | |
| cloudwatch | 12 | | | | | |
| cloudwatch-logs | 19 | | | | | |
| cognito | 67 | | | | | |
| dynamodb | 19 | | | | | |
| dynamodbstreams | 4 | | | | | |

### 4.4 Inert-candidate: everything else (default bucket)

The remaining 255 manifest services default to inert-candidate. No
service-by-service justification is required to sit in this bucket — the
burden of proof is on §4.2 (never) and, later, on
[inert-tier-rollout.md](./inert-tier-rollout.md)'s own sequencing for *when*
each gets a Tier 1 package, and on [full-emulation-priority.md](./full-emulation-priority.md)
for *which* of them are worth Tier 2. Representative examples spanning the
rubric's near-misses, to make the boundary concrete:

- **ML/AI inference** (Rekognition, Comprehend, Textract, Translate, Polly,
  Transcribe, Lex, Kendra, Personalize, Bedrock's agent/runtime family) — hard
  to emulate with fidelity, not nonsensical to stub (§3.2 last row).
- **Data-plane/warehouse engines adjacent to already-registered ones**
  (Redshift, Neptune, DocumentDB, MemoryDB, Timestream, Keyspaces, DAX, DSQL) —
  same "Docker-backed engine" shape as already-registered RDS/ElastiCache.
- **Security/compliance products whose control plane is pure metadata**
  (GuardDuty, SecurityHub, Inspector, Detective, Macie, Config, AWS Config,
  Firewall Manager) — CDK commonly *enables* these as compliance-as-code even
  though their findings inherently need real telemetry to mean anything.
- **Network/DNS adjuncts to already-registered EC2/Route 53** (Global
  Accelerator, Route 53 Resolver/Profiles, VPC Lattice, Network Firewall) — same
  "CRUD over routing metadata, no real traffic" pattern the emulator already
  uses for CloudFront and Route 53 zones.
- **CI/CD and dev-tool services** (CodeBuild, CodeDeploy, CodePipeline,
  CodeArtifact, CodeConnections) — ordinary infrastructure-as-code targets, no
  different in kind from already-registered ECR/ECS.

### 4.5 Borderline cases

**IAM enforcement.** Not a scope question — `iam` is already registered and
registered (61 ops, "no enforcement" per STATUS.md, needs reverification
per §2.2 but the package unambiguously exists). The open question is whether
Overcast should ever *evaluate* Allow/Deny policy semantics against requests,
which is a Tier 1→2 depth question inside an already-in-scope service. It
belongs in [full-emulation-priority.md](./full-emulation-priority.md), not here.

**Organizations.** `organizations` is already registered (1 op, presumably a
minimal `DescribeOrganization`-shaped stub). Overcast is single-account by
design, but CDK bootstrap and multi-account IaC patterns query Organizations
for OU/account membership; faking a small org tree (a root, a few OUs, a few
member accounts) is ordinary metadata CRUD with no physical or commercial
dependency. Verdict: **not never** — worth deepening **at Tier 1** (fuller
org-tree metadata via the inert backfill machinery). Tier 2 multi-account
*semantics* remain a non-goal per
[full-emulation-priority.md](./full-emulation-priority.md) §7; the two
statements are compatible — one is about storing an org tree, the other about
emulating cross-account behaviour.

**Cost Explorer.** Genuinely tempting as a dev-loop tool — "what will this
stack cost" is a real question — but `cost-explorer`'s API surface reports
*actual historical spend against a real payer account*. There is no local
spend to explore, and fabricating numbers to answer `GetCostAndUsage` would
actively mislead a cost decision rather than merely be incomplete, which is a
different failure mode than every other inert stub in this document (an inert
stub returns *correct-shaped emptiness*, not *plausible-looking fiction*).
Verdict: **never-501** (category B), grouped in §4.2. If Overcast ever wants a
"what will this cost" dev-loop feature, it should be a purpose-built local
estimator (à la `cdk diff --cost` concepts), not a Cost Explorer emulation.

**CloudTrail locally.** `cloudtrail` is already registered (9 ops) — trail
CRUD is in scope and not up for debate. The open question is whether Overcast
should *emit real synthetic CloudTrail events* for every API call it handles
(useful for testing audit-log-driven pipelines locally). That is a Tier 2
feature decision inside an in-scope service, not a never-501 question — send
it to [full-emulation-priority.md](./full-emulation-priority.md).

**Config (`config-service`).** Not currently registered. AWS Config evaluates
resource compliance against real configuration history, but its control plane
(configuration recorders, rules, conformance packs) is plausible metadata CRUD
against Overcast's own state store — CDK commonly provisions Config rules
alongside application stacks as compliance-as-code. Verdict: **inert-candidate**,
not never; real evaluation logic is a full-emulation-priority question.

**Service Catalog (`service-catalog`).** Not currently registered. Its job is
distributing pre-approved CloudFormation templates for org self-service — pure
metadata over CFN templates the emulator already understands. CDK has Service
Catalog constructs. Verdict: **inert-candidate**, not never.

**AppRegistry (`service-catalog-appregistry`), already implemented.** Worth
calling out explicitly: AppRegistry shares a Smithy service family with the
never/commercial-adjacent Marketplace and PartnerCentral services, but it is
*already registered* (21 ops) precisely because its actual job — associating
applications with attribute groups and resources for tagging/cost-allocation
purposes — is ordinary metadata CDK/CFN provisions routinely. It is the
concrete proof that family adjacency to a "commercial" product does not by
itself veto a service; the rubric criteria (§3) are evaluated per-service, not
per-family label.

## 5. Mechanics of "never"

### 5.1 Routing: already free, no code required

Mechanically, "never" for a service requires **zero new code**. As soon as
[aws-api-operation-coverage.md](./aws-api-operation-coverage.md)'s A1–A4 phases
are live (git history shows the baseline reaching zero failures in #462/#463),
every operation in the 426-service manifest that has no claiming service
package already receives the correct protocol-specific `501`
(`NotImplementedJSON`/`NotImplementedQueryXML`/`NotImplementedEC2QueryXML`/
`NotImplementedXML`) from the shared registry fallback, never an S3 response.
A never-501 verdict is therefore implemented by **absence**: do not create an
`internal/services/<name>/` package for it, ever. This document's only real
addition is the editorial classification and the presentation layer below —
the routing safety net already exists and needs nothing further.

### 5.2 compat: exclude, don't accumulate

`compat/AGENTS.md` §Baseline & uniformity treats `unimplemented` (`501`) as a
legitimate permanent resting state for a **roadmapped** gap — "a real, tracked
gap in Overcast," ranked above `fail` and never blocking CI. That model fits
services waiting their turn, not services that will never be built.

**Recommendation: never-listed services should not accumulate hand-written
`compat/` SDK-suite fixtures at all.** Writing a boto3/CLI/dotnet-sdk test that
asserts `groundstation.RegisterAgent` returns `501` forever adds a permanent,
unmovable baseline row for no future signal — it will never flip to `pass`,
and its presence in `baseline.json` gives a false impression of "on the
roadmap." The routing guarantee in §5.1, plus [aws-api-operation-coverage.md §7](./aws-api-operation-coverage.md#7-testing-strategy)'s
generated per-operation discriminator corpus (which already asserts every
manifest operation reaches an owner or a non-S3 `501`), is the correct and
sufficient test tier for never-listed services. Reserve `compat/` suite
fixtures for services genuinely on an implementation trajectory — i.e.,
everything in §4.3 and §4.4, never §4.2.

If a compat suite fixture for a never-listed service already exists (unlikely,
since none of the 95 have an `internal/services/` package to test against),
delete it rather than baseline it — same logic as never registering the
package in the first place.

### 5.3 Presentation: a machine-readable marker, not prose

STATUS.md, the capability tables, and the web UI currently have only one way to
say "not implemented": a `501`/absent row, indistinguishable from "nobody has
gotten to this yet." That conflates two different promises to a user — "file
an issue, we'll consider it" vs. "this will never happen, don't ask." Recommend
a small, hand-curated, checked-in policy file, analogous in spirit to the
explicit alias table in [aws-api-operation-coverage.md §4.1](./aws-api-operation-coverage.md#41-generated-operation-manifest)
("explicit data, never inferred from a naming rule"):

```go
// internal/awsapi/policy.go — hand-maintained, not generated.
// Records services this repository has decided never to implement, and why.
package awsapi

type NeverReason string

const (
    ReasonPhysicalInfra   NeverReason = "physical-external-infra"   // rubric A
    ReasonCommercialOnly  NeverReason = "commercial-administrative" // rubric B
    ReasonConsoleOnly     NeverReason = "console-internal-only"     // rubric D
    ReasonDeprecated      NeverReason = "deprecated-upstream"       // rubric E
)

type NeverEntry struct {
    Reason  NeverReason
    Note    string // one-line justification, shown in docs/UI
    Since   string // date this policy entry was added, e.g. "2026-08-03"
}

// NeverEmulated maps manifest service keys (see manifest.gen.go's Service
// field) to why this repository will not build them. Absence from this map
// means "not yet," not "never" — see docs/plans/services-never-emulated.md.
var NeverEmulated = map[string]NeverEntry{
    "groundstation": {ReasonPhysicalInfra, "requires a real satellite ground station", "2026-08-03"},
    // ... one entry per §4.2 row ...
}
```

This is deliberately **hand-written**, not generated from Smithy — it is an
editorial decision like the alias table, not a fact derivable from the model.
Consumers:

1. **`capgen --write-docs` / STATUS.md generation** renders a distinct "Not
   applicable to a local emulator" section for `NeverEmulated` entries,
   separate from the roadmap/gap section, so a reader does not mistake a
   permanent non-goal for a backlog item.
2. **`stub-report`** (already the manifest/implementation reconciliation tool
   per [aws-api-operation-coverage.md §4.4](./aws-api-operation-coverage.md#44-status-stays-separate))
   annotates never-listed services distinctly in its coverage report instead of
   counting them as undifferentiated gaps.
3. **The web UI's capability explorer** shows a distinct badge (e.g. "N/A" with
   the one-line reason on hover) instead of the same "not implemented" styling
   used for roadmap gaps.
4. A small CI check can assert every §4.2 service key that appears in the
   manifest also appears in `NeverEmulated`, and vice versa — keeping the
   policy file and this document from drifting apart the way STATUS.md's prose
   drifted from its own generated block (§2.2).

## 6. Revisit policy

This is a policy default, not a constitution. A service leaves §4.2 (never)
when **any** of the following is shown in a PR description or linked issue:

- **Concrete user demand**: a filed issue describing a real local dev/test
  workflow blocked by the `501`, not a hypothetical.
- **CDK/CloudFormation starts calling it in ordinary deploys**: if a future AWS
  CDK release adds a construct that provisions the service as a normal
  dependency of application infrastructure (the veto in §3.2), the never
  verdict is void immediately, no debate needed.
- **The upstream deprecation reverses**: for §4.2's category-E entries
  specifically, if AWS reopens the service to new customers or reverses an
  EOL, re-run this document's rubric against it fresh rather than assuming the
  original verdict still holds.
- **It turns out to gate another in-scope service's API**, the way Shield gates
  ordinary WAF/CloudFront deploys — discovered via a real `fail`/error in a
  compat suite for an *in-scope* service that traces back to the never-listed
  one.

Moving a service off the list means: remove its `NeverEmulated` entry, and it
falls through to the inert-candidate default (§4.4) — it does not jump straight
to registered. Moving a service *onto* the list (a new deprecation, a
service AWS pulls from new-customer onboarding) follows the same PR-with-citation
bar as any other entry in §4.2.

## 7. Open questions / low-confidence calls

Flagged explicitly so a reviewer can correct them rather than inheriting them
silently:

- **`interconnect`** (13 ops) — grouped under rubric A on the assumption it is
  Direct-Connect-adjacent physical cross-connect plumbing; the public AWS docs
  name for this SDK ID was not independently confirmed.
- **`lambda-core`, `lambda-microvms`** — grouped under rubric D as AWS-internal
  Firecracker/microVM control-plane APIs; if either turns out to be genuinely
  customer-facing, they move to inert-candidate immediately (§3.2's dev-loop
  veto), not through a full revisit cycle.
- **`swf`** — grouped under rubric E on AWS's long-standing "use Step Functions
  instead" guidance, but no hard EOL/no-new-customers date was found (unlike
  App Mesh, Cloud9, CloudSearch, CodeCommit, Forecast, Fraud Detector, WorkMail,
  Mechanical Turk, all of which have confirmed dates as of the 2026-08-03
  research pass for this document).
- **`cognito-sync`, `simpledbv2`** — "de facto frozen for a decade" rather than
  a formally announced sunset; kept in §4.2 because forward investment is
  clearly zero, but these are the softest calls in category E.
- **Category-B members that are debatable rather than clear-cut**: `pricing`
  (public price-list lookups have occasional CI/cost-estimation utility, but
  no state to CRUD) and `trustedadvisor` (advisory findings need real account
  data to mean anything, same shape as the ML-inference counter-argument in
  §3.2 — kept in never rather than inert-candidate because, unlike ML
  inference, there is no coherent deterministic-canned-response shape for "is
  my real account well-architected").

## 8. Implementation checklist

Small, mostly-mechanical; each step is an independently reviewable PR.

| Step | Contents | Effort | Acceptance gate |
| --- | --- | --- | --- |
| **N1** — policy file | `internal/awsapi/policy.go` (§5.3): `NeverEmulated` map with one entry per §4.2 row, reason codes A/B/D/E, note, since-date. CI test asserting every entry's key exists in the manifest and no entry collides with a registered key or alias. | S | Test green; the map's row count equals §4.2's 95 and a comment cross-links this document. |
| **N2** — consumers | `capgen --write-docs` renders a distinct "Not applicable to a local emulator" STATUS.md section; `stub-report` annotates never-listed services separately from roadmap gaps; web UI capability explorer shows an "N/A" badge with the reason on hover. | M | A reader of STATUS.md / the UI can tell "never" from "not yet" without opening this document. |
| **N3** — compat carve-out | Amend [compat/AGENTS.md](../../compat/AGENTS.md) principle 3 ("tests cover all services") with the §5.2 exception: never-listed services get no suite fixtures; the server-side generated 501 corpus ([aws-api-operation-coverage.md §7](./aws-api-operation-coverage.md)) is their test tier. Land together with the generated-`suites`-scoping amendment from [compat-coverage-modelgen.md](./compat-coverage-modelgen.md) §3.6 so the file is edited once. | S | Amendment merged with reviewer sign-off; no compat fixture exists for any never-listed service. |
| **N4** — drift guard | CI check that this document's §4.2 lists and `NeverEmulated` stay in sync (parse the doc's backticked keys, diff against the map). | S | Deliberately breaking one side fails CI. |
