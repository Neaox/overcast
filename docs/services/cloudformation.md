---
title: "CloudFormation — endpoint support"
description: "CloudFormation uses the AWS Query protocol (POST / with form-encoded body). Overcast implements stack lifecycle, change sets, and resource provisioning with an async provisioner..."
section: "Service Reference"
tags:
  - cloudformation
  - docs
  - endpoint
  - services
  - support
---

# CloudFormation — endpoint support

> AWS docs: [CloudFormation API Reference](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/Welcome.html)

CloudFormation uses the AWS Query protocol (`POST /` with form-encoded body). Overcast
implements stack lifecycle, change sets, and resource provisioning with a bounded async
provisioner that dispatches internal HTTP requests through the emulator to create/delete
resources.

---

## Intrinsic functions

The template engine supports these intrinsic functions:

| Function          | Status | Notes                                    |
| ----------------- | ------ | ---------------------------------------- |
| `Ref`             | ✅     | Resource logical IDs and parameters      |
| `Fn::Sub`         | ✅     | String substitution with `${var}` syntax |
| `Fn::Join`        | ✅     | Delimiter-based array join               |
| `Fn::Select`      | ✅     | Index-based selection from an array      |
| `Fn::GetAtt`      | ✅     | Resource attribute access                |
| `Fn::If`          | ✅     | Conditional values                       |
| `Fn::Split`       | ✅     | String splitting                         |
| `Fn::GetAZs`      | ✅     | Availability zone list                   |
| `Fn::ImportValue` | ✅     | Cross-stack reference resolution         |
| `Fn::Equals`      | ✅     | Condition: equality test                 |
| `Fn::Not`         | ✅     | Condition: negation                      |
| `Fn::And`         | ✅     | Condition: logical AND                   |
| `Fn::Or`          | ✅     | Condition: logical OR                    |

Pseudo-parameters supported: `AWS::Region`, `AWS::AccountId`, `AWS::StackId`, `AWS::StackName`, `AWS::URLSuffix`.

---

## Dynamic references

A dynamic reference is plain text inside a property value — not an intrinsic
function — that CloudFormation substitutes from another service at deploy time.
They are resolved against the emulated services, so a reference reads exactly
what the equivalent `GetSecretValue` or `GetParameter` call would return.

| Reference                                                                            | Status | Notes                                                     |
| ------------------------------------------------------------------------------------ | ------ | --------------------------------------------------------- |
| `{{resolve:secretsmanager:secret-id:SecretString:json-key:version-stage:version-id}}` | ✅     | `secret-id` may be a name or a full ARN                   |
| `{{resolve:ssm:parameter-name:version}}`                                              | ✅     | Version is accepted but resolves to the current value     |
| `{{resolve:ssm-secure:parameter-name:version}}`                                       | ✅     | Read with decryption; same version caveat                 |
| `{{resolve:s3:...}}`                                                                  | ❌     | Not resolved                                              |

Resolution happens after the intrinsic functions, so a reference built by
`Fn::Sub` or `Fn::Join` is resolved once the surrounding value is complete. A
resolved value is never rescanned — secret content that happens to contain
`{{resolve:` is data, not a reference. (AWS documents no behaviour either way
for that last case; resolving once is a deliberate choice, not a verified
match.)

**A reference is compared as written, never as resolved.** Change detection and
the stored resource properties both keep the literal `{{resolve:...}}` text, so:

- Rotating a secret behind an unchanged template does not make the resource look
  changed. This matches AWS — *"Updating only the secret value in Secrets
  Manager doesn't automatically cause CloudFormation to retrieve the new
  value"* — and to push a new value you must change the resource in the
  template, exactly as on AWS. No `GetSecretValue` call is made for an unchanged
  containing resource, so a no-op stack update also succeeds if that secret is
  no longer available.
- A resolved secret is never written to Overcast's state. Only the service the
  property belongs to ever sees it, which is the one exposure AWS also allows:
  *"the secret value may show up in the service whose resource it's being used
  in"*.

**Outputs leave references literal.** A `{{resolve:...}}` in an `Outputs` value
comes back as the reference text, not the value — matching CloudFormation, and
avoiding publishing a secret through `DescribeStacks`, which *"doesn't redact or
obfuscate any information you include in the Outputs section"*.

**A reference creates no dependency.** Only `Ref`, `Fn::GetAtt` and `Fn::Sub`
order resources. A resource reading a secret created by the same template needs
an explicit `DependsOn`, as it does on AWS.

**A reference that cannot be resolved fails the resource** with
`CREATE_FAILED`/`UPDATE_FAILED` and a reason naming the reference, and the stack
rolls back as it would for any other resource failure. This matters more than it
sounds: the alternative is a resource created with the literal
`{{resolve:...}}` text in place of a value, which every service downstream then
treats as data — an RDS instance whose master username is a 150-character
reference, for instance, is accepted by the API and then rejected by the
database engine.

Divergences from AWS:

- An explicit SSM parameter version is accepted but resolves to the current
  value; Overcast's `GetParameter` has no version selector. A warning is logged.
- `{{resolve:s3:...}}` is not supported and fails the resource rather than
  resolving to something wrong.
- `ssm-secure` is accepted in any resource property. AWS restricts it to an
  enumerated list (`AWS::RDS::DBInstance.MasterUserPassword`,
  `AWS::IAM::User.LoginProfile.Password` and a handful of others); Overcast does
  not enforce that list yet.

---

## Supported resource types

