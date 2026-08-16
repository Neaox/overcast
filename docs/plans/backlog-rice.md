# Backlog RICE — every open issue scored, and what the scores change

> Status: applied, 2026-08-17. Owner: TBD.
> Scope: all 321 open issues as of 2026-08-17. Companion to
> [full-emulation-priority.md](./full-emulation-priority.md), which ranks the Tier-2
> programme only; this document ranks *everything* on the same evidence base.
> Priority labels on GitHub and the service tiers in
> [docs/dev/compatibility/](../dev/compatibility/) were updated to match, in the
> commit that introduced this file.

## 1. Why this exists

The backlog had 106 open issues labelled `priority/p0` — a third of everything open.
A third of a backlog cannot be critical, and the label had stopped carrying
information. Tracing where those labels came from explains why, and the explanation
is not carelessness:

- **190 of the 321 issues are compat-audit issues** (`#3`–`#198`): 49 per-service
  baseline trackers and 141 single-scenario children generated from
  `docs/dev/compatibility/services/*.yaml`. Each one inherited its `priority/pN`
  label verbatim from the **service-level** `priority:` field in that YAML.
- That field answers *"how important is this service?"* Applied to an issue it
  reads as *"how important is this task?"* — a different question with a different
  answer. `AppConfig compat: Add hosted configuration version operations` (#58) is
  `p0` because AppConfig was tiered `p0`, not because the task is urgent. It scores
  **RICE 1**, the joint lowest in the backlog.
- Meanwhile the highest-value items were **unlabelled or under-labelled**, because
  they were filed by hand as work was discovered rather than generated:
  `#512` (partial-batch failures honoured nowhere) had **no priority label at all**
  and scores **560**; `#521` (CDK drops the policies off every IAM role) sat at `p1`
  and scores **1124**, the top of the backlog.

So the ranking inverted at both ends. Scoring every issue on one scale fixes that.

## 2. The model

RICE, in the standard form:

```
RICE = (Reach x Impact x Confidence) / Effort
```

This is not a new framework competing with `full-emulation-priority.md` §2 — it is
that rubric's six factors regrouped into RICE's four, so the two documents rank the
same work the same way:

| RICE factor | `full-emulation-priority.md` §2 factor |
|---|---|
| Reach | `usage` x `leverage` — who is affected, and how many architectures it unblocks |
| Impact | `fit` and `risk` — does correct behaviour matter here, and does getting it half-right mislead |
| Confidence | `dep_ready`, plus the evidence quality actually present in the issue |
| Effort | `cost`, on the same S/M/L scale |

### Reach

An index of 0–1000: developers per quarter whose local loop touches this code path.
Computed as `service_reach x path_factor`.

`service_reach` is the share of Overcast users who touch the service **at all**,
anchored on the evidence base in `full-emulation-priority.md` §4 (npm dependent
counts, LocalStack's free-vs-paid tier split, Datadog's serverless adoption
figures), adjusted for the fact that CDK/CloudFormation is how most stacks reach
the emulator at all:

| Service | Reach | Service | Reach | Service | Reach |
|---|--:|---|--:|---|--:|
| s3 | 900 | ecr | 250 | glue | 70 |
| cloudformation | 850 | rds | 250 | eks | 70 |
| lambda | 800 | cognito | 220 | efs | 60 |
| iam | 750 | kinesis | 200 | cloudtrail | 60 |
| dynamodb | 600 | cloudwatch | 200 | acm | 60 |
| sqs | 500 | elbv2 | 180 | bedrock | 60 |
| cloudwatch-logs | 450 | dynamodbstreams | 150 | pipes | 60 |
| sns | 400 | cloudfront | 150 | msk | 50 |
| sts | 400 | ses | 130 | appconfig | 40 |
| apigateway | 400 | elasticache | 120 | backup | 40 |
| eventbridge | 350 | scheduler | 120 | organizations | 30 |
| ec2 | 350 | route53 | 120 | waf | 30 |
| secretsmanager | 300 | appsync | 90 | appconfigdata | 25 |
| ssm | 300 | firehose | 90 | transfer | 20 |
| ecs | 300 | autoscaling | 80 | appregistry | 15 |
| stepfunctions | 250 | opensearch | 80 | shield | 10 |
| kms | 250 | athena | 70 |  |  |

Cross-cutting surfaces use the same index: `protocol` 700, `docs` 400, `storage` 300, `web-ui` 250, `devex` 100, `compat` 80.
`protocol` is high because the router, the manifest and the wire format sit under
every service; `devex` is low because its audience is contributors, not users — real,
but not the same population.

`path_factor` is the share of *that service's* users who hit the specific path the
issue is about. A CDK property that every stack sets scores 0.4–0.75; a single
`ListObjectsV2` boundary-validation case scores 0.05.

**One correction worth recording**, because it inverted a whole class on the first
pass: every CDK property-drop issue carries `service/cloudformation` alongside its
target service. Taking the maximum over an issue's service labels therefore gave all
of them CloudFormation's reach of 850, which put `scheduler: properties dropped`
(#537) at the top of the backlog. The audience for that issue is people who use
**Scheduler** via CDK, not everyone who uses CDK. Reach for these is now taken from
the target service, and CloudFormation's own reach applies only to issues about
CloudFormation itself.

### Impact

`3` massive, `2` high, `1` medium, `0.5` low, `0.25` minimal. Anchored on
`full-emulation-priority.md` §2.1, which is the sharpest impact distinction this
repo has:

- **3** — silent wrong behaviour on a path applications depend on. §2.1 shape 2:
  the thing provisions clean, reports success, and does not work.
- **2** — a real divergence a user will hit, but one that fails visibly, or a silent
  one on a narrower path.
- **1** — correctness or honesty gap with a workaround, or a doc error that
  misdirects a real decision.
- **0.5** — fidelity polish; nothing is misled today.
- **0.25** — writing down a gap that has already been deliberately decided.

That last band is where most of the compat-audit backlog lands, and it is the single
biggest reason those issues drop. `Cognito compat: Device SRP verifier cryptographic
proof validation is intentionally partial` (#95) is not a defect report. It is a note
that a decision already taken should be documented. Worth doing; not worth `p0`.

### Confidence

`1.0` where the issue cites in-repo evidence (file and line, or a reproduction),
`0.8` where the scope is well specified but the behaviour is inferred, `0.5` where
the issue is explicitly unverified — `needs-aws-verification`, `needs-info`, or a
single unreproduced sighting.

### Effort

Person-weeks, from the existing `effort/*` labels where present and from the issue's
own definition of done where not: `small` 0.5, `medium` 1.5, `large` 4.0.

### Bands

| Band | RICE | Count (was) |
|---|---|---|
| `priority/p0` | >= 300 | **14** (was 106) |
| `priority/p1` | 100-299 | **38** (was 81) |
| `priority/p2` | 25-99 | **84** (was 62) |
| `priority/p3` | < 25 | **185** (was 27) |

45 issues carried no priority label at all and now have one. 278 of 321 issues change band.

## 3. What the scores actually change

### 3.1 The CDK property-drop sweep is the top of the backlog

Seven of the top fourteen are `#521`–`#545`/`#710` — properties that arrive from CDK
and are parsed into nothing. They share a shape that maximises RICE: the reach is
whoever uses the service (these are default CDK properties, not exotic ones), the
impact is 2–3 because the stack deploys clean and the resource is silently wrong,
confidence is 1.0 because each issue cites the exact handler line that drops the
property, and effort is small-to-medium because the receiving service already
implements the feature — the fix is passing a value that is already there.

`#521` (IAM) tops the backlog at **1124**: `role.addToPolicy(...)` and
`addManagedPolicy(...)` render to nothing, and every CDK stack has roles.

### 3.2 The compat-audit backlog is real work, but it is not p0 work

All 190 drop. 70 go `p0` -> `p3` and 30 go `p0` -> `p2`. The 55 Cognito children are
the clearest case: Cognito's own reach is 220, each child covers one operation's edge
case (path factor ~0.04–0.08), and roughly half are explicitly labelled
"intentionally partial" — impact 0.25. Typical score: **1–4**.

This is not an argument to close them. They are the mechanism by which support
claims stay honest, and honesty about coverage is something this project has chosen
to spend on. It is an argument that they should not outrank a silently-broken
delivery path, which at `p0` they did.

### 3.3 Docs issues score higher than their labels suggest

`#741` (**720**), `#742` (**560**) and `#743` (**480**) all move up. The reasoning is
that these are not tidiness: `#741` tells a user evaluating Overcast that `cdk deploy`
supports ~50 resource types when the real number is 132, and `#742`'s stale table
tells a LocalStack migrant that five services they depend on are unimplemented when
all five work. Both are read at the moment someone decides whether to adopt the
project, and both are wrong in the direction that loses the user. `#743` omits the
three environment variables that gate remote access to the MCP endpoint, which an
operator cannot make a decision about from documentation that does not mention them.

### 3.4 Small, verified, high-blast-radius fixes rise

`#733` (**720**, S3 -> SNS notifications never fire), `#735` (**560**, the SigV4
warning says validation is unimplemented while it rejects with 403), `#783` (**512**,
a cancelled update discards applied resource records) and `#976` (**420**, an
unimplemented `ListTags` answers 200 for every service) are all `effort/small` with
confidence 1.0. They are the cheapest real wins in the backlog and none was above
`p2`.

### 3.5 Where the model disagrees with itself, and I left it disagreeing

`#864` (make the manifest the enforced source of truth) scores **192** — `p1`, not
`p0` — because its effort is `large` and RICE divides by effort. That undersells it:
the issue names ten separate faults from one release cycle that the enforcement would
have caught, so its real value is preventing a recurring class rather than fixing one
path. RICE has no term for "stops the same bug being refiled monthly". The same
applies to `#883` (**98**) and `#749` (**80**), which are the equivalent argument for
request shapes and for documentation facts respectively.

Recorded rather than corrected by fiat: inflating a score to make the arithmetic
agree with a hunch defeats the point of scoring. Read those three as
higher than their rank.

## 4. Top 30

| # | Issue | RICE | R | I | C | E | Was | Now |
|---|---|--:|--:|--:|--:|--:|---|---|
| [#521](https://github.com/Neaox/overcast/issues/521) | iam: properties dropped between CDK and the emulator | **1124** | 562 | 3.0 | 1.0 | 1.5 | `p1` | `p0` |
| [#733](https://github.com/Neaox/overcast/issues/733) | s3: bucket notifications never deliver to SNS — TopicConfigurations is stored but never ... | **720** | 180 | 2.0 | 1.0 | 0.5 | `p2` | `p0` |
| [#741](https://github.com/Neaox/overcast/issues/741) | docs: CloudFormation resource-type coverage understated in 5 docs (132 actual, ~50 claim... | **720** | 180 | 2.0 | 1.0 | 0.5 | `p2` | `p0` |
| [#512](https://github.com/Neaox/overcast/issues/512) | lambda/pipes: ReportBatchItemFailures is accepted everywhere and honoured nowhere | **560** | 280 | 3.0 | 1.0 | 1.5 | `none` | `p0` |
| [#735](https://github.com/Neaox/overcast/issues/735) | OVERCAST_SIGV4_VALIDATE: startup warning and docs claim it is unimplemented, but validat... | **560** | 140 | 2.0 | 1.0 | 0.5 | `p2` | `p0` |
| [#742](https://github.com/Neaox/overcast/issues/742) | docs: the LocalStack migration guide's known-gaps table is stale in every row | **560** | 140 | 2.0 | 1.0 | 0.5 | `p2` | `p0` |
| [#710](https://github.com/Neaox/overcast/issues/710) | iam: CloudFormation Create handlers drop principal-attachment properties, and adding the... | **551** | 413 | 2.0 | 1.0 | 1.5 | `none` | `p0` |
| [#783](https://github.com/Neaox/overcast/issues/783) | cloudformation: a cancelled update discards the resource records it already applied | **512** | 128 | 2.0 | 1.0 | 0.5 | `p2` | `p0` |
| [#743](https://github.com/Neaox/overcast/issues/743) | docs: configuration reference omits ~25 env vars, including the MCP remote-access controls | **480** | 120 | 2.0 | 1.0 | 0.5 | `p2` | `p0` |
| [#484](https://github.com/Neaox/overcast/issues/484) | Tier 2 full-emulation programme: prioritized backlog | **453** | 340 | 2.0 | 1.0 | 1.5 | `p1` | `p0` |
| [#976](https://github.com/Neaox/overcast/issues/976) | aws backup list-tags answers HTTP 200 {"tags":{}} from another service's /tags/{resource... | **420** | 105 | 2.0 | 1.0 | 0.5 | `none` | `p0` |
| [#528](https://github.com/Neaox/overcast/issues/528) | apigateway: properties dropped between CDK and the emulator | **320** | 160 | 2.0 | 1.0 | 1.0 | `p1` | `p0` |
| [#760](https://github.com/Neaox/overcast/issues/760) | cloudformation: tell users which resources in their stack are stubs or inert, in the dep... | **318** | 298 | 2.0 | 0.8 | 1.5 | `p2` | `p0` |
| [#529](https://github.com/Neaox/overcast/issues/529) | ec2: properties dropped between CDK and the emulator | **316** | 158 | 2.0 | 1.0 | 1.0 | `p1` | `p0` |
| [#183](https://github.com/Neaox/overcast/issues/183) | SNS compat: Publish reviewed against AWS docs; fix destination selection, MessageStructu... | **288** | 80 | 2.0 | 0.9 | 0.5 | `p1` | `p1` |
| [#523](https://github.com/Neaox/overcast/issues/523) | dynamodb: properties dropped between CDK and the emulator | **240** | 120 | 2.0 | 1.0 | 1.0 | `p1` | `p1` |
| [#740](https://github.com/Neaox/overcast/issues/740) | docs: four factual errors in user-facing docs (console port, IAM enforcement, startup ta... | **240** | 120 | 1.0 | 1.0 | 0.5 | `p2` | `p1` |
| [#785](https://github.com/Neaox/overcast/issues/785) | state: HybridStore.Set keeps every written value in memory regardless of tier | **240** | 180 | 2.0 | 1.0 | 1.5 | `p2` | `p1` |
| [#440](https://github.com/Neaox/overcast/issues/440) | compat: baseline auto-promotion cannot push to main since required-check enforcement (#393) | **224** | 56 | 2.0 | 1.0 | 0.5 | `none` | `p1` |
| [#714](https://github.com/Neaox/overcast/issues/714) | [priority:P2] route EC2's DependencyViolation through this sentinel too — AWS::EC2::Secu... | **224** | 70 | 2.0 | 0.8 | 0.5 | `none` | `p1` |
| [#715](https://github.com/Neaox/overcast/issues/715) | [priority:P2] add a charset pattern here so services can enforce their model's TagKey/Ta... | **224** | 140 | 1.0 | 0.8 | 0.5 | `none` | `p1` |
| [#537](https://github.com/Neaox/overcast/issues/537) | scheduler: properties dropped between CDK and the emulator | **216** | 54 | 2.0 | 1.0 | 0.5 | `p2` | `p1` |
| [#754](https://github.com/Neaox/overcast/issues/754) | ec2: typed dispatch is disabled — 69 typed operations are unreachable, filters ignored a... | **211** | 158 | 2.0 | 1.0 | 1.5 | `p2` | `p1` |
| [#532](https://github.com/Neaox/overcast/issues/532) | ssm: properties dropped between CDK and the emulator | **210** | 105 | 1.0 | 1.0 | 0.5 | `p1` | `p1` |
| [#538](https://github.com/Neaox/overcast/issues/538) | sqs: properties dropped between CDK and the emulator | **200** | 100 | 1.0 | 1.0 | 0.5 | `p2` | `p1` |
| [#864](https://github.com/Neaox/overcast/issues/864) | awsapi: make the pinned manifest the enforced source of truth for AWS services and opera... | **192** | 385 | 2.0 | 1.0 | 4.0 | `none` | `p1` |
| [#39](https://github.com/Neaox/overcast/issues/39) | S3 Baseline Compat | **178** | 315 | 1.0 | 0.85 | 1.5 | `p0` | `p1` |
| [#866](https://github.com/Neaox/overcast/issues/866) | web/cloudwatch: the log filter placeholder is mojibake — an em dash decoded as cp1252 | **176** | 88 | 1.0 | 1.0 | 0.5 | `none` | `p1` |
| [#606](https://github.com/Neaox/overcast/issues/606) | CloudFormation: custom resources resolve secure dynamic references, and get mismatched o... | **170** | 85 | 1.0 | 1.0 | 0.5 | `p2` | `p1` |
| [#13](https://github.com/Neaox/overcast/issues/13) | CloudFormation Baseline Compat | **169** | 298 | 1.0 | 0.85 | 1.5 | `p0` | `p1` |

## 5. Service tiers in the compatibility trackers

The `priority:` field in `docs/dev/compatibility/matrix.yaml` and
`docs/dev/compatibility/services/*.yaml` is the thing that generated the label
problem in §1, so it was re-tiered from the same reach index rather than left to
regenerate the old distribution. It keeps its original meaning — *how much does this
service's compatibility matter* — and is deliberately **not** the same number as any
individual issue's RICE score.

26 of 49 services move:

| Service | Was | Now | | Service | Was | Now |
|---|---|---|---|---|---|---|
| `cloudwatch-logs` | p1 | **p0** | | `sns` | p1 | **p0** |
| `ec2` | p0 | **p1** | | `ssm` | p0 | **p1** |
| `ecs` | p0 | **p1** | | `cognito` | p0 | **p1** |
| `cloudfront` | p0 | **p1** | | `ses` | p0 | **p2** |
| `scheduler` | p1 | **p2** | | `route53` | p1 | **p2** |
| `appsync` | p0 | **p2** | | `autoscaling` | p1 | **p2** |
| `opensearch` | p0 | **p2** | | `eks` | p1 | **p2** |
| `cloudtrail` | p1 | **p2** | | `bedrock` | p0 | **p2** |
| `pipes` | p1 | **p2** | | `msk` | p1 | **p3** |
| `appconfig` | p0 | **p3** | | `backup` | p1 | **p3** |
| `organizations` | p2 | **p3** | | `waf` | p1 | **p3** |
| `appconfigdata` | p2 | **p3** | | `transfer` | p1 | **p3** |
| `appregistry` | p1 | **p3** | | `shield` | p2 | **p3** |

The moves that matter most, in both directions:

- **Up.** `cloudwatch-logs` and `sns` to `p0`. Both are near-universal in a
  serverless local loop — every Lambda writes logs, and SNS is the default fan-out
  primitive — and both sat at `p1` while AppConfig sat at `p0`.
- **Down.** `appconfig`, `bedrock`, `opensearch`, `appsync` and `ses` all leave `p0`.
  None is close to universal in local development. `appsync` in particular carried 7
  issues at `p0` on the strength of a service tier that the project's own Tier-2
  ranking never supported.
- `cognito` to `p1` is the largest single effect: it moves 55 issues off `p0`.

## 6. Keeping this current

Same triggers as `full-emulation-priority.md` §8, plus one specific to this file:

1. **New issues get scored on filing.** The scoring inputs are three numbers and a
   sentence; deferring it is how the backlog got back to 106 `p0`s last time.
2. **The generated compat issues take their label from the service tier, and that is
   fine** — as long as the tier means "service importance" and nobody reads a
   generated `p2` as a considered per-issue judgement. This document is the record
   that they are different claims.
3. **Re-score, do not re-read.** Reach estimates move as the ecosystem moves and as
   Overcast's own coverage changes. A service that becomes usable locally for the
   first time gains reach it did not have.
4. This file is a snapshot of a scoring *method* plus one application of it. When it
   is re-derived, update it in place.

## 7. Full backlog, scored

All 321 open issues, highest first. `R`/`I`/`C`/`E` are the RICE inputs;
`Was`/`Now` are the priority label before and after.

| # | Issue | RICE | R | I | C | E | Was | Now | Basis |
|---|---|--:|--:|--:|--:|--:|---|---|---|
| [#521](https://github.com/Neaox/overcast/issues/521) | iam: properties dropped between CDK and the emulator | 1124 | 562 | 3.0 | 1.0 | 1.5 | `p1` | `p0` | CDK role.addToPolicy/addManagedPolicy renders to nothing; every CDK stac... |
| [#733](https://github.com/Neaox/overcast/issues/733) | s3: bucket notifications never deliver to SNS — TopicConfigurations is store... | 720 | 180 | 2.0 | 1.0 | 0.5 | `p2` | `p0` | S3 to SNS notifications never fire; SQS/Lambda/EventBridge all work |
| [#741](https://github.com/Neaox/overcast/issues/741) | docs: CloudFormation resource-type coverage understated in 5 docs (132 actua... | 720 | 180 | 2.0 | 1.0 | 0.5 | `p2` | `p0` | coverage understated 2.5x in 5 docs; users told cdk deploy will not work... |
| [#512](https://github.com/Neaox/overcast/issues/512) | lambda/pipes: ReportBatchItemFailures is accepted everywhere and honoured no... | 560 | 280 | 3.0 | 1.0 | 1.5 | `none` | `p0` | reportBatchItemFailures accepted everywhere, honoured nowhere; partial-b... |
| [#735](https://github.com/Neaox/overcast/issues/735) | OVERCAST_SIGV4_VALIDATE: startup warning and docs claim it is unimplemented,... | 560 | 140 | 2.0 | 1.0 | 0.5 | `p2` | `p0` | startup warning and docs say SigV4 validation is unimplemented while it ... |
| [#742](https://github.com/Neaox/overcast/issues/742) | docs: the LocalStack migration guide's known-gaps table is stale in every row | 560 | 140 | 2.0 | 1.0 | 0.5 | `p2` | `p0` | LocalStack migration known-gaps table stale in every row; the doc evalua... |
| [#710](https://github.com/Neaox/overcast/issues/710) | iam: CloudFormation Create handlers drop principal-attachment properties, an... | 551 | 413 | 2.0 | 1.0 | 1.5 | `none` | `p0` | same class as #521 across role/user/managed-policy; blocked on matching ... |
| [#783](https://github.com/Neaox/overcast/issues/783) | cloudformation: a cancelled update discards the resource records it already ... | 512 | 128 | 2.0 | 1.0 | 0.5 | `p2` | `p0` | cancelled update discards applied resource records |
| [#743](https://github.com/Neaox/overcast/issues/743) | docs: configuration reference omits ~25 env vars, including the MCP remote-a... | 480 | 120 | 2.0 | 1.0 | 0.5 | `p2` | `p0` | config reference omits ~25 vars including the MCP remote-access controls |
| [#484](https://github.com/Neaox/overcast/issues/484) | Tier 2 full-emulation programme: prioritized backlog | 453 | 340 | 2.0 | 1.0 | 1.5 | `p1` | `p0` | the Tier 2 programme tracker itself |
| [#976](https://github.com/Neaox/overcast/issues/976) | aws backup list-tags answers HTTP 200 {"tags":{}} from another service's /ta... | 420 | 105 | 2.0 | 1.0 | 0.5 | `none` | `p0` | /tags/{arn} answers 200 {} for any service, signed or not; unimplemented... |
| [#528](https://github.com/Neaox/overcast/issues/528) | apigateway: properties dropped between CDK and the emulator | 320 | 160 | 2.0 | 1.0 | 1.0 | `p1` | `p0` | stage Variables live at execution time, dropped by CFN; integrations see... |
| [#760](https://github.com/Neaox/overcast/issues/760) | cloudformation: tell users which resources in their stack are stubs or inert... | 318 | 298 | 2.0 | 0.8 | 1.5 | `p2` | `p0` | stack reaches CREATE_COMPLETE with inert resources and nothing says so |
| [#529](https://github.com/Neaox/overcast/issues/529) | ec2: properties dropped between CDK and the emulator | 316 | 158 | 2.0 | 1.0 | 1.0 | `p1` | `p0` | VPC DNS + MapPublicIpOnLaunch; underlies ECS/RDS/Lambda-VPC |
| [#183](https://github.com/Neaox/overcast/issues/183) | SNS compat: Publish reviewed against AWS docs; fix destination selection, Me... | 288 | 80 | 2.0 | 0.9 | 0.5 | `p1` | `p1` | names concrete defects to fix |
| [#523](https://github.com/Neaox/overcast/issues/523) | dynamodb: properties dropped between CDK and the emulator | 240 | 120 | 2.0 | 1.0 | 1.0 | `p1` | `p1` | LSI table provisions then fails every index query |
| [#740](https://github.com/Neaox/overcast/issues/740) | docs: four factual errors in user-facing docs (console port, IAM enforcement... | 240 | 120 | 1.0 | 1.0 | 0.5 | `p2` | `p1` | four factual errors incl. the console port |
| [#785](https://github.com/Neaox/overcast/issues/785) | state: HybridStore.Set keeps every written value in memory regardless of tier | 240 | 180 | 2.0 | 1.0 | 1.5 | `p2` | `p1` | HybridStore.Set keeps every written value in memory; memory scales with ... |
| [#440](https://github.com/Neaox/overcast/issues/440) | compat: baseline auto-promotion cannot push to main since required-check enf... | 224 | 56 | 2.0 | 1.0 | 0.5 | `none` | `p1` | every baseline promotion is dropped; 50 had accumulated |
| [#714](https://github.com/Neaox/overcast/issues/714) | [priority:P2] route EC2's DependencyViolation through this sentinel too — AW... | 224 | 70 | 2.0 | 0.8 | 0.5 | `none` | `p1` | EC2 handlers swallow DependencyViolation and report the resource deleted |
| [#715](https://github.com/Neaox/overcast/issues/715) | [priority:P2] add a charset pattern here so services can enforce their model... | 224 | 140 | 1.0 | 0.8 | 0.5 | `none` | `p1` | tag charset pattern unenforced across every service sharing the validator |
| [#537](https://github.com/Neaox/overcast/issues/537) | scheduler: properties dropped between CDK and the emulator | 216 | 54 | 2.0 | 1.0 | 0.5 | `p2` | `p1` | timezone/StartDate/EndDate change when a schedule fires |
| [#754](https://github.com/Neaox/overcast/issues/754) | ec2: typed dispatch is disabled — 69 typed operations are unreachable, filte... | 211 | 158 | 2.0 | 1.0 | 1.5 | `p2` | `p1` | 69 typed EC2 ops unreachable, filters ignored, mutations dropped |
| [#532](https://github.com/Neaox/overcast/issues/532) | ssm: properties dropped between CDK and the emulator | 210 | 105 | 1.0 | 1.0 | 0.5 | `p1` | `p1` | Description/Tags dropped on SSM parameters |
| [#538](https://github.com/Neaox/overcast/issues/538) | sqs: properties dropped between CDK and the emulator | 200 | 100 | 1.0 | 1.0 | 0.5 | `p2` | `p1` | Tags + RedriveAllowPolicy; SQS handler otherwise good |
| [#864](https://github.com/Neaox/overcast/issues/864) | awsapi: make the pinned manifest the enforced source of truth for AWS servic... | 192 | 385 | 2.0 | 1.0 | 4.0 | `none` | `p1` | manifest is the closest thing to a definition of AWS and almost none of ... |
| [#39](https://github.com/Neaox/overcast/issues/39) | S3 Baseline Compat | 178 | 315 | 1.0 | 0.85 | 1.5 | `p0` | `p1` | service-wide baseline review tracker; scored as a programme item |
| [#866](https://github.com/Neaox/overcast/issues/866) | web/cloudwatch: the log filter placeholder is mojibake — an em dash decoded ... | 176 | 88 | 1.0 | 1.0 | 0.5 | `none` | `p1` | mojibake placeholder in a high-traffic view, survived 34 releases; sweep... |
| [#606](https://github.com/Neaox/overcast/issues/606) | CloudFormation: custom resources resolve secure dynamic references, and get ... | 170 | 85 | 1.0 | 1.0 | 0.5 | `p2` | `p1` | custom resources get resolved secrets and mismatched old/new property forms |
| [#13](https://github.com/Neaox/overcast/issues/13) | CloudFormation Baseline Compat | 169 | 298 | 1.0 | 0.85 | 1.5 | `p0` | `p1` | service-wide baseline review tracker; scored as a programme item |
| [#153](https://github.com/Neaox/overcast/issues/153) | IAM compat: Reconcile docs/capability wording with IAM enforcement tests. | 162 | 90 | 1.0 | 0.9 | 0.5 | `p0` | `p1` | named implementation/doc disagreement |
| [#748](https://github.com/Neaox/overcast/issues/748) | docs: operation-manifest.md silently omits 7 services (S3, Lambda, API Gatew... | 160 | 80 | 1.0 | 1.0 | 0.5 | `p3` | `p1` | operation manifest silently omits S3, Lambda, API Gateway, CloudFront, E... |
| [#761](https://github.com/Neaox/overcast/issues/761) | config: OVERCAST_HOST defaults to 0.0.0.0, while the docs say never expose O... | 160 | 40 | 2.0 | 1.0 | 0.5 | `p2` | `p1` | OVERCAST_HOST defaults to 0.0.0.0 while three docs say never expose it |
| [#32](https://github.com/Neaox/overcast/issues/32) | Lambda Baseline Compat | 159 | 280 | 1.0 | 0.85 | 1.5 | `p0` | `p1` | service-wide baseline review tracker; scored as a programme item |
| [#525](https://github.com/Neaox/overcast/issues/525) | kms: properties dropped between CDK and the emulator | 150 | 75 | 2.0 | 1.0 | 1.0 | `p1` | `p1` | key policy dropped on Create, applied on Update; load-bearing once #496 ... |
| [#534](https://github.com/Neaox/overcast/issues/534) | stepfunctions: properties dropped between CDK and the emulator | 150 | 75 | 2.0 | 1.0 | 1.0 | `p2` | `p1` | DefinitionSubstitutions unresolved; ASL placeholders reach the interpret... |
| [#460](https://github.com/Neaox/overcast/issues/460) | emulator: audit MSK, ECS tasks, and Lambda execution environments for the de... | 149 | 140 | 2.0 | 0.8 | 1.5 | `none` | `p1` | third instance of the delete-mid-start container leak; MSK and ECS unaud... |
| [#29](https://github.com/Neaox/overcast/issues/29) | IAM Baseline Compat | 148 | 262 | 1.0 | 0.85 | 1.5 | `p0` | `p1` | service-wide baseline review tracker; scored as a programme item |
| [#758](https://github.com/Neaox/overcast/issues/758) | eventbridge: emulated services emit almost no service-originated events (EC2... | 140 | 105 | 2.0 | 1.0 | 1.5 | `p2` | `p1` | only 2 service-originated events exist; every other rule silently never ... |
| [#540](https://github.com/Neaox/overcast/issues/540) | cloudformation: properties dropped between CDK and the emulator — patterns a... | 136 | 255 | 1.0 | 0.8 | 1.5 | `p2` | `p1` | tracking issue for the tail + the shared allow-list helper |
| [#713](https://github.com/Neaox/overcast/issues/713) | [priority:P3] coerce a string RetentionInDays to a number before dispatching... | 136 | 85 | 1.0 | 0.8 | 0.5 | `none` | `p1` | string RetentionInDays fails JSON decode where CFN accepts it |
| [#543](https://github.com/Neaox/overcast/issues/543) | elasticache: properties dropped between CDK and the emulator | 132 | 66 | 2.0 | 1.0 | 1.0 | `p1` | `p1` | Engine dropped, so the wrong container image starts |
| [#628](https://github.com/Neaox/overcast/issues/628) | lambda: validate qualified permission targets and AddPermission condition fi... | 128 | 80 | 1.0 | 0.8 | 0.5 | `p2` | `p1` | qualified permission targets unvalidated |
| [#788](https://github.com/Neaox/overcast/issues/788) | CloudFormation: 33 resource handlers generate names from the stack name alon... | 128 | 255 | 2.0 | 1.0 | 4.0 | `p2` | `p1` | 33 handlers name resources from the stack name alone; two unnamed resour... |
| [#530](https://github.com/Neaox/overcast/issues/530) | ecr: properties dropped between CDK and the emulator | 124 | 62 | 1.0 | 1.0 | 0.5 | `p1` | `p1` | ImageTagMutability dropped; echo fidelity |
| [#513](https://github.com/Neaox/overcast/issues/513) | pipes: source DeadLetterConfig is stored but never used, so exhausted record... | 120 | 30 | 2.0 | 1.0 | 0.5 | `none` | `p1` | pipe source DLQ stored and never used; exhausted records vanish |
| [#531](https://github.com/Neaox/overcast/issues/531) | kinesis: properties dropped between CDK and the emulator | 120 | 60 | 1.0 | 1.0 | 0.5 | `p1` | `p1` | retention dropped; stream reports 24h |
| [#716](https://github.com/Neaox/overcast/issues/716) | [priority:P2] the s3, sqs, sns, kinesis, lambda, secretsmanager and logs con... | 120 | 75 | 1.0 | 0.8 | 0.5 | `none` | `p1` | search cache keys never match, so global search always refetches and ign... |
| [#18](https://github.com/Neaox/overcast/issues/18) | DynamoDB Baseline Compat | 119 | 210 | 1.0 | 0.85 | 1.5 | `p0` | `p1` | service-wide baseline review tracker; scored as a programme item |
| [#647](https://github.com/Neaox/overcast/issues/647) | lambda: mount S3 Files FileSystemConfigs in execution environments | 107 | 80 | 2.0 | 1.0 | 1.5 | `p1` | `p1` | S3 Files ARNs routed through the EFS resolver; mount silently skipped |
| [#614](https://github.com/Neaox/overcast/issues/614) | Run RDS in live mode under compat CI, not mock | 100 | 150 | 1.0 | 1.0 | 1.5 | `none` | `p1` | RDS runs in mock mode under compat; live mode is the way back |
| [#813](https://github.com/Neaox/overcast/issues/813) | changelog-required: a fully-exempt PR is asked for a fragment — changed.txt ... | 100 | 50 | 1.0 | 1.0 | 0.5 | `none` | `p1` | fully-exempt PRs asked for a changelog fragment; stale base.sha |
| [#45](https://github.com/Neaox/overcast/issues/45) | SQS Baseline Compat | 99 | 175 | 1.0 | 0.85 | 1.5 | `p0` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#883](https://github.com/Neaox/overcast/issues/883) | awsapi: generate the HTTP member bindings the manifest cannot carry, so requ... | 98 | 245 | 2.0 | 0.8 | 4.0 | `none` | `p2` | member bindings so request shape becomes enforceable; the other half of ... |
| [#887](https://github.com/Neaox/overcast/issues/887) | router: answer a service-scope mismatch the way AWS does, instead of droppin... | 93 | 70 | 2.0 | 1.0 | 1.5 | `none` | `p2` | service-scope mismatch drops into S3 wildcard; NoSuchBucket for an OpenS... |
| [#16](https://github.com/Neaox/overcast/issues/16) | CloudWatch Logs Baseline Compat | 90 | 158 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#968](https://github.com/Neaox/overcast/issues/968) | release-prep refresh races itself when two PRs merge seconds apart, silently... | 90 | 45 | 1.0 | 1.0 | 0.5 | `none` | `p2` | release-prep races itself; a fragment is left unconsumed |
| [#184](https://github.com/Neaox/overcast/issues/184) | SNS compat: Review Query/JSON protocol parity; current service advertises Qu... | 86 | 48 | 1.0 | 0.9 | 0.5 | `p1` | `p2` | named implementation/doc disagreement |
| [#829](https://github.com/Neaox/overcast/issues/829) | cloudformation: name-based reads still resolve DELETE_COMPLETE stacks, where... | 85 | 128 | 1.0 | 1.0 | 1.5 | `none` | `p2` | name-based reads still resolve DELETE_COMPLETE stacks; clients branch on... |
| [#527](https://github.com/Neaox/overcast/issues/527) | elbv2: properties dropped between CDK and the emulator | 84 | 63 | 2.0 | 1.0 | 1.5 | `p1` | `p2` | target group health-check/TargetType dropped |
| [#749](https://github.com/Neaox/overcast/issues/749) | docs: generate or CI-check the countable documentation facts so corrections ... | 80 | 120 | 1.0 | 1.0 | 1.5 | `p2` | `p2` | generate or CI-check the countable facts so these corrections are made once |
| [#947](https://github.com/Neaox/overcast/issues/947) | inithooks: a hook script's orphaned grandchild holds go test's stdout on Win... | 80 | 40 | 1.0 | 1.0 | 0.5 | `none` | `p2` | orphaned grandchild reds the pre-push gate after every test passes |
| [#4](https://github.com/Neaox/overcast/issues/4) | API Gateway Baseline Compat | 79 | 140 | 1.0 | 0.85 | 1.5 | `p0` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#44](https://github.com/Neaox/overcast/issues/44) | SNS Baseline Compat | 79 | 140 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#48](https://github.com/Neaox/overcast/issues/48) | STS Baseline Compat | 79 | 140 | 1.0 | 0.85 | 1.5 | `p0` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#496](https://github.com/Neaox/overcast/issues/496) | tier2: evaluate resource-based policies at IAM enforcement time (S3 bucket, ... | 75 | 188 | 2.0 | 0.8 | 4.0 | `none` | `p2` | resource policies stored but never enforced; the other half of #471 |
| [#642](https://github.com/Neaox/overcast/issues/642) | Implement S3 bucket ACL APIs and CloudFormation AccessControl | 72 | 135 | 1.0 | 0.8 | 1.5 | `p2` | `p2` | S3 bucket ACL APIs are 501; AccessControl cannot be applied |
| [#643](https://github.com/Neaox/overcast/issues/643) | Implement S3 Public Access Block APIs and CloudFormation dispatch | 72 | 135 | 1.0 | 0.8 | 1.5 | `p2` | `p2` | S3 public access block APIs are 501 |
| [#991](https://github.com/Neaox/overcast/issues/991) | compat flake: java-sdk/ecs-tasks (2 test(s)) | 72 | 36 | 1.0 | 1.0 | 0.5 | `none` | `p2` | ecs-tasks flake; most compat flakes have been real emulator state races |
| [#995](https://github.com/Neaox/overcast/issues/995) | compat flake: python-sdk/ecs-tasks (2 test(s)) | 72 | 36 | 1.0 | 1.0 | 0.5 | `none` | `p2` | same flake in python-sdk |
| [#518](https://github.com/Neaox/overcast/issues/518) | ec2: launch templates, so Auto Scaling groups can use them | 70 | 105 | 1.0 | 1.0 | 1.5 | `p2` | `p2` | EC2 launch templates; the modern CDK default path ASGs cannot use |
| [#20](https://github.com/Neaox/overcast/issues/20) | EC2 Baseline Compat | 69 | 122 | 1.0 | 0.85 | 1.5 | `p0` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#26](https://github.com/Neaox/overcast/issues/26) | EventBridge Baseline Compat | 69 | 122 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#639](https://github.com/Neaox/overcast/issues/639) | cloudformation: restore all in-place resource updates during stack rollback | 68 | 170 | 2.0 | 0.8 | 4.0 | `p1` | `p2` | in-place updates not restored on rollback; live resource disagrees with ... |
| [#737](https://github.com/Neaox/overcast/issues/737) | s3: flexible checksums (x-amz-checksum-*) are not implemented; verify aws-ch... | 68 | 270 | 2.0 | 0.5 | 4.0 | `none` | `p2` | recent SDK majors send checksums by default and none are implemented |
| [#506](https://github.com/Neaox/overcast/issues/506) | compat: add a CloudWatch metrics/alarms group across all suites | 67 | 100 | 1.0 | 1.0 | 1.5 | `none` | `p2` | no CloudWatch metrics/alarms compat group; needs SDK wiring in seven suites |
| [#886](https://github.com/Neaox/overcast/issues/886) | cloudwatch: GetMetricData is unreachable over awsJson1_0, its primary modele... | 67 | 50 | 2.0 | 1.0 | 1.5 | `none` | `p2` | GetMetricData unreachable over its primary modeled protocol |
| [#629](https://github.com/Neaox/overcast/issues/629) | lambda: enforce resource policies for cross-service invocations | 64 | 120 | 1.0 | 0.8 | 1.5 | `p2` | `p2` | cross-service invokes ignore Lambda resource policies |
| [#172](https://github.com/Neaox/overcast/issues/172) | S3 compat: ListObjectsV2: Validate max-keys boundary and malformed values ag... | 61 | 72 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#173](https://github.com/Neaox/overcast/issues/173) | S3 compat: ListObjectsV2: Review encoding-type=url, fetch-owner, requester-p... | 61 | 72 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#174](https://github.com/Neaox/overcast/issues/174) | S3 compat: Finish ListObjectsV2 optional-parameter review before marking the... | 61 | 72 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#175](https://github.com/Neaox/overcast/issues/175) | S3 compat: Verify CDK bootstrap bucket flows and policy/versioning/tagging e... | 61 | 72 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#22](https://github.com/Neaox/overcast/issues/22) | ECS Baseline Compat | 60 | 105 | 1.0 | 0.85 | 1.5 | `p0` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#41](https://github.com/Neaox/overcast/issues/41) | Secrets Manager Baseline Compat | 60 | 105 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#46](https://github.com/Neaox/overcast/issues/46) | SSM Baseline Compat | 60 | 105 | 1.0 | 0.85 | 1.5 | `p0` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#558](https://github.com/Neaox/overcast/issues/558) | docker: stop relying on corepack — it is gone from Node, and blocks Node 26 | 60 | 30 | 1.0 | 1.0 | 0.5 | `p2` | `p2` | corepack is gone from Node and blocks Node 26 LTS in October 2026 |
| [#744](https://github.com/Neaox/overcast/issues/744) | docs: STATUS.md contradicts its own generated tables and lists completed wor... | 60 | 60 | 0.5 | 1.0 | 0.5 | `p3` | `p2` | STATUS.md contradicts its own generated tables |
| [#885](https://github.com/Neaox/overcast/issues/885) | ci(aws-model-refresh): open the refresh PR even when the gates fail, so a mo... | 60 | 30 | 1.0 | 1.0 | 0.5 | `p2` | `p2` | model-refresh PR is skipped when gates fail, which is exactly when you w... |
| [#965](https://github.com/Neaox/overcast/issues/965) | Warn when OVERCAST_HOSTNAME's subdomains do not resolve on this host (breaks... | 60 | 30 | 1.0 | 1.0 | 0.5 | `none` | `p2` | warn when subdomains do not resolve; breaks virtual-hosted S3 and cdk as... |
| [#536](https://github.com/Neaox/overcast/issues/536) | cognito: properties dropped between CDK and the emulator | 59 | 88 | 1.0 | 1.0 | 1.5 | `p2` | `p2` | LambdaConfig triggers look configured and fire nothing |
| [#73](https://github.com/Neaox/overcast/issues/73) | CloudFormation compat: Audit resource-type tracking against provisioner regi... | 58 | 68 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#74](https://github.com/Neaox/overcast/issues/74) | CloudFormation compat: Review update/delete semantics and unsupported-resour... | 58 | 68 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#165](https://github.com/Neaox/overcast/issues/165) | OpenSearch compat: Review AWS wire compatibility because custom /_opensearch... | 58 | 16 | 2.0 | 0.9 | 0.5 | `p0` | `p2` | names concrete defects to fix |
| [#514](https://github.com/Neaox/overcast/issues/514) | protocol: modeled error shapes drop structured members (e.g. ValidationExcep... | 56 | 105 | 1.0 | 0.8 | 1.5 | `none` | `p2` | modeled error shapes drop structured members repo-wide |
| [#565](https://github.com/Neaox/overcast/issues/565) | ci(compat): baseline promotion can raise a PR that is empty the moment its p... | 56 | 28 | 1.0 | 1.0 | 0.5 | `p2` | `p2` | promotion can raise an empty PR that cannot self-heal |
| [#159](https://github.com/Neaox/overcast/issues/159) | Lambda compat: CreateFunction: Review Publish=true version creation and addi... | 54 | 64 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#160](https://github.com/Neaox/overcast/issues/160) | Lambda compat: Finish CreateFunction Publish=true and advanced optional conf... | 54 | 64 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#161](https://github.com/Neaox/overcast/issues/161) | Lambda compat: Review runtime invocation fidelity, error shapes, layers, and... | 54 | 64 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#162](https://github.com/Neaox/overcast/issues/162) | Lambda compat: Review Docker-unavailable fallback compatibility. | 54 | 64 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#154](https://github.com/Neaox/overcast/issues/154) | IAM compat: Review CDK role, policy, and instance profile compatibility. | 51 | 60 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#21](https://github.com/Neaox/overcast/issues/21) | ECR Baseline Compat | 50 | 88 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#31](https://github.com/Neaox/overcast/issues/31) | KMS Baseline Compat | 50 | 88 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#37](https://github.com/Neaox/overcast/issues/37) | RDS Baseline Compat | 50 | 88 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#47](https://github.com/Neaox/overcast/issues/47) | Step Functions Baseline Compat | 50 | 88 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#870](https://github.com/Neaox/overcast/issues/870) | config: OVERCAST_HOST means the opposite of LOCALSTACK_HOST, and a code comm... | 50 | 25 | 1.0 | 1.0 | 0.5 | `p2` | `p2` | OVERCAST_HOST means the opposite of LOCALSTACK_HOST; comment claims equi... |
| [#511](https://github.com/Neaox/overcast/issues/511) | pipes: honour BatchSize/MaximumBatchingWindowInSeconds on the DynamoDB Strea... | 48 | 24 | 1.0 | 1.0 | 0.5 | `none` | `p2` | BatchSize ignored on the DDB-Streams source; batching code already exist... |
| [#533](https://github.com/Neaox/overcast/issues/533) | pipes: properties dropped between CDK and the emulator | 48 | 36 | 2.0 | 1.0 | 1.5 | `p1` | `p2` | every field that configures pipe behaviour is dropped; blocks #511/#513 |
| [#662](https://github.com/Neaox/overcast/issues/662) | cloudformation: RollbackStack must restore persisted dirty update failures | 45 | 85 | 1.0 | 0.8 | 1.5 | `p1` | `p2` | RollbackStack converts a failed resource to UPDATE_COMPLETE without retr... |
| [#51](https://github.com/Neaox/overcast/issues/51) | Cognito Baseline Compat | 44 | 77 | 1.0 | 0.85 | 1.5 | `p0` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#72](https://github.com/Neaox/overcast/issues/72) | Bedrock compat: Custom /_bedrock prefix likely needs explicit compatibility ... | 43 | 12 | 2.0 | 0.9 | 0.5 | `p0` | `p2` | names concrete defects to fix |
| [#81](https://github.com/Neaox/overcast/issues/81) | CloudWatch compat: Reconcile docs saying Query-only with implementation JSON... | 43 | 24 | 1.0 | 0.9 | 0.5 | `p1` | `p2` | named implementation/doc disagreement |
| [#136](https://github.com/Neaox/overcast/issues/136) | DynamoDB compat: Review CBOR coverage against SDK expectations. | 43 | 48 | 0.5 | 0.9 | 0.5 | `p0` | `p2` | test coverage for tracked behaviour |
| [#156](https://github.com/Neaox/overcast/issues/156) | Kinesis compat: Reconcile JSON 1.0 support versus docs claiming JSON 1.1. | 43 | 24 | 1.0 | 0.9 | 0.5 | `p1` | `p2` | named implementation/doc disagreement |
| [#135](https://github.com/Neaox/overcast/issues/135) | DynamoDB compat: Review expression edge cases, pagination semantics, and GSI... | 41 | 48 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#17](https://github.com/Neaox/overcast/issues/17) | CloudWatch Baseline Compat | 40 | 70 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#30](https://github.com/Neaox/overcast/issues/30) | Kinesis Baseline Compat | 40 | 70 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#535](https://github.com/Neaox/overcast/issues/535) | firehose: properties dropped between CDK and the emulator | 40 | 40 | 1.0 | 1.0 | 1.0 | `p2` | `p2` | every Firehose destination config discarded |
| [#745](https://github.com/Neaox/overcast/issues/745) | docs: five plan-doc status headers contradict shipped code — and the process... | 40 | 40 | 0.5 | 1.0 | 0.5 | `p3` | `p2` | five plan-doc headers contradict shipped code |
| [#747](https://github.com/Neaox/overcast/issues/747) | docs: four broken links/anchors, and no link check in CI | 40 | 40 | 0.5 | 1.0 | 0.5 | `p3` | `p2` | four broken links, no CI link check |
| [#544](https://github.com/Neaox/overcast/issues/544) | rds: properties dropped between CDK and the emulator | 38 | 38 | 0.5 | 1.0 | 0.5 | `p2` | `p2` | audit found RDS forwards everything load-bearing |
| [#599](https://github.com/Neaox/overcast/issues/599) | Web UI: one home for Prism JSON highlighting — five call sites still hand-ro... | 38 | 38 | 0.5 | 1.0 | 0.5 | `p3` | `p2` | five call sites hand-roll Prism JSON highlighting |
| [#25](https://github.com/Neaox/overcast/issues/25) | ELBv2 Baseline Compat | 36 | 63 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#186](https://github.com/Neaox/overcast/issues/186) | SQS compat: ReceiveMessage: ReceiveRequestAttemptId behavior for FIFO queues... | 34 | 40 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#187](https://github.com/Neaox/overcast/issues/187) | SQS compat: ReceiveMessage: VisibilityTimeout validation and in-flight seman... | 34 | 40 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#79](https://github.com/Neaox/overcast/issues/79) | CloudWatch Logs compat: Review PutLogEvents sequence token behavior. | 31 | 36 | 0.5 | 0.85 | 0.5 | `p1` | `p2` | narrow single-scenario review |
| [#80](https://github.com/Neaox/overcast/issues/80) | CloudWatch Logs compat: Review FilterLogEvents syntax and pagination fidelity. | 31 | 36 | 0.5 | 0.85 | 0.5 | `p1` | `p2` | narrow single-scenario review |
| [#14](https://github.com/Neaox/overcast/issues/14) | CloudFront Baseline Compat | 29 | 52 | 1.0 | 0.85 | 1.5 | `p0` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#19](https://github.com/Neaox/overcast/issues/19) | DynamoDB Streams Baseline Compat | 29 | 52 | 1.0 | 0.85 | 1.5 | `p1` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#54](https://github.com/Neaox/overcast/issues/54) | API Gateway compat: Finish ExecuteRestAPI Lambda proxy scenario coverage for... | 29 | 32 | 0.5 | 0.9 | 0.5 | `p0` | `p2` | test coverage for tracked behaviour |
| [#57](https://github.com/Neaox/overcast/issues/57) | API Gateway compat: Review CloudFormation/CDK resource coverage. | 29 | 32 | 0.5 | 0.9 | 0.5 | `p0` | `p2` | test coverage for tracked behaviour |
| [#55](https://github.com/Neaox/overcast/issues/55) | API Gateway compat: Review Lambda, HTTP, MOCK integration execution fidelity. | 27 | 32 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#56](https://github.com/Neaox/overcast/issues/56) | API Gateway compat: Review route matching and authorizer behavior. | 27 | 32 | 0.5 | 0.85 | 0.5 | `p0` | `p2` | narrow single-scenario review |
| [#185](https://github.com/Neaox/overcast/issues/185) | SNS compat: Review async delivery behavior, filter policy fidelity, and SQS/... | 27 | 32 | 0.5 | 0.85 | 0.5 | `p1` | `p2` | narrow single-scenario review |
| [#42](https://github.com/Neaox/overcast/issues/42) | SES Baseline Compat | 26 | 46 | 1.0 | 0.85 | 1.5 | `p0` | `p2` | service-wide baseline review tracker; scored as a programme item |
| [#671](https://github.com/Neaox/overcast/issues/671) | IAM: remove dead legacy handlers after typed dispatch migration | 25 | 75 | 0.5 | 1.0 | 1.5 | `p2` | `p2` | remove dead IAM legacy handlers |
| [#831](https://github.com/Neaox/overcast/issues/831) | ECR: opt-in strict cross-region image resolution (documented works-locally/f... | 25 | 25 | 0.5 | 1.0 | 0.5 | `none` | `p2` | opt-in strict cross-region ECR resolution |
| [#24](https://github.com/Neaox/overcast/issues/24) | ElastiCache Baseline Compat | 24 | 42 | 1.0 | 0.85 | 1.5 | `p2` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#38](https://github.com/Neaox/overcast/issues/38) | Route 53 Baseline Compat | 24 | 42 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#40](https://github.com/Neaox/overcast/issues/40) | Scheduler Baseline Compat | 24 | 42 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#139](https://github.com/Neaox/overcast/issues/139) | EC2 compat: Focus VPC, subnet, security group, route table, and ENI behavior. | 24 | 28 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#140](https://github.com/Neaox/overcast/issues/140) | EC2 compat: Review AWS Query XML shapes. | 24 | 28 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#148](https://github.com/Neaox/overcast/issues/148) | EventBridge compat: Review target, rule, and tag protocol fidelity. | 24 | 28 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#680](https://github.com/Neaox/overcast/issues/680) | cloudformation: support AWS::SecretsManager::SecretTargetAttachment | 24 | 45 | 1.0 | 0.8 | 1.5 | `none` | `p3` | CDK database-secret flows synthesize SecretTargetAttachment; no handler |
| [#933](https://github.com/Neaox/overcast/issues/933) | Lambda: throttled async events are dead-lettered where AWS queues them for h... | 24 | 120 | 1.0 | 0.8 | 4.0 | `p2` | `p3` | throttled async events dead-lettered where AWS queues them |
| [#739](https://github.com/Neaox/overcast/issues/739) | cdk deploy speed: measure the sync-wait budget against real stacks, verify c... | 23 | 85 | 0.5 | 0.8 | 1.5 | `p3` | `p3` | measure the cdk sync-wait budget, document the levers |
| [#755](https://github.com/Neaox/overcast/issues/755) | protocol: 131 hand-written Query XML response envelopes, with no shared helper | 23 | 70 | 0.5 | 1.0 | 1.5 | `p3` | `p3` | 131 hand-written Query XML envelopes, no shared helper |
| [#852](https://github.com/Neaox/overcast/issues/852) | Compat review: s3 GetObject/DeleteObject ?versionId= validation disagrees ac... | 22 | 45 | 0.5 | 0.5 | 0.5 | `p3` | `p3` | versionId validation disagrees across verbs |
| [#142](https://github.com/Neaox/overcast/issues/142) | ECS compat: Prioritize RunTask, CreateService, RegisterTaskDefinition, Farga... | 20 | 24 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#178](https://github.com/Neaox/overcast/issues/178) | Secrets Manager compat: Review unsupported policy/replication operations and... | 20 | 24 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#179](https://github.com/Neaox/overcast/issues/179) | Secrets Manager compat: Remove duplicated/non-generated summary drift risk i... | 20 | 24 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#567](https://github.com/Neaox/overcast/issues/567) | EFS lifecycle tests race the mock clock: settle() advances without waiting | 20 | 20 | 0.5 | 1.0 | 0.5 | `p3` | `p3` | EFS lifecycle tests race the mock clock; fifth service with the #562 pat... |
| [#617](https://github.com/Neaox/overcast/issues/617) | docs search ranks by unbounded body term frequency, so ordinary prose edits ... | 20 | 20 | 0.5 | 1.0 | 0.5 | `none` | `p3` | docs search ranks by unbounded body term frequency; prose edits break un... |
| [#648](https://github.com/Neaox/overcast/issues/648) | lambda: capture collection validation and empty ImageConfig response semantics | 20 | 40 | 0.5 | 0.5 | 0.5 | `p1` | `p3` | two wire details need real-AWS capture |
| [#657](https://github.com/Neaox/overcast/issues/657) | CloudFormation/Lambda: capture EventSourceMapping default reset semantics | 20 | 40 | 0.5 | 0.5 | 0.5 | `p1` | `p3` | ESM default-reset semantics need real-AWS capture |
| [#884](https://github.com/Neaox/overcast/issues/884) | devex: a supported way to fetch the pinned AWS models, cached outside the repo | 20 | 30 | 1.0 | 1.0 | 1.5 | `none` | `p3` | supported way to fetch the pinned AWS models |
| [#147](https://github.com/Neaox/overcast/issues/147) | EventBridge compat: Decide whether inert routing is acceptable for compatibi... | 19 | 21 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | support-claim honesty: reclassify inert/partial |
| [#738](https://github.com/Neaox/overcast/issues/738) | architecture: three parallel implementations of router-replayed cross-servic... | 19 | 56 | 0.5 | 1.0 | 1.5 | `p3` | `p3` | three parallel router-replay dispatch implementations |
| [#8](https://github.com/Neaox/overcast/issues/8) | AppSync Baseline Compat | 18 | 31 | 1.0 | 0.85 | 1.5 | `p0` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#27](https://github.com/Neaox/overcast/issues/27) | Firehose Baseline Compat | 18 | 31 | 1.0 | 0.85 | 1.5 | `p2` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#157](https://github.com/Neaox/overcast/issues/157) | KMS compat: Review crypto, key policy, and grant edge-case semantics. | 17 | 20 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#158](https://github.com/Neaox/overcast/issues/158) | KMS compat: Align docs note for JSON 1.0 if intentional. | 17 | 20 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#169](https://github.com/Neaox/overcast/issues/169) | RDS compat: Review CreateDBInstance/CreateDBCluster lifecycle, validation er... | 17 | 20 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#10](https://github.com/Neaox/overcast/issues/10) | Auto Scaling Baseline Compat | 16 | 28 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#34](https://github.com/Neaox/overcast/issues/34) | OpenSearch Baseline Compat | 16 | 28 | 1.0 | 0.85 | 1.5 | `p0` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#477](https://github.com/Neaox/overcast/issues/477) | tier2: DynamoDB PartiQL (ExecuteStatement/ExecuteTransaction/BatchExecuteSta... | 16 | 60 | 0.5 | 0.8 | 1.5 | `p3` | `p3` | PartiQL; unblocked by GSI phase 3 |
| [#677](https://github.com/Neaox/overcast/issues/677) | DynamoDB: model asynchronous TimeToLive status transitions | 16 | 60 | 0.5 | 0.8 | 1.5 | `p2` | `p3` | async TTL status transitions |
| [#678](https://github.com/Neaox/overcast/issues/678) | cloudformation: regenerate Secrets Manager values when GenerateSecretString ... | 16 | 30 | 1.0 | 0.8 | 1.5 | `none` | `p3` | GenerateSecretString change does not regenerate |
| [#681](https://github.com/Neaox/overcast/issues/681) | cloudformation: support AWS::SecretsManager::RotationSchedule | 16 | 30 | 1.0 | 0.8 | 1.5 | `none` | `p3` | CDK RotationSchedule not provisioned though rotation APIs exist |
| [#746](https://github.com/Neaox/overcast/issues/746) | docs: stale counts and a stale line citation in AGENTS.md and CONTRIBUTING.md | 16 | 32 | 0.25 | 1.0 | 0.5 | `p3` | `p3` | stale counts in AGENTS.md/CONTRIBUTING.md |
| [#83](https://github.com/Neaox/overcast/issues/83) | Cognito compat: AdminCreateUser: Self-service ConfirmSignUp ForceAliasCreati... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#84](https://github.com/Neaox/overcast/issues/84) | Cognito compat: UpdateUserAttributes: AutoVerifiedAttributes-specific CodeDe... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#86](https://github.com/Neaox/overcast/issues/86) | Cognito compat: RespondToAuthChallenge: ChallengeResponses USERNAME edge cas... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#87](https://github.com/Neaox/overcast/issues/87) | Cognito compat: RespondToAuthChallenge: MFA challenge behavior not fully rev... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#90](https://github.com/Neaox/overcast/issues/90) | Cognito compat: AdminInitiateAuth: Device opt-in confirmation edge cases, ex... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#91](https://github.com/Neaox/overcast/issues/91) | Cognito compat: AdminInitiateAuth: SRP responses use AWS-shaped challenge pa... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#92](https://github.com/Neaox/overcast/issues/92) | Cognito compat: AdminInitiateAuth: WebAuthn registration and assertion respo... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#93](https://github.com/Neaox/overcast/issues/93) | Cognito compat: AdminRespondToAuthChallenge: ChallengeResponses USERNAME edg... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#96](https://github.com/Neaox/overcast/issues/96) | Cognito compat: AdminRespondToAuthChallenge: MFA challenge behavior not full... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#97](https://github.com/Neaox/overcast/issues/97) | Cognito compat: SignUp: UUID equals sub behavior needs dedicated assertion. | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#98](https://github.com/Neaox/overcast/issues/98) | Cognito compat: SignUp: Duplicate username-attribute validation needs review. | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#99](https://github.com/Neaox/overcast/issues/99) | Cognito compat: ConfirmSignUp: General choice-based USER_AUTH without a Conf... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#100](https://github.com/Neaox/overcast/issues/100) | Cognito compat: ListUsers: Full invalid-filter error-message parity not veri... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#103](https://github.com/Neaox/overcast/issues/103) | Cognito compat: InitiateAuth: Device opt-in confirmation edge cases, exact v... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#104](https://github.com/Neaox/overcast/issues/104) | Cognito compat: InitiateAuth: SRP responses use AWS-shaped challenge paramet... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#105](https://github.com/Neaox/overcast/issues/105) | Cognito compat: InitiateAuth: WebAuthn registration and assertion responses ... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#106](https://github.com/Neaox/overcast/issues/106) | Cognito compat: ConfirmDevice: Device verifier cryptographic validation, dup... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#107](https://github.com/Neaox/overcast/issues/107) | Cognito compat: GetDevice: Exact not-found and malformed-device-key validati... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#108](https://github.com/Neaox/overcast/issues/108) | Cognito compat: ListDevices: User ListDevices pagination and exact Limit val... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#109](https://github.com/Neaox/overcast/issues/109) | Cognito compat: UpdateDeviceStatus: Exact invalid status and missing-device ... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#110](https://github.com/Neaox/overcast/issues/110) | Cognito compat: ForgetDevice: Exact not-found semantics are not reviewed. | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#112](https://github.com/Neaox/overcast/issues/112) | Cognito compat: AdminListDevices: Exact pagination-token encoding and valida... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#113](https://github.com/Neaox/overcast/issues/113) | Cognito compat: AdminUpdateDeviceStatus: Exact invalid status and not-found ... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#114](https://github.com/Neaox/overcast/issues/114) | Cognito compat: AdminForgetDevice: Exact not-found semantics are not reviewed. | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#115](https://github.com/Neaox/overcast/issues/115) | Cognito compat: CreateUserPool: Essentials-tier restrictions are not reviewed. | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#116](https://github.com/Neaox/overcast/issues/116) | Cognito compat: UpdateUserPool: Essentials-tier restrictions are not reviewed. | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#117](https://github.com/Neaox/overcast/issues/117) | Cognito compat: CreateUserPoolClient: App-tier restrictions for ALLOW_USER_A... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#118](https://github.com/Neaox/overcast/issues/118) | Cognito compat: UpdateUserPoolClient: App-tier restrictions for ALLOW_USER_A... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#119](https://github.com/Neaox/overcast/issues/119) | Cognito compat: SetUserPoolMfaConfig: Email/SMS/software-token MFA config de... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#121](https://github.com/Neaox/overcast/issues/121) | Cognito compat: GetUserPoolMfaConfig: Email/SMS/software-token MFA config re... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#126](https://github.com/Neaox/overcast/issues/126) | Cognito compat: Review exact device validation errors, user ListDevices pagi... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#128](https://github.com/Neaox/overcast/issues/128) | Cognito compat: Review app-tier restrictions for ALLOW_USER_AUTH. | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#129](https://github.com/Neaox/overcast/issues/129) | Cognito compat: Review app-tier restrictions for SignInPolicy and ALLOW_USER... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#130](https://github.com/Neaox/overcast/issues/130) | Cognito compat: Review exact verification-code TTL, throttling, delivery fai... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#131](https://github.com/Neaox/overcast/issues/131) | Cognito compat: Review exact ListUsers invalid-filter validation parity. | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#132](https://github.com/Neaox/overcast/issues/132) | Cognito compat: Review SECRET_HASH expectations when username aliases are used. | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#133](https://github.com/Neaox/overcast/issues/133) | Cognito compat: Review all challenge flows, including MFA and NEW_PASSWORD_R... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#134](https://github.com/Neaox/overcast/issues/134) | Cognito compat: Review every admin user operation that accepts Username for ... | 15 | 18 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#545](https://github.com/Neaox/overcast/issues/545) | cloudfront: properties dropped between CDK and the emulator | 15 | 15 | 0.5 | 1.0 | 0.5 | `p2` | `p3` | audit found CloudFront config clean; model to copy |
| [#9](https://github.com/Neaox/overcast/issues/9) | Athena Baseline Compat | 14 | 24 | 1.0 | 0.85 | 1.5 | `p2` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#23](https://github.com/Neaox/overcast/issues/23) | EKS Baseline Compat | 14 | 24 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#28](https://github.com/Neaox/overcast/issues/28) | Glue Baseline Compat | 14 | 24 | 1.0 | 0.85 | 1.5 | `p2` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#82](https://github.com/Neaox/overcast/issues/82) | CloudWatch compat: Review alarm and metric wire parity. | 14 | 16 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#141](https://github.com/Neaox/overcast/issues/141) | ECR compat: Downgrade or annotate Docker-dependent and scanner-unavailable b... | 14 | 15 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | support-claim honesty: reclassify inert/partial |
| [#155](https://github.com/Neaox/overcast/issues/155) | Kinesis compat: Review pagination and error-shape fidelity. | 14 | 16 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#605](https://github.com/Neaox/overcast/issues/605) | CloudFormation: ssm-secure dynamic references are accepted in any resource p... | 14 | 42 | 0.5 | 1.0 | 1.5 | `p3` | `p3` | ssm-secure accepted in any property; AWS enumerates them |
| [#672](https://github.com/Neaox/overcast/issues/672) | CloudWatch Logs: implement advanced log-group properties before CFN forwarding | 14 | 68 | 1.0 | 0.8 | 4.0 | `p2` | `p3` | advanced log-group properties must land in Logs before CFN forwards them |
| [#168](https://github.com/Neaox/overcast/issues/168) | Pipes compat: Reconcile stale service comment saying UpdatePipe is 501 with ... | 13 | 7 | 1.0 | 0.9 | 0.5 | `p1` | `p3` | named implementation/doc disagreement |
| [#762](https://github.com/Neaox/overcast/issues/762) | lint: enforce the Overcast-specific architecture rules instead of restating ... | 13 | 25 | 1.0 | 0.8 | 1.5 | `p2` | `p3` | architecture rules restated in five documents and enforced by nothing |
| [#800](https://github.com/Neaox/overcast/issues/800) | lambda: an INIT timeout reports almost nothing — no per-environment Runtime ... | 13 | 40 | 1.0 | 0.5 | 1.5 | `none` | `p3` | INIT timeout reports almost nothing; diagnosis aid |
| [#3](https://github.com/Neaox/overcast/issues/3) | ACM Baseline Compat | 12 | 21 | 1.0 | 0.85 | 1.5 | `p2` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#12](https://github.com/Neaox/overcast/issues/12) | Bedrock Baseline Compat | 12 | 21 | 1.0 | 0.85 | 1.5 | `p0` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#15](https://github.com/Neaox/overcast/issues/15) | CloudTrail Baseline Compat | 12 | 21 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#36](https://github.com/Neaox/overcast/issues/36) | Pipes Baseline Compat | 12 | 21 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#145](https://github.com/Neaox/overcast/issues/145) | ELBv2 compat: Check docs naming/path consistency between elb.md and service ... | 12 | 14 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#146](https://github.com/Neaox/overcast/issues/146) | ELBv2 compat: Verify unsupported listener rule and attribute operations are ... | 12 | 14 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#137](https://github.com/Neaox/overcast/issues/137) | DynamoDB Streams compat: Add integration coverage. | 11 | 12 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | test coverage for tracked behaviour |
| [#517](https://github.com/Neaox/overcast/issues/517) | compat: add an Auto Scaling group across all suites | 11 | 32 | 0.5 | 1.0 | 1.5 | `p3` | `p3` | no Auto Scaling compat group; same shape as #506 |
| [#33](https://github.com/Neaox/overcast/issues/33) | MSK Baseline Compat | 10 | 18 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#75](https://github.com/Neaox/overcast/issues/75) | CloudFront compat: Separate emulator-only ProxyRequest from AWS compatibilit... | 10 | 12 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#76](https://github.com/Neaox/overcast/issues/76) | CloudFront compat: Review ETag and XML response fidelity. | 10 | 12 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#138](https://github.com/Neaox/overcast/issues/138) | DynamoDB Streams compat: Review JSON target dispatch and CBOR compatibility. | 10 | 12 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#478](https://github.com/Neaox/overcast/issues/478) | tier2: CloudTrail real API-call logging | 10 | 18 | 1.0 | 0.8 | 1.5 | `p3` | `p3` | CloudTrail real logging; leaf feature, real dev-loop value |
| [#775](https://github.com/Neaox/overcast/issues/775) | [priority:P3] emit platform.initStart and platform.initReport records for La... | 10 | 40 | 0.25 | 0.5 | 0.5 | `none` | `p3` | auto-todo, initStart/initReport for cold starts |
| [#539](https://github.com/Neaox/overcast/issues/539) | eventbridge: properties dropped between CDK and the emulator | 9 | 18 | 0.25 | 1.0 | 0.5 | `p3` | `p3` | audit found EventBridge clean; record-only |
| [#5](https://github.com/Neaox/overcast/issues/5) | AppConfig Baseline Compat | 8 | 14 | 1.0 | 0.85 | 1.5 | `p0` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#11](https://github.com/Neaox/overcast/issues/11) | Backup Baseline Compat | 8 | 14 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#144](https://github.com/Neaox/overcast/issues/144) | ElastiCache compat: Review CreateCacheCluster/CreateReplicationGroup validat... | 8 | 10 | 0.5 | 0.85 | 0.5 | `p2` | `p3` | narrow single-scenario review |
| [#170](https://github.com/Neaox/overcast/issues/170) | Route 53 compat: Clarify QueryXML compatibility in docs or remove if acciden... | 8 | 10 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#171](https://github.com/Neaox/overcast/issues/171) | Route 53 compat: Review pagination and marker semantics. | 8 | 10 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#176](https://github.com/Neaox/overcast/issues/176) | Scheduler compat: Review target dispatch docs versus code. | 8 | 10 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#180](https://github.com/Neaox/overcast/issues/180) | SES compat: Review multi-protocol tracking for v2 REST-JSON doc-only operati... | 8 | 10 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#181](https://github.com/Neaox/overcast/issues/181) | SES compat: Verify unsupported v1 stubs match AWS error shape. | 8 | 10 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#720](https://github.com/Neaox/overcast/issues/720) | Unconfirmed: TestDescribeScalingActivities_recordsLaunches may race the scal... | 8 | 16 | 0.5 | 0.5 | 0.5 | `none` | `p3` | unconfirmed autoscaling test race |
| [#833](https://github.com/Neaox/overcast/issues/833) | ECR: opt-in AWS-grammar repositoryUri (LocalStack domain-strategy parity) | 8 | 25 | 0.5 | 1.0 | 1.5 | `none` | `p3` | opt-in AWS-grammar repositoryUri |
| [#756](https://github.com/Neaox/overcast/issues/756) | protocol: generate request/response types from the pinned AWS models (level2... | 7 | 70 | 0.5 | 0.8 | 4.0 | `p3` | `p3` | generate request/response types from the pinned models |
| [#35](https://github.com/Neaox/overcast/issues/35) | Organizations Baseline Compat | 6 | 10 | 1.0 | 0.85 | 1.5 | `p2` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#50](https://github.com/Neaox/overcast/issues/50) | WAF Baseline Compat | 6 | 10 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#61](https://github.com/Neaox/overcast/issues/61) | AppSync compat: CreateGraphqlApi: Add focused tests for visibility, introspe... | 6 | 7 | 0.5 | 0.9 | 0.5 | `p0` | `p3` | test coverage for tracked behaviour |
| [#62](https://github.com/Neaox/overcast/issues/62) | AppSync compat: CreateGraphqlApi: Review default API key auto-creation again... | 6 | 7 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#63](https://github.com/Neaox/overcast/issues/63) | AppSync compat: Audit support claims category by category. | 6 | 7 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#65](https://github.com/Neaox/overcast/issues/65) | AppSync compat: Continue CreateGraphqlApi boundary/nested-config coverage be... | 6 | 7 | 0.5 | 0.9 | 0.5 | `p0` | `p3` | test coverage for tracked behaviour |
| [#149](https://github.com/Neaox/overcast/issues/149) | Firehose compat: Review request/response shapes. | 6 | 7 | 0.5 | 0.85 | 0.5 | `p2` | `p3` | narrow single-scenario review |
| [#177](https://github.com/Neaox/overcast/issues/177) | Scheduler compat: Mark unsupported target types and advanced scheduler featu... | 6 | 7 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | support-claim honesty: reclassify inert/partial |
| [#651](https://github.com/Neaox/overcast/issues/651) | Implement Lambda advanced function configuration fields | 6 | 64 | 0.5 | 0.8 | 4.0 | `p2` | `p3` | advanced function config fields; 501 until then, so it fails loudly |
| [#652](https://github.com/Neaox/overcast/issues/652) | Implement Lambda publish and deployment-code storage controls | 6 | 64 | 0.5 | 0.8 | 4.0 | `p2` | `p3` | publish/deployment-code controls; 501 until then |
| [#653](https://github.com/Neaox/overcast/issues/653) | Implement advanced Lambda event-source mapping configurations | 6 | 64 | 0.5 | 0.8 | 4.0 | `p2` | `p3` | advanced ESM configs; 501 until then |
| [#774](https://github.com/Neaox/overcast/issues/774) | [priority:P3] populate errorType on Lambda platform.runtimeDone and platform... | 6 | 24 | 0.25 | 0.5 | 0.5 | `none` | `p3` | auto-todo, errorType on runtimeDone/report |
| [#934](https://github.com/Neaox/overcast/issues/934) | Lambda: aged-out async records report RetriesExhausted, condition value unve... | 6 | 24 | 0.25 | 0.5 | 0.5 | `p3` | `p3` | condition value unverified; blocked on AWS capture |
| [#6](https://github.com/Neaox/overcast/issues/6) | AppConfigData Baseline Compat | 5 | 9 | 1.0 | 0.85 | 1.5 | `p2` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#66](https://github.com/Neaox/overcast/issues/66) | Athena compat: Review query execution and result response shapes. | 5 | 6 | 0.5 | 0.85 | 0.5 | `p2` | `p3` | narrow single-scenario review |
| [#143](https://github.com/Neaox/overcast/issues/143) | EKS compat: Run a focused EKS inventory and AWS docs mapping. | 5 | 6 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#151](https://github.com/Neaox/overcast/issues/151) | Glue compat: Compare catalog shapes against AWS SDK expectations. | 5 | 6 | 0.5 | 0.85 | 0.5 | `p2` | `p3` | narrow single-scenario review |
| [#152](https://github.com/Neaox/overcast/issues/152) | Glue compat: Track unsupported Glue operation families. | 5 | 6 | 0.5 | 0.85 | 0.5 | `p2` | `p3` | narrow single-scenario review |
| [#193](https://github.com/Neaox/overcast/issues/193) | Step Functions compat: Change StartExecution/execution tracking to inert/par... | 5 | 12 | 0.25 | 0.9 | 0.5 | `p1` | `p3` | documentation of known behaviour |
| [#480](https://github.com/Neaox/overcast/issues/480) | tier2: SES receipt rules (inbound routing) | 5 | 20 | 0.5 | 0.8 | 1.5 | `p3` | `p3` | SES receipt rules; outbound already covers the common case |
| [#561](https://github.com/Neaox/overcast/issues/561) | deps: evaluate replacing Dependabot with Renovate, for LTS-aware base-image ... | 5 | 20 | 0.5 | 0.8 | 1.5 | `p2` | `p3` | Renovate for LTS-aware base-image updates |
| [#633](https://github.com/Neaox/overcast/issues/633) | CloudFormation: implement SNS Subscription Region semantics | 5 | 20 | 0.5 | 0.8 | 1.5 | `none` | `p3` | SNS Subscription Region semantics |
| [#751](https://github.com/Neaox/overcast/issues/751) | docs: remaining gaps after the architecture doc — web console architecture, ... | 5 | 32 | 0.25 | 1.0 | 1.5 | `p3` | `p3` | web console architecture, plans index, codegen narrative |
| [#49](https://github.com/Neaox/overcast/issues/49) | Transfer Baseline Compat | 4 | 7 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#52](https://github.com/Neaox/overcast/issues/52) | ACM compat: Expand integration coverage for tags. | 4 | 5 | 0.5 | 0.9 | 0.5 | `p2` | `p3` | test coverage for tracked behaviour |
| [#64](https://github.com/Neaox/overcast/issues/64) | AppSync compat: Downgrade or annotate config-only and inert behavior where A... | 4 | 5 | 0.5 | 0.9 | 0.5 | `p0` | `p3` | support-claim honesty: reclassify inert/partial |
| [#68](https://github.com/Neaox/overcast/issues/68) | Auto Scaling compat: Review inert versus supported classification for Query ... | 4 | 5 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | support-claim honesty: reclassify inert/partial |
| [#71](https://github.com/Neaox/overcast/issues/71) | Bedrock compat: Review AWS Bedrock Runtime path compatibility and canned res... | 4 | 5 | 0.5 | 0.85 | 0.5 | `p0` | `p3` | narrow single-scenario review |
| [#78](https://github.com/Neaox/overcast/issues/78) | CloudTrail compat: Decide whether inert operations should be reviewed as sup... | 4 | 4 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | support-claim honesty: reclassify inert/partial |
| [#85](https://github.com/Neaox/overcast/issues/85) | Cognito compat: GetUserAttributeVerificationCode: Exact code TTL, throttling... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#88](https://github.com/Neaox/overcast/issues/88) | Cognito compat: AdminInitiateAuth: CUSTOM_AUTH Lambda Define/Create/Verify t... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#89](https://github.com/Neaox/overcast/issues/89) | Cognito compat: AdminInitiateAuth: Device SRP verifier cryptographic proof v... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#94](https://github.com/Neaox/overcast/issues/94) | Cognito compat: AdminRespondToAuthChallenge: CUSTOM_AUTH Lambda trigger exec... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#95](https://github.com/Neaox/overcast/issues/95) | Cognito compat: AdminRespondToAuthChallenge: Device SRP verifier cryptograph... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#101](https://github.com/Neaox/overcast/issues/101) | Cognito compat: InitiateAuth: CUSTOM_AUTH Lambda Define/Create/Verify trigge... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#102](https://github.com/Neaox/overcast/issues/102) | Cognito compat: InitiateAuth: Device SRP verifier cryptographic proof valida... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#111](https://github.com/Neaox/overcast/issues/111) | Cognito compat: AdminGetDevice: Exact IAM enforcement and device attribute s... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#120](https://github.com/Neaox/overcast/issues/120) | Cognito compat: SetUserPoolMfaConfig: Exact WebAuthn configuration validatio... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#122](https://github.com/Neaox/overcast/issues/122) | Cognito compat: StartWebAuthnRegistration: Exact challenge expiry and all We... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#123](https://github.com/Neaox/overcast/issues/123) | Cognito compat: CompleteWebAuthnRegistration: WebAuthn attestation, origin, ... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#124](https://github.com/Neaox/overcast/issues/124) | Cognito compat: SRP challenge parameters and verifier response shape are emu... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#125](https://github.com/Neaox/overcast/issues/125) | Cognito compat: Device SRP challenge parameters and verifier response shape ... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#127](https://github.com/Neaox/overcast/issues/127) | Cognito compat: WebAuthn challenge and registration shapes are emulated, but... | 4 | 9 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documents an already-decided deliberate gap |
| [#167](https://github.com/Neaox/overcast/issues/167) | Pipes compat: Mark source/target delivery compatibility as partial. | 4 | 4 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | support-claim honesty: reclassify inert/partial |
| [#481](https://github.com/Neaox/overcast/issues/481) | tier2: EventBridge Archive/Replay/API destinations | 4 | 35 | 0.5 | 0.8 | 4.0 | `p3` | `p3` | EventBridge archive/replay/API destinations |
| [#679](https://github.com/Neaox/overcast/issues/679) | cloudformation,secretsmanager: support Secret ReplicaRegions lifecycle | 4 | 15 | 0.5 | 0.8 | 1.5 | `none` | `p3` | ReplicaRegions dropped; replication ops unsupported |
| [#7](https://github.com/Neaox/overcast/issues/7) | AppRegistry Baseline Compat | 3 | 5 | 1.0 | 0.85 | 1.5 | `p1` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#163](https://github.com/Neaox/overcast/issues/163) | MSK compat: Audit REST path, status, and header fidelity. | 3 | 4 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#164](https://github.com/Neaox/overcast/issues/164) | MSK compat: Ensure unsupported operations are consistently tracked and tested. | 3 | 4 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#585](https://github.com/Neaox/overcast/issues/585) | DynamoDB-Streams pipe lost an entire 50-record burst, once, never reproduced | 3 | 9 | 1.0 | 0.5 | 1.5 | `none` | `p3` | one unreproduced 50-record Pipes burst loss; kept so the next sighting i... |
| [#765](https://github.com/Neaox/overcast/issues/765) | docs: convert the architecture diagrams from mermaid to drawio | 3 | 20 | 0.25 | 0.8 | 1.5 | `p3` | `p3` | mermaid to drawio |
| [#767](https://github.com/Neaox/overcast/issues/767) | docs search: ranking gaps left after field weighting | 3 | 20 | 0.25 | 1.0 | 1.5 | `p3` | `p3` | ranking gaps left after field weighting |
| [#43](https://github.com/Neaox/overcast/issues/43) | Shield Baseline Compat | 2 | 4 | 1.0 | 0.85 | 1.5 | `p2` | `p3` | service-wide baseline review tracker; scored as a programme item |
| [#59](https://github.com/Neaox/overcast/issues/59) | AppConfigData compat: Verify control-plane/data-plane split and token rotati... | 2 | 2 | 0.5 | 0.85 | 0.5 | `p2` | `p3` | narrow single-scenario review |
| [#67](https://github.com/Neaox/overcast/issues/67) | Athena compat: Document inert query engine behavior in compatibility tracking. | 2 | 4 | 0.25 | 0.9 | 0.5 | `p2` | `p3` | documentation of known behaviour |
| [#70](https://github.com/Neaox/overcast/issues/70) | Backup compat: Verify inert capabilities match intended AWS SDK/IaC semantics. | 2 | 2 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | support-claim honesty: reclassify inert/partial |
| [#150](https://github.com/Neaox/overcast/issues/150) | Firehose compat: Document inert delivery semantics explicitly. | 2 | 4 | 0.25 | 0.9 | 0.5 | `p2` | `p3` | documentation of known behaviour |
| [#166](https://github.com/Neaox/overcast/issues/166) | Organizations compat: Keep minimal CDK-unblocking stub unless real Organizat... | 2 | 2 | 0.5 | 0.85 | 0.5 | `p2` | `p3` | narrow single-scenario review |
| [#197](https://github.com/Neaox/overcast/issues/197) | WAF compat: Review service naming and scope compatibility for WAFv2. | 2 | 2 | 0.5 | 0.85 | 0.5 | `p1` | `p3` | narrow single-scenario review |
| [#198](https://github.com/Neaox/overcast/issues/198) | WAF compat: Expand tracking beyond WebACL basics if SDK coverage is desired. | 2 | 2 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | test coverage for tracked behaviour |
| [#263](https://github.com/Neaox/overcast/issues/263) | AppSync: support configurable Cognito JWT enforcement | 2 | 9 | 0.5 | 0.8 | 1.5 | `p2` | `p3` | configurable AppSync Cognito JWT enforcement |
| [#757](https://github.com/Neaox/overcast/issues/757) | perf: benchmark router-replay dispatch against a direct interface call | 2 | 5 | 0.25 | 0.8 | 0.5 | `p3` | `p3` | benchmark router replay vs direct call |
| [#53](https://github.com/Neaox/overcast/issues/53) | ACM compat: Document JSON 1.0 and CBOR support if intentional. | 1 | 3 | 0.25 | 0.9 | 0.5 | `p2` | `p3` | documentation of known behaviour |
| [#58](https://github.com/Neaox/overcast/issues/58) | AppConfig compat: Add hosted configuration version operations to capabilitie... | 1 | 2 | 0.25 | 0.9 | 0.5 | `p0` | `p3` | documentation of known behaviour |
| [#60](https://github.com/Neaox/overcast/issues/60) | AppRegistry compat: Review inert/config-only behavior and no-pagination beha... | 1 | 1 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | support-claim honesty: reclassify inert/partial |
| [#69](https://github.com/Neaox/overcast/issues/69) | Backup compat: Add explicit protocol notes to docs. | 1 | 2 | 0.25 | 0.9 | 0.5 | `p1` | `p3` | docs-only protocol note |
| [#77](https://github.com/Neaox/overcast/issues/77) | CloudTrail compat: Add protocol section to docs. | 1 | 2 | 0.25 | 0.9 | 0.5 | `p1` | `p3` | docs-only protocol note |
| [#182](https://github.com/Neaox/overcast/issues/182) | Shield compat: Add integration tests for tracked operations before expanding... | 1 | 1 | 0.5 | 0.9 | 0.5 | `p2` | `p3` | test coverage for tracked behaviour |
| [#196](https://github.com/Neaox/overcast/issues/196) | Transfer compat: Review inert status versus supported status for SDK-facing ... | 1 | 1 | 0.5 | 0.9 | 0.5 | `p1` | `p3` | support-claim honesty: reclassify inert/partial |
| [#260](https://github.com/Neaox/overcast/issues/260) | [priority:P2] confirm real AWS StartSchemaCreation behavior for AppSync dire... | 1 | 3 | 0.25 | 0.5 | 0.5 | `none` | `p3` | auto-todo, scope template unfilled |
| [#348](https://github.com/Neaox/overcast/issues/348) | [priority:P3] adopt the net/exec checks and fix the call sites. | 1 | 5 | 0.25 | 0.5 | 0.5 | `none` | `p3` | auto-todo, adopt net/exec checks |
| [#349](https://github.com/Neaox/overcast/issues/349) | [priority:P3] decide whether to adopt ST1005 (error string style) | 1 | 3 | 0.25 | 0.5 | 0.5 | `none` | `p3` | auto-todo, ST1005 error string style |
| [#479](https://github.com/Neaox/overcast/issues/479) | tier2: AWS Backup on-demand backup/restore | 1 | 6 | 0.5 | 0.8 | 4.0 | `p3` | `p3` | Backup snapshot/restore; compliance-oriented, mediocre fit |
| [#482](https://github.com/Neaox/overcast/issues/482) | tier2: Route 53 real DNS serving | 1 | 12 | 0.5 | 0.5 | 4.0 | `p3` | `p3` | Route 53 real DNS; fit structurally capped |
| [#195](https://github.com/Neaox/overcast/issues/195) | Transfer compat: Add explicit protocol notes to docs. | 0 | 1 | 0.25 | 0.9 | 0.5 | `p1` | `p3` | docs-only protocol note |
| [#483](https://github.com/Neaox/overcast/issues/483) | tier2: WAF request-time rule evaluation | 0 | 2 | 0.25 | 0.5 | 4.0 | `p3` | `p3` | WAF request-time evaluation; lowest-confidence item in the backlog |