The provisioner registers handlers for **136 resource types** (126 provisioned
for real, 10 stubs), plus dynamically resolved custom resources and nested
stacks. The source of truth is the `resourceHandlers` map in
`internal/services/cloudformation/provisioner.go`; the complete list, grouped
by service, is in [cdk.md § Supported resource types](../cdk.md#supported-resource-types).

The provisioner dispatches internal HTTP requests to the emulated services. Resources
listed as **Provisioned** create real state in the target service; **Stub** resources
generate a placeholder ID without side effects.

### Resources that wait

Some services answer a create long before the thing they created is usable, and
CloudFormation does not pass that on: the resource is not `CREATE_COMPLETE`
until it settles, and anything downstream of it waits behind that. These
resource types stabilize in Overcast, matching AWS:

| Resource Type                        | Complete when                                    |
| ------------------------------------ | ------------------------------------------------ |
| `AWS::RDS::DBInstance`               | the instance reports `available`                 |
| `AWS::RDS::DBCluster`                | the cluster reports `available`                  |
| `AWS::ECS::Service`                  | the current deployment reaches its desired count |
| `AWS::ElastiCache::CacheCluster`     | the cluster reports `available`                  |
| `AWS::ElastiCache::ReplicationGroup` | the group reports `available`                    |
| `AWS::ElastiCache::ServerlessCache`  | the cache reports `available`                    |
| `AWS::MSK::Cluster`                  | the cluster reports `ACTIVE`                     |
| `AWS::EKS::Cluster`                  | the cluster reports `ACTIVE`                     |
| `AWS::EFS::FileSystem`               | the file system reports `available`              |
| `AWS::EFS::MountTarget`              | the mount target reports `available`             |
| `AWS::EFS::AccessPoint`              | the access point reports `available`             |
| `AWS::Lambda::Function`              | the function reports `Active`                    |

Status matching folds case, because AWS does not spell one service's vocabulary
consistently — a replication group reports `available` and AWS documents a
serverless cache's as `AVAILABLE`. An unrecognised status keeps the resource
waiting rather than completing it: AWS adds statuses, and completing on one
nothing here understands is the failure these waits exist to prevent.

Updates wait on the same condition, so an `UPDATE_COMPLETE` means the change was
applied *and* settled. A resource that cannot settle fails with the reason the
service itself gives — an RDS event, the newest actionable ECS service event
rather than a later task-start progress event, an MSK `stateInfo`, an EKS health
issue, a Lambda `StateReason` — and the stack rolls back. Where a service records
no reason of its own, the failure carries the status the resource reached and
what it was being waited for, which is what tells a resource still starting apart
from one wedged in a status that will never change. A failed update is never
answered by replacing the resource: the change has already been applied to the
one that exists, so a second copy would carry the same problem.

Which statuses end a wait comes from AWS's own machine-readable answer wherever
it has one — the waiters botocore ships for each service. `CacheClusterAvailable`,
`ReplicationGroupAvailable`, `ClusterActive` (EKS) and `FunctionActive` (Lambda)
are used as written; the vocabularies differ more than they look, and an
ElastiCache cache cluster in particular has no `create-failed` status at all.
Two gaps are filled from the API reference: a replication group's documented
`create-failed`, which its waiter predates, and MSK and EFS, for which botocore
ships no waiters.

A Lambda function is complete at `Active`, which is what real CloudFormation
waits for — an invoke, an `UpdateFunctionCode` or a `PublishVersion` against a
`Pending` function fails. `Active` means deployed, not working: a function with
a broken handler is `Active` on AWS too and fails at invoke, so the wait
deliberately stops there rather than proving the handler runs. AWS's separate
`FunctionUpdated` waiter reads `LastUpdateStatus` after an update; Overcast does
not model that field, so nothing waits on it yet.

Every wait runs whether or not the deployment has a container runtime for the
service behind it. A resource with no container coming reaches its ready status
as soon as it is recorded — there is no data plane being claimed, so there is
nothing left to prove — and the wait gets its answer on the first poll. This is
uniform across the services: an RDS instance, a Lambda function, a mock-mode EKS
cluster, an ElastiCache cache and an MSK cluster all behave the same way.

`AWS::ECS::Service.ForceNewDeployment` is translated to ECS's
`forceNewDeployment` update flag, so changing the nonce emitted by CDK launches
fresh tasks even when the task definition is unchanged. Those tasks resolve
Secrets Manager and SSM values again at container start.

The table below details physical ID formats and `Fn::GetAtt` attributes for the
most commonly used types. It is **not** the complete registry — for the full
132-type list see [cdk.md § Supported resource types](../cdk.md#supported-resource-types).

| Resource Type                              | Status      | Physical ID Format        | GetAtt Attributes                               |
| ------------------------------------------ | ----------- | ------------------------- | ----------------------------------------------- |
| `AWS::SQS::Queue`                          | Provisioned | ARN                       | QueueName, Arn, QueueUrl                        |
| `AWS::SQS::QueuePolicy`                    | Stub        | —                         | —                                               |
| `AWS::SNS::Topic`                          | Provisioned | ARN                       | TopicName, TopicArn                             |
| `AWS::SNS::Subscription`                   | Provisioned | ARN                       | —                                               |
| `AWS::S3::Bucket`                          | Provisioned | Bucket name               | Arn, BucketName, DomainName, RegionalDomainName |
| `AWS::S3::BucketPolicy`                    | Provisioned | Bucket name               | —                                               |
| `AWS::DynamoDB::Table`                     | Provisioned | Table name                | Arn, TableName, StreamArn                       |
| `AWS::Lambda::Function`                    | Provisioned | ARN                       | Arn, FunctionName                               |
| `AWS::Lambda::EventSourceMapping`          | Provisioned | UUID                      | —                                               |
| `AWS::Lambda::LayerVersion`                | Provisioned | Layer version ARN         | LayerVersionArn                                 |
| `AWS::Lambda::Permission`                  | Provisioned | `functionName\|statementId` | —                                             |
| `AWS::IAM::Role`                           | Provisioned | Role name                 | Arn, RoleId, RoleName                           |
| `AWS::IAM::User`                           | Provisioned | User name                 | —                                               |
| `AWS::IAM::Group`                          | Provisioned | Group name                | Arn, GroupName                                  |
| `AWS::IAM::Policy`                         | Provisioned | Stack-scoped name         | —                                               |
| `AWS::IAM::ManagedPolicy`                  | Provisioned | Policy ARN                | Arn                                             |
| `AWS::IAM::InstanceProfile`                | Provisioned | Instance profile ARN      | Arn                                             |
| `AWS::IAM::ServiceLinkedRole`              | Provisioned | Role ARN                  | Arn, RoleName                                   |
| `AWS::Logs::LogGroup`                      | Provisioned | Log group name            | Arn, LogGroupName                               |
| `AWS::Logs::LogStream`                     | Provisioned | Log stream name           | —                                               |
| `AWS::SSM::Parameter`                      | Provisioned | Parameter name            | Type, Value                                     |
| `AWS::SecretsManager::Secret`              | Provisioned | ARN                       | Arn, Name                                       |
| `AWS::EC2::VPC`                            | Provisioned | `vpc-xxxx`                | VpcId, CidrBlock                                |
| `AWS::EC2::Subnet`                         | Provisioned | `subnet-xxxx`             | SubnetId, VpcId, CidrBlock, AvailabilityZone    |
| `AWS::EC2::SecurityGroup`                  | Provisioned | `sg-xxxx`                 | GroupId, VpcId                                  |
| `AWS::EC2::InternetGateway`                | Provisioned | `igw-xxxx`                | InternetGatewayId                               |
| `AWS::EC2::VPCGatewayAttachment`           | Provisioned | `vpcId\|igwId`            | —                                               |
| `AWS::EC2::RouteTable`                     | Provisioned | `rtb-xxxx`                | RouteTableId                                    |
| `AWS::EC2::Route`                          | Provisioned | `rtbId\|cidr`             | —                                               |
| `AWS::EC2::SubnetRouteTableAssociation`    | Provisioned | `rtbassoc-xxxx`           | —                                               |
| `AWS::EC2::EIP`                            | Provisioned | `eipalloc-xxxx`           | AllocationId, PublicIp                          |
| `AWS::EC2::NatGateway`                     | Provisioned | `nat-xxxx`                | NatGatewayId                                    |
| `AWS::ECS::Cluster`                        | Provisioned | Cluster ARN               | Arn                                             |
| `AWS::ECS::TaskDefinition`                 | Provisioned | Task definition ARN       | TaskDefinitionArn                               |
| `AWS::ECS::Service`                        | Provisioned | Service ARN               | ServiceArn, Name                                |
| `AWS::KMS::Key`                            | Provisioned | Key ID (UUID)             | KeyId, Arn                                      |
| `AWS::KMS::Alias`                          | Provisioned | Alias name                | —                                               |
| `AWS::Events::EventBus`                    | Provisioned | Event bus ARN             | Arn, Name                                       |
| `AWS::Events::Rule`                        | Provisioned | Rule ARN                  | Arn                                             |
| `AWS::Scheduler::Schedule`                 | Provisioned | `groupName/name`          | Arn, GroupName, Name                            |
| `AWS::Scheduler::ScheduleGroup`            | Provisioned | Group ARN                 | Arn, Name                                       |
| `AWS::StepFunctions::StateMachine`         | Provisioned | State machine ARN         | Arn, Name                                       |
| `AWS::ApiGateway::RestApi`                 | Provisioned | REST API ID               | RestApiId, RootResourceId                       |
| `AWS::ApiGateway::Resource`                | Provisioned | `apiId/resourceId`        | ResourceId                                      |
| `AWS::ApiGateway::Method`                  | Provisioned | `apiId/resourceId/method` | —                                               |
| `AWS::ApiGateway::Deployment`              | Provisioned | `apiId/deploymentId`      | DeploymentId                                    |
| `AWS::ApiGateway::Stage`                   | Provisioned | `apiId/stageName`         | —                                               |
| `AWS::ApiGateway::Authorizer`               | Provisioned | `apiId/authorizerId`      | AuthorizerId                                    |
| `AWS::ApiGateway::Model`                    | Provisioned | `apiId/name`              | Name (Ref returns the model name, as on AWS)    |
| `AWS::ApiGateway::RequestValidator`         | Provisioned | `apiId/requestValidatorId` | RequestValidatorId                             |
| `AWS::ApiGateway::Account`                 | Stub        | —                         | —                                               |
| `AWS::ApiGatewayV2::Api`                   | Provisioned | API ID                    | ApiId, ApiEndpoint                              |
| `AWS::ApiGatewayV2::Stage`                 | Provisioned | `apiId/stageName`         | —                                               |
| `AWS::ApiGatewayV2::Integration`           | Provisioned | `apiId/integrationId`     | IntegrationId                                   |
| `AWS::ApiGatewayV2::Route`                 | Provisioned | `apiId/routeId`           | RouteId                                         |
| `AWS::AppSync::GraphQLApi`                 | Provisioned | API ID                    | ApiId, Arn, GraphQLUrl, RealtimeUrl, GraphQLDns, RealtimeDns |
| `AWS::AppSync::GraphQLSchema`              | Provisioned | `apiId/schema`            | Id                                              |
| `AWS::AppSync::ApiKey`                     | Provisioned | `apiId/keyId`             | Arn, ApiKey, ApiKeyId                           |
| `AWS::AppSync::DataSource`                 | Provisioned | `apiId/name`              | DataSourceArn, Name                             |
| `AWS::AppSync::Resolver`                   | Provisioned | `apiId/typeName/fieldName` | ResolverArn, FieldName, TypeName               |
| `AWS::AppSync::FunctionConfiguration`      | Provisioned | `apiId/functionId`        | FunctionArn, FunctionId, Name, DataSourceName   |
| `AWS::CDK::Metadata`                       | Stub        | —                         | —                                               |
| `Custom::*`                                | Provisioned | Lambda response or stub   | Lambda response Data or —                       |
| `AWS::CloudFormation::CustomResource`      | Provisioned | Lambda response or stub   | Lambda response Data or —                       |
| `AWS::CloudFormation::Stack`               | Provisioned | Child stack ARN           | Outputs.\*                                      |
| `AWS::CloudFormation::WaitConditionHandle` | Stub        | —                         | —                                               |
| `AWS::CloudFormation::WaitCondition`       | Stub        | —                         | —                                               |

Unknown resource types are accepted with a synthetic stub ID and a warning log, so templates
with unsupported resources can still partially deploy — and, per the note below, the resource's
`ResourceStatusReason` now says so too.

---

## Notes

Grouped by topic; each heading below is Ctrl+F/TOC-navigable on its own.

### JSON and YAML templates

Both JSON templates and YAML templates (including short-form tags like `!Ref`, `!Sub`, `!GetAtt`) are supported.

### Bounded async provisioner

Stack create/update/delete happens in background goroutines, but handlers wait briefly for fast stacks so SDK waiters can observe terminal status on their first `DescribeStacks` call. `OVERCAST_CFN_SYNC_WAIT_MS` controls the budget in milliseconds (default `1000`; `0` restores fully asynchronous returns). Resources are created via internal HTTP requests through the emulator's router, enabling CloudFormation to orchestrate any implemented service.

### Stacks are addressable by name or ARN — but a deleted stack only by ARN, as on AWS

Every stack-scoped operation's `StackName` member accepts either the stack name or the unique stack ID (the ARN `CreateStack` returned) [while the stack is live](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStacks.html): "Running stacks: You can specify either the stack's name or its unique stack ID. Deleted stacks: You must specify the unique stack ID." A stack's name becomes reusable the moment it reaches `DELETE_COMPLETE`, so from then on a name-based read (`DescribeStacks`, `DescribeStackEvents`, `ListStackResources`, `DescribeStackResources`, `GetTemplate`, `GetTemplateSummary`) answers `ValidationError: Stack with id <name> does not exist` — the same answer a name that was never created gets — while the ARN, which embeds the generation's uuid, still resolves the deleted stack's final state and events. This is what CDK's deploy monitor relies on: it polls by the ARN it already holds from the deploy, and the AWS SDK's `stack-delete-complete` waiter treats that ValidationError as the terminal success case alongside the status literally reading `DELETE_COMPLETE`, so a stack mid-`DELETE_IN_PROGRESS` still resolves by name and only the completed record excludes it. A stale ARN from a deleted-and-recreated stack of the same name resolves to nothing, since stacks are keyed by name (recreating overwrites the row) — the flip side is that the name resolves whatever currently occupies it, live or freshly recreated, the instant a new stack replaces a `DELETE_COMPLETE` one. Mutating operations go further and treat a `DELETE_COMPLETE` stack as nonexistent by either handle: `UpdateStack` and `CreateChangeSet` report it does not exist, and a repeat `DeleteStack` is a no-op success.

### How big a template may be

Both of AWS's [template quotas](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/cloudformation-limits.html) are enforced, so a template that is too large fails here rather than in the account. An inline `TemplateBody` is capped at 51,200 bytes and a larger one is refused with a `ValidationError` naming the limit; the cap is on the parameter, so `CreateStack`, `UpdateStack`, `CreateChangeSet`, `ValidateTemplate` and `GetTemplateSummary` all apply it. Past that size a template goes to S3 and travels as a `TemplateURL`, which lifts the limit to 1,000,000 bytes — a decimal megabyte, which is the number AWS's own error names. That second cap covers a nested stack's child template too, since it arrives through the same parameter. `aws cloudformation deploy` and `cdk deploy` switch to `TemplateURL` on your behalf; a hand-rolled `create-stack` with a large `--template-body` is the call that has to change.

### DependsOn

Resource dependency ordering is supported via topological sort.

### Status state machine

Stacks follow the full AWS status lifecycle: `CREATE_IN_PROGRESS` → `CREATE_COMPLETE` / `CREATE_FAILED`, etc., including both cleanup states (`UPDATE_COMPLETE_CLEANUP_IN_PROGRESS`, `UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS`). There is no separate "ordinary" rollback to set against a failure rollback: a rollback is always the response to a failure, or a `RollbackStack` call on a stack a failure already left in `CREATE_FAILED` / `UPDATE_FAILED`. What the status distinguishes is *which* operation unwound — `ROLLBACK_*` a create, `UPDATE_ROLLBACK_*` an update — and the two are not interchangeable for a client. `ROLLBACK_COMPLETE` has no last known stable state, so it is delete-only; `UPDATE_ROLLBACK_COMPLETE` is one of the [stable states](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RollbackStack.html) and can be updated again.

### Which state an operation may start from

The status is enforced, not merely reported. A create — `CreateStack`, or a change set with `ChangeSetType: CREATE`, at both `CreateChangeSet` and `ExecuteChangeSet` — is refused with `AlreadyExistsException` while a stack of that name exists; only the `DELETE_COMPLETE` tombstone frees the name, which is why the CDK CLI deletes a `ROLLBACK_COMPLETE` stack before deploying it again. A change set's own `REVIEW_IN_PROGRESS` placeholder is the one exception, and it is an exception for change sets only: `CreateStack` against that placeholder still fails, as it does on AWS. An update — `UpdateStack`, or a change set with `ChangeSetType: UPDATE` — is refused with `ValidationError` (`Stack:<arn> is in <status> state and can not be updated.`) from anything outside the [last known stable states](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RollbackStack.html): `CREATE_COMPLETE`, `UPDATE_COMPLETE`, `UPDATE_ROLLBACK_COMPLETE`, `IMPORT_COMPLETE`, `IMPORT_ROLLBACK_COMPLETE`. `CREATE_FAILED` and `UPDATE_FAILED` are the documented exception: an update may resume one of those when it sets `DisableRollback` (the CLI's `--disable-rollback`, the console's ["preserve successfully provisioned resources"](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/stack-failure-options.html)), which is how a failure that kept its resources is retried; without the option the update is still refused, and a change set may be created against those two statuses without it because the option belongs to the execution. `ROLLBACK_COMPLETE` is refused either way — it is recovered by deleting the stack and creating it again, and `RollbackStack` recovers the failed states it can. `UPDATE_ROLLBACK_FAILED` is refused by every update path too, and is the one state `ContinueUpdateRollback` exists for; `RollbackStack` also accepts it, because retrying the rollback is the same work either way.

### Where the rollback reason lives

In `StackStatusReason` and in each event's `ResourceStatusReason` — never in the status itself. Overcast sets it on every rollback path (`resource X failed: …` for an automatic rollback, `User Initiated` for `RollbackStack`) and clears it once the rollback reaches its terminal `*_ROLLBACK_COMPLETE` state, so on a `ROLLBACK_COMPLETE` stack the surviving explanation is the `ROLLBACK_IN_PROGRESS` event and the `CREATE_FAILED` resource that caused it. `ListStacks` summaries carry `StackStatusReason` alongside the status, as AWS's `StackSummary` does, so a list view can say why a stack failed without a second call.

### Where the reason lives when nothing rolls back

A create that fails with `DisableRollback` (`--disable-rollback`, `--no-rollback`, `OnFailure=DO_NOTHING`) has no rollback to explain, so the stack stops at `CREATE_FAILED` carrying AWS's summary of *which* resources failed — `The following resource(s) failed to create: [MyBucket]`, per [stack failure options](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/stack-failure-options.html) — rather than the underlying service error. That error is not lost: it stays on the resource's `ResourceStatusReason` and on its `CREATE_FAILED` event, which is where AWS keeps it and where a client looks for the detail.

### Why a deploy failed, after the rollback deleted the evidence

A rollback is faithful, which means it destroys the thing you needed to read. When an ECS service cannot keep its tasks alive, the best sentence CloudFormation can carry is the one ECS gives it — `(service Foo) is unable to consistently start tasks successfully` — and the actual answer, that container `app` exited 1 having printed `DATABASE_URL is not set`, is not expressible in CloudFormation's vocabulary at all. So Overcast reads the evidence at the moment a deploy is declared failed, *before* teardown, and keeps it under **`GET /_overcast/cloudformation/stacks/{stackName}/diagnostics`** — an emulator-only endpoint, not part of the AWS API surface, mirrored for the console at `/api/cloudformation/stacks/{stackName}/diagnostics`. What comes back is the CloudFormation reason, one sentence of Overcast's own reading of the evidence, and a set of panes each tagged with where it came from: `aws-api` for what `aws ecs describe-services` would also have returned, `overcast-capture` for what Overcast preserved and AWS discards too, `overcast-inference` for Overcast's interpretation. A counterfactual names what real AWS would have left you instead, because the question worth answering is whether the signal you are about to write a fix against is one you will also have in production. `AWS::ECS::Service` is the resource type covered today. Capture is best-effort and time-boxed, so a collector that cannot answer costs a missing pane and never a failed rollback; nothing gathered ever reaches an AWS-shaped field, and `DescribeStacks`, `DescribeStackEvents` and `ListStackResources` are byte-identical whether or not a diagnosis exists. A diagnosis is kept only for a stack whose *most recent* deploy failed — a successful deploy clears it, so the endpoint answers `404` for a healthy stack rather than explaining a failure that no longer applies. Environment variable values, secrets and resolved parameter values are never included, names only; a container's own output is reproduced verbatim because that is the diagnosis, but Overcast never adds a value the container did not print itself.

### What an update rollback puts back

An update writes the template, parameters and tags it was given onto the stack record before it touches a resource, so from that moment `DescribeStacks` and `GetTemplate` describe the attempt rather than what is deployed. A rollback therefore restores all three alongside the resource list, and a stack that reaches `UPDATE_ROLLBACK_COMPLETE` describes the generation it actually returned to. A nested stack restores its own, because a child whose update failed is never recorded by the parent as an in-place success and so is never reversed from above. Metadata the rollback could not persist fails it: the stack reports `UPDATE_ROLLBACK_FAILED` rather than a completion it did not achieve. An update that fails with `DisableRollback` keeps the attempted template and parameters, because nothing was unwound.

### Getting out of `UPDATE_ROLLBACK_FAILED`

A stack reaches it when an update fails and the automatic rollback fails too — usually because both attempts were blocked by the same thing outside the stack, a host port already bound or a resource something else is still holding. That state has no last known stable state to update from, so every later `UpdateStack`, `ExecuteChangeSet` and change set is refused, and the stack stays stuck until the rollback is finished. `ContinueUpdateRollback` is the way out, and is supported: clear the blocker by hand, call it, and the rollback resumes — retrying the deletes that failed, retiring what the failed attempt half-created, and driving the stack to `UPDATE_ROLLBACK_COMPLETE`, which is updatable again. A retry that meets the same blocker lands back in `UPDATE_ROLLBACK_FAILED` and can be tried again; the failure is reported on the stack and its events, not to the caller, which sees `200` either way. Nested stacks are continued before their parent, so a child left wedged is recovered rather than reported rolled back from above. `ResourcesToSkip` is honoured for a resource that cannot be cleaned up at all, including AWS's `NestedStackName.ResourceLogicalID` form for one inside a nested stack (and deeper paths, since nesting nests): the named resource is left physically untouched, reported `UPDATE_COMPLETE`, and the rest of the stack rolls back around it — AWS's own trade, a resource whose recorded state is now a fiction in exchange for a stack that works. Every member is validated before the operation is accepted, so a typo, or a resource the rollback did not actually fail on, is a `ValidationError` rather than a half-continued rollback. The operation is refused from any other status with the same `Stack:<arn> is in <status> state and can not be updated.` an update gets — `UPDATE_FAILED` included, where no rollback is under way to continue and `RollbackStack` is the call that starts one.

### Tracing an event back to the request that caused it

Every stack event carries a `ClientRequestToken`, as on AWS: the caller's, when the operation supplied one, and otherwise the request ID of the API call that started the operation. That fallback is what makes a failure traceable — the request ID is the key `/_overcast/debug/trace/{requestId}` is served under, so pasting an event's token into the trace viewer opens the request behind it, with every internal service call the operation made, their bodies and statuses, and the operation's log lines. The token belongs to the operation rather than the stack, so a create and a later update are distinguishable, and a nested stack's events carry the token of the parent operation that provisioned it. Real CloudFormation fills the field the same way when an operation arrives without one (the console's `Console-CreateStack-<uuid>`); the value is opaque to clients either way, and `OVERCAST_DEBUG` must be on for the trace itself to be retained.

### A limitation a resource carries is its `ResourceStatusReason`

A resource that Overcast creates but will not act on in full is provisioned, and the shortfall rides its status reason on the `CREATE_COMPLETE`/`UPDATE_COMPLETE` event so a deploy shows it as the resource goes by rather than only on a later describe. `AWS::CloudWatch::Alarm` is the current case: a metric-math, extended-statistic or anomaly-detection alarm is created and says it will not be evaluated (see [CloudWatch](./cloudwatch.md)). Refusing such a resource instead would fail the stack and the deploy over something the template is right to contain.

### Stub and inert-tier resources say so on the same channel

([#760](https://github.com/overcast-sh/overcast/issues/760)) A stack can reach `CREATE_COMPLETE` while parts of it do nothing, and three categories look identical — a green resource — but arise for different reasons: no handler is registered for the type at all (a synthetic stub ID, as above); a handler is registered but is a deliberate no-op (`AWS::CDK::Metadata` and around a dozen others); or a handler creates a real, describable resource whose owning service is registered at inert or stub tier (an `AWS::IAM::Role`, a `AWS::WAFv2::WebACL`), so nothing the resource does has any effect. One classification point turns a resource type and the handler it resolved to into a sentence prefixed `Overcast:`, which rides `ResourceStatusReason` exactly as the CloudWatch case above does, deferring to a handler's own more specific reason when one was already reported. A fully-backed resource gets nothing new. The console's stack view reads the same field: it colours an `Overcast:`-prefixed reason as an informational notice rather than a failure, and a stack with any such resource carries a count badge ("N of M stub or inert") next to its Resources heading. Nothing here enters `StackStatusReason` or otherwise changes the stack's own status — the signal is per-resource, and `DescribeStackEvents` is what a `cdk deploy` actually polls and renders.

### A persistence flush is not a stack failure

After a stack reaches a terminal status, its state is flushed to the persistent store so a restart straight afterwards still finds it. A flush that does not finish in time is logged and nothing more — the stack keeps the status it reached. Every resource in it exists and answers requests, and the queued writes are neither lost nor abandoned: they go back at the head of the pending queue for the next flush, and the pending log replays them after an unclean exit. Whether the store is keeping up is reported by `/_overcast/health` (`storage.persistent.pendingWrites`) and `/_overcast/debug/metrics` (`flushHistory`, which times every flush attempt), which is where a durability problem belongs — not in a `CREATE_FAILED` on a stack that provisioned.

### Fn::GetAtt

Returns real attribute values from service responses (e.g. `!GetAtt MyVpc.CidrBlock` returns `10.0.0.0/16`). Falls back to the physical resource ID when a specific attribute is not captured.

### Cross-stack references

`Fn::ImportValue` resolves exports from other active stacks in the same region. `ListExports` and `ListImports` return the export index.

### Custom resources

`Custom::*` and `AWS::CloudFormation::CustomResource` types invoke the Lambda function specified by `ServiceToken`. The handler sends a CloudFormation custom resource request to the Lambda and parses the response (`PhysicalResourceId`, `Data`). When Docker is unavailable (Lambda cannot execute), the handler degrades gracefully to a stub physical ID so the stack can still deploy.

### Nested stacks

`AWS::CloudFormation::Stack` is supported. The provisioner fetches the child template from the `TemplateURL` (typically an S3 object), creates a child stack, and provisions its resources synchronously within the parent's goroutine. Child stack outputs are exposed via `Fn::GetAtt` as `Outputs.<key>`. Deletion of nested stacks cascades — deleting the parent deletes all child resources, and a child that cannot be torn down fails the parent rather than reporting a deletion that did not happen.

### Scheduled ECS/Fargate tasks

`AWS::Events::Rule` provisions `Targets` through EventBridge on create/update, including ECS target parameters used by CDK scheduled Fargate tasks. EventBridge evaluates scheduled rules and invokes ECS/Fargate `RunTask` targets through the emulator router.

### S3 bucket sub-resources

`AWS::S3::Bucket` translates `LifecycleConfiguration`, `VersioningConfiguration`, `NotificationConfiguration`, `BucketEncryption`, `Tags`, `CorsConfiguration`, and `WebsiteConfiguration` into the existing S3 REST-XML APIs. `LifecycleConfiguration.TransitionDefaultMinimumObjectSize` travels as the `x-amz-transition-default-minimum-object-size` request header it maps to on AWS, rather than in the body. Updates keep the bucket and replace only changed configurations; removal deletes the configuration, clears notifications, or suspends versioning as AWS's irreversible versioning state requires. Unknown properties and fields the S3 API model cannot preserve fail the resource instead of being silently dropped.

### API Gateway properties

`AWS::ApiGateway::Stage.Variables` and `AWS::ApiGatewayV2::Stage.StageVariables` reach the stage on create and update — the service already read `stage.Variables` at execution time (a Lambda proxy integration's event `stageVariables`) and already accepted the field on `UpdateStage`/`UpdateV2Stage`, but the CloudFormation handlers never sent it. `Method.AuthorizerId` and `Route.AuthorizerId` round-trip the same way, `Method.RequestParameters` and `RestApi.Tags`/`Policy`/`BinaryMediaTypes`/`DisableExecuteApiEndpoint` are forwarded on create (and, for the `RestApi` scalars, on update), and `Method.MethodResponses` is applied via `PutMethodResponse`. `AWS::ApiGateway::Authorizer`, `AWS::ApiGateway::Model`, and `AWS::ApiGateway::RequestValidator` are provisioned resource types now — the service supported all three already, nothing dispatched to them. `RestApi.Body`/`BodyS3Location` and `ApiGatewayV2::Api.Body` (OpenAPI import) fail the resource instead of silently provisioning a routeless API, since neither is implemented. Left as documented gaps rather than wired or failed: `Method.RequestModels`/`RequestValidatorId`/`OperationName`/`AuthorizationScopes`, `Stage.MethodSettings`/`TracingEnabled`/`AccessLogSetting`/`CacheClusterEnabled`/`CacheClusterSize`/`ClientCertificateId`/`DocumentationVersion`, `RestApi.MinimumCompressionSize`/`ApiKeySourceType`/`Parameters`, `ApiGatewayV2::Api.Target`/`ApiKeySelectionExpression`, `ApiGatewayV2::Integration.RequestParameters`/`ResponseParameters`/`IntegrationSubtype`/`ConnectionId`/`CredentialsArn`/`PassthroughBehavior`/`TemplateSelectionExpression`, `ApiGatewayV2::Route.AuthorizationScopes`/`RequestParameterConstraints`/`OperationName`, and `ApiGatewayV2::Stage.DefaultRouteSettings`/`RouteSettings`/`AccessLogSettings` — none of these have a field on the service's domain types yet ([#528](https://github.com/overcast-sh/overcast/issues/528)).

### EC2 VPC networking properties

`AWS::EC2::VPC`'s `EnableDnsSupport`/`EnableDnsHostnames` and `AWS::EC2::Subnet`'s `MapPublicIpOnLaunch` reach the emulator's `ModifyVpcAttribute`/`ModifySubnetAttribute` calls on create, so a CDK `Vpc` construct — which always sets both DNS attributes, and `MapPublicIpOnLaunch` on every public subnet — is reflected by `DescribeVpcAttribute`/`DescribeSubnets` instead of the emulator's own `CreateVpc`/`CreateSubnet` defaults. Both resource types support in-place `Update` for these properties (and tags); changing `CidrBlock`/`InstanceTenancy` on a VPC or `VpcId`/`CidrBlock`/`AvailabilityZone` on a Subnet still replaces the resource, as on real AWS. `AWS::EC2::EIP` reads `Domain` and `Tags` from the template instead of hardcoding `Domain: vpc`, and `DescribeAddresses` returns the tags. What these properties change downstream — routing an ECS task, RDS instance, or Lambda ENI differently based on `EnableDnsHostnames` or `MapPublicIpOnLaunch` — is not implemented; this is storage, round-trip, and CloudFormation-threading only, tracked together with the data-plane behavior in [#529](https://github.com/overcast-sh/overcast/issues/529) and [#1100](https://github.com/overcast-sh/overcast/issues/1100). Left as documented gaps: `Subnet.Ipv6CidrBlock`/`AssignIpv6AddressOnCreation`/`EnableDns64`/`PrivateDnsNameOptionsOnLaunch`/`OutpostArn`, `VPC.Ipv4IpamPoolId`/`Ipv4NetmaskLength`, `NatGateway.ConnectivityType`/`PrivateIpAddress`, and `Route`'s IPv6/ENI/transit-gateway/peering/carrier-gateway destination properties — none of these have a field on the service's domain types yet.

### AppSync stacks

`AWS::AppSync::GraphQLApi`, `GraphQLSchema`, `ApiKey`, `DataSource`, `Resolver`, and `FunctionConfiguration` create real AppSync emulator state through the normal AppSync REST routes. CDK-style `Fn::GetAtt` wiring for API IDs, API keys, data source names, resolver ARNs, and function IDs is supported, and deployed APIs execute locally at `POST /_overcast/appsync/apis/{apiId}/graphql`. `GraphQLSchema` delete is a no-op because AppSync has no schema-delete API; deleting the parent API cascades child state cleanup.

### Generated secrets

`AWS::SecretsManager::Secret` supports `GenerateSecretString`. The password is generated at create time through Secrets Manager's own `GetRandomPassword` (honouring `PasswordLength`, the `Exclude*` settings, `IncludeSpace`, and `RequireEachIncludedType`), and `SecretStringTemplate` + `GenerateStringKey` place it inside the template's JSON object — the shape CDK's `new Secret(..., { generateSecretString })` produces. The template must be a JSON object and may not already contain `GenerateStringKey`; validation completes before the secret is created. `KmsKeyId`, resource tags, and propagated stack tags are applied through Secrets Manager's own APIs. Specifying both `SecretString` and `GenerateSecretString` fails the resource. Unlike AWS, changing `GenerateSecretString` on a stack update does not yet create a new secret version ([#678](https://github.com/overcast-sh/overcast/issues/678)). Replica regions, target attachments, and rotation schedules are tracked separately in [#679](https://github.com/overcast-sh/overcast/issues/679), [#680](https://github.com/overcast-sh/overcast/issues/680), and [#681](https://github.com/overcast-sh/overcast/issues/681).

### Change sets

`ExecuteChangeSet` now advances `ExecutionStatus` to `EXECUTE_COMPLETE` or `EXECUTE_FAILED` when the triggered stack provisioning reaches a terminal status, so clients polling `DescribeChangeSet` do not remain stuck in `EXECUTE_IN_PROGRESS`.

### Stack updates and drift

`UpdateStack` (and `ExecuteChangeSet`) detect property drift per-resource via a sha256 hash of the resolved property map stored alongside each `StackResource`. Resources whose hash is unchanged are skipped. When a resource's properties change, the provisioner picks one of three strategies, in order:
  1. **In-place update** — when the resource handler implements an `Update` method, mutable properties are applied via the service's mutation API. Implemented for:
     - `AWS::Lambda::Function` — `UpdateFunctionCode` + `UpdateFunctionConfiguration`
     - `AWS::SQS::Queue` — `SetQueueAttributes`
     - `AWS::SNS::Topic` — `SetTopicAttributes` (DisplayName, KmsMasterKeyId)
     - `AWS::DynamoDB::Table` — `UpdateTable` (BillingMode, ProvisionedThroughput, AttributeDefinitions, StreamSpecification) + `UpdateTimeToLive`
     - `AWS::SecretsManager::Secret` — `UpdateSecret` (Description, SecretString, KmsKeyId) plus tag reconciliation
     - `AWS::SSM::Parameter` — `PutParameter` with `Overwrite=true`
     - `AWS::Logs::LogGroup` — `PutRetentionPolicy` / `DeleteRetentionPolicy`
     - `AWS::CloudWatch::Alarm` — `PutMetricAlarm`, plus `TagResource` / `UntagResource` for tag changes. The tag calls are not redundant: `PutMetricAlarm` applies `Tags` only when it creates an alarm and ignores them when it updates one, as on AWS (see [CloudWatch](./cloudwatch.md)), so an update that changed only the tags would otherwise change nothing
     - `AWS::IAM::Role` — `UpdateAssumeRolePolicy`, `UpdateRole` (Description, MaxSessionDuration), `Put`/`DeleteRolePermissionsBoundary`, `Tag`/`UntagRole`, and add/remove reconciliation of `ManagedPolicyArns` (`Attach`/`DetachRolePolicy`) and inline `Policies` (`Put`/`DeleteRolePolicy`); a mutation that fails is compensated so the role is left as it was
     - `AWS::IAM::User` — `UpdateUser` (Path), `Put`/`DeleteUserPermissionsBoundary`, `Tag`/`UntagUser`, and add/remove reconciliation of `Groups`, `ManagedPolicyArns` and inline `Policies`
     - `AWS::IAM::Group` — add/remove reconciliation of `ManagedPolicyArns` and inline `Policies`
     - `AWS::IAM::ManagedPolicy` — add/remove reconciliation of the `Roles`/`Users`/`Groups` attachment lists, and `CreatePolicyVersion` (`SetAsDefault=true`) for `PolicyDocument` changes
     - `AWS::Scheduler::Schedule` — `UpdateSchedule`, which replaces the whole schedule as AWS's own operation does (see [Scheduler](./scheduler.md)); `Name` and `GroupName` are the resource's only createOnly properties
     - `AWS::AppSync::GraphQLApi` — `UpdateGraphqlApi` for mutable API config
     - `AWS::AppSync::GraphQLSchema` — `StartSchemaCreation` for schema definition changes
     - `AWS::AppSync::ApiKey` — `UpdateApiKey` for description/expiration changes
     - `AWS::AppSync::DataSource` — `UpdateDataSource` for mutable data source config
     - `AWS::AppSync::Resolver` — `UpdateResolver` for templates, runtime, pipeline, caching, and metrics config
     - `AWS::AppSync::FunctionConfiguration` — `UpdateFunction` for mutable function config
     - `AWS::S3::Bucket` — S3 lifecycle, versioning, notification, encryption, tagging, CORS, and website PUT/delete operations
  2. **Replacement (delete + create)** — when an Update method signals replacement (immutable property changed) or no `Update` method is registered for the resource type. Immutable identity properties trigger this path:
     - `BucketName` (S3), `TableName` / `KeySchema` (DynamoDB), `QueueName` / `FifoQueue` (SQS), `TopicName` (SNS), `Name` (SSM Parameter, SecretsManager Secret), `LogGroupName` (Logs), `RoleName` (IAM Role), `FunctionName` (Lambda), `Name` / `GroupName` (Scheduler Schedule).
  3. **Retain on replace** — `UpdateReplacePolicy: Retain` (or `Snapshot`) skips deletion of the old resource so the new one is created and the old one is orphaned, no longer tracked by the stack. This matches AWS CloudFormation behaviour.
### DeletionPolicy

Honoured. `DeletionPolicy: Retain` skips deletion when a create rolls back, when an update rolls back over a resource that update created, when the stack is deleted, or when a resource is removed from the template on update — the resource is orphaned and a `DELETE_SKIPPED` event is emitted. An update rollback drops the retained resource from the stack's resource list along with the rest of what the failed update created, so the stack that reaches `UPDATE_ROLLBACK_COMPLETE` is the pre-update one. The *replacement* a failed update created is deleted regardless of `DeletionPolicy`: the original is still standing and is what the stack rolls back to. `RetainExceptOnCreate` deletes the resource during its initial create rollback and retains it during ordinary deletion. The `RetainExceptOnCreate` **operation** option does the same for a resource whose template policy is `Retain`: set it on `CreateStack`, `UpdateStack`, `ExecuteChangeSet` or `RollbackStack` and the rollback deletes what that operation created instead of orphaning it. It defaults to `false`, as on AWS — so a first deploy that fails partway leaves a `Retain` resource standing, and the next deploy of the same template collides with it. `CreateChangeSet` does not take the option — as on AWS, the choice belongs to `ExecuteChangeSet`. `Snapshot` is treated the same as `Retain` (snapshots are not actually taken).

### Teardown failure

A delete reports success only when the resource is gone, and there are two ways for it to be gone: the delete removed it, or it was not there to begin with. An absent resource is always a successful teardown — nothing may wedge a stack over a resource that no longer exists — and every other outcome is reported. What the stack does with it depends on which teardown is running:

- A **rollback** — of a failed `CreateStack`, of a failed `UpdateStack`, or an explicit `RollbackStack` — emits `DELETE_FAILED` carrying the service's own error, keeps the resource in the stack's resource list, and reaches `ROLLBACK_FAILED` or `UPDATE_ROLLBACK_FAILED`. This is what AWS does, and it is the signal an operator needs: the stack did not clean up after itself and the resource is still standing. Keeping the resource listed also stops the next `UpdateStack` from treating it as new and creating a second copy alongside it — and is what lets `ContinueUpdateRollback` retry exactly those deletes once the operator has cleared what blocked them.
- **`DeleteStack`** stops at the resource: it emits `DELETE_FAILED`, leaves the stack `DELETE_FAILED` with the resource still listed so a retry knows what is standing, and goes no further. Reporting `DELETE_COMPLETE` over it would also drop the stack's record of the resource, which is the only thing still naming what is out there. Some of these are *refusals* — a lasting condition to clear before a retry can get past it:
  - IAM's `DeleteConflict` (HTTP 409) — `DeleteRole`, `DeleteUser` and `DeletePolicy` refuse while the entity still has a dependency the stack cannot clear for itself (see [IAM](./iam.md)).
  - A non-empty `AWS::S3::Bucket`.
  - An `AWS::RDS::DBCluster` with `DeletionProtection` enabled.
  - A nested `AWS::CloudFormation::Stack` whose own teardown failed.

  The rest are failures a retry may clear on its own. Either way the resource is still standing, which is why both stop the teardown. Note that `DeleteStack`'s `RetainResources` option is not implemented, so a resource that keeps refusing cannot yet be skipped past — clear the cause and call `DeleteStack` again.
- The **cleanup phase after a successful update**, which removes resources the new template dropped, emits `DELETE_FAILED` for the resource and still completes the stack. AWS runs this phase after the update has already succeeded and does not roll it back, so the event says the resource is still standing rather than either lying about the delete or failing an update that worked.

Resources deleted before the failure stay deleted — CloudFormation does not roll a delete back — so clearing the cause and calling `DeleteStack` again resumes from what is left.

### Legacy state compatibility

Stacks created before property-hash tracking was introduced have an empty hash; on the first update, missing-hash resources are treated as unchanged so re-running an unchanged template (for example `cdk bootstrap`) is a no-op rather than a destructive replace.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category             | ✅ Supported | ❌ Unsupported |
| -------------------- | ------------ | -------------- |
| Stacks               | 7            | 5              |
| Change sets          | 5            |                |
| Resources and events | 3            |                |
| Templates            | 3            | 1              |
| Exports              | 2            |                |
| Intrinsic functions  | 1            |                |
| Dynamic references   | 3            | 1              |
| Resource types       |              | 1              |
| StackSets            |              | 13             |
| Type registry        |              | 7              |

---

## Endpoints

### Stacks

| Operation                | Status         | Notes                                                                                                                                       | AWS Docs                                                                                                  |
| ------------------------ | -------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `CreateStack`            | ✅ Supported   | Async provisioner; JSON templates; intrinsic functions                                                                                      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CreateStack.html)            |
| `UpdateStack`            | ✅ Supported   | Re-provisions with updated template                                                                                                         | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_UpdateStack.html)            |
| `RollbackStack`          | ✅ Supported   | Rolls a CREATE_FAILED, UPDATE_FAILED, or UPDATE_ROLLBACK_FAILED stack back to a terminal rollback state                                     | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RollbackStack.html)          |
| `ContinueUpdateRollback` | ✅ Supported   | Retries a failed update rollback from UPDATE_ROLLBACK_FAILED; ResourcesToSkip, including nested-stack paths                                 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ContinueUpdateRollback.html) |
| `DeleteStack`            | ✅ Supported   | Async resource cleanup in reverse dependency order; DELETE_FAILED when a resource refuses deletion                                          | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DeleteStack.html)            |
| `DescribeStacks`         | ✅ Supported   | Status, parameters, outputs, tags                                                                                                           | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStacks.html)         |
| `ListStacks`             | ✅ Supported   | StackStatusFilter, validated against the full StackStatus enum; unfiltered lists include DELETE_COMPLETE; summaries carry StackStatusReason | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStacks.html)             |
| `CancelUpdateStack`      | ❌ Unsupported | stub; returns 501                                                                                                                           | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CancelUpdateStack.html)      |
| `SignalResource`         | ❌ Unsupported | stub; returns 501                                                                                                                           | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_SignalResource.html)         |
| `GetStackPolicy`         | ❌ Unsupported | stub; returns 501                                                                                                                           | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_GetStackPolicy.html)         |
| `SetStackPolicy`         | ❌ Unsupported | stub; returns 501                                                                                                                           | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_SetStackPolicy.html)         |
| `DescribeAccountLimits`  | ❌ Unsupported | stub; returns 501                                                                                                                           | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeAccountLimits.html)  |

### Change sets

| Operation           | Status       | Notes                                                               | AWS Docs                                                                                             |
| ------------------- | ------------ | ------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `CreateChangeSet`   | ✅ Supported | Creates a change set from a template                                | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CreateChangeSet.html)   |
| `DescribeChangeSet` | ✅ Supported | Returns change set details and status; accepts ARN-only lookup      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeChangeSet.html) |
| `ExecuteChangeSet`  | ✅ Supported | Provisions resources via async provisioner; accepts ARN-only lookup | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ExecuteChangeSet.html)  |
| `DeleteChangeSet`   | ✅ Supported |                                                                     | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DeleteChangeSet.html)   |
| `ListChangeSets`    | ✅ Supported | Lists active change sets for a stack                                | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListChangeSets.html)    |

### Resources and events

| Operation                | Status       | Notes                                               | AWS Docs                                                                                                  |
| ------------------------ | ------------ | --------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `DescribeStackResources` | ✅ Supported | Lists resources for a stack, with status and reason | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStackResources.html) |
| `ListStackResources`     | ✅ Supported | Lists resources with status                         | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStackResources.html)     |
| `DescribeStackEvents`    | ✅ Supported | Returns stack provisioning events                   | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStackEvents.html)    |

### Templates

| Operation              | Status         | Notes                                 | AWS Docs                                                                                                |
| ---------------------- | -------------- | ------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `GetTemplate`          | ✅ Supported   | Returns the stack's template body     | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_GetTemplate.html)          |
| `GetTemplateSummary`   | ✅ Supported   | Returns parameters and resource types | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_GetTemplateSummary.html)   |
| `ValidateTemplate`     | ✅ Supported   | Validates template syntax             | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ValidateTemplate.html)     |
| `EstimateTemplateCost` | ❌ Unsupported | stub; returns 501                     | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_EstimateTemplateCost.html) |

### Exports

| Operation     | Status       | Notes                                            | AWS Docs                                                                                       |
| ------------- | ------------ | ------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `ListExports` | ✅ Supported | Returns exports from all active stacks in region | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListExports.html) |
| `ListImports` | ✅ Supported | Returns stacks that import a given export name   | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListImports.html) |

### Intrinsic functions

| Operation         | Status       | Notes                            | AWS Docs                                                                                                             |
| ----------------- | ------------ | -------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `Fn::ImportValue` | ✅ Supported | Cross-stack reference resolution | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/intrinsic-function-reference-importvalue.html) |

### Dynamic references

| Operation                    | Status         | Notes                                                                   | AWS Docs                                                                                                                         |
| ---------------------------- | -------------- | ----------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `{{resolve:secretsmanager}}` | ✅ Supported   | Secret by name or ARN; JSON key, version stage and version ID selectors | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html#dynamic-references-secretsmanager) |
| `{{resolve:ssm}}`            | ✅ Supported   | Plaintext parameter; an explicit version resolves to the current value  | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html#dynamic-references-ssm)            |
| `{{resolve:ssm-secure}}`     | ✅ Supported   | Read with decryption; an explicit version resolves to the current value | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html#dynamic-references-ssm-secure)     |
| `{{resolve:s3}}`             | ❌ Unsupported | Not resolved; fails the resource that uses it                           | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/dynamic-references.html)                                   |

### Resource types

| Operation                                  | Status         | Notes | AWS Docs                                                                                                                    |
| ------------------------------------------ | -------------- | ----- | --------------------------------------------------------------------------------------------------------------------------- |
| `AWS::CloudFormation::WaitConditionHandle` | ❌ Unsupported | Stub  | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-resource-cloudformation-waitconditionhandle.html) |

### StackSets

| Operation                      | Status         | Notes             | AWS Docs                                                                                                        |
| ------------------------------ | -------------- | ----------------- | --------------------------------------------------------------------------------------------------------------- |
| `CreateStackSet`               | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CreateStackSet.html)               |
| `CreateStackInstances`         | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CreateStackInstances.html)         |
| `DeleteStackSet`               | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DeleteStackSet.html)               |
| `DeleteStackInstances`         | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DeleteStackInstances.html)         |
| `DescribeStackSet`             | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStackSet.html)             |
| `DescribeStackInstance`        | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStackInstance.html)        |
| `DescribeStackSetOperation`    | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStackSetOperation.html)    |
| `ListStackSets`                | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStackSets.html)                |
| `ListStackInstances`           | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStackInstances.html)           |
| `ListStackSetOperations`       | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStackSetOperations.html)       |
| `ListStackSetOperationResults` | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStackSetOperationResults.html) |
| `UpdateStackSet`               | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_UpdateStackSet.html)               |
| `UpdateStackInstances`         | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_UpdateStackInstances.html)         |

### Type registry

| Operation                  | Status         | Notes             | AWS Docs                                                                                                    |
| -------------------------- | -------------- | ----------------- | ----------------------------------------------------------------------------------------------------------- |
| `RegisterType`             | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RegisterType.html)             |
| `DeregisterType`           | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DeregisterType.html)           |
| `DescribeType`             | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeType.html)             |
| `DescribeTypeRegistration` | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeTypeRegistration.html) |
| `ListTypes`                | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListTypes.html)                |
| `ListTypeRegistrations`    | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListTypeRegistrations.html)    |
| `SetTypeDefaultVersion`    | ❌ Unsupported | stub; returns 501 | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_SetTypeDefaultVersion.html)    |

<!-- END overcast:capabilities -->
