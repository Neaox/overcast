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
  template, exactly as on AWS.
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

The provisioner dispatches internal HTTP requests to the emulated services. Resources
listed as **Provisioned** create real state in the target service; **Stub** resources
generate a placeholder ID without side effects.

### Resources that wait

Some services answer a create long before the thing they created is usable, and
CloudFormation does not pass that on: the resource is not `CREATE_COMPLETE`
until it settles, and anything downstream of it waits behind that. Three
resource types stabilize in Overcast, matching AWS:

| Resource Type              | Complete when                                   |
| -------------------------- | ----------------------------------------------- |
| `AWS::RDS::DBInstance`     | the instance reports `available`                |
| `AWS::RDS::DBCluster`      | the cluster reports `available`                 |
| `AWS::ECS::Service`        | the current deployment reaches its desired count |

Updates wait on the same condition, so an `UPDATE_COMPLETE` means the change was
applied *and* settled. A resource that cannot settle fails with the reason the
service itself gives — an RDS event, an ECS service event — and the stack rolls
back, deleting the resource it created. A failed update is never answered by
replacing the resource: the change has already been applied to the one that
exists, so a second copy would carry the same problem.

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
| `AWS::Lambda::Permission`                  | Stub        | —                         | —                                               |
| `AWS::IAM::Role`                           | Provisioned | ARN                       | Arn, RoleId, RoleName                           |
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
| `AWS::StepFunctions::StateMachine`         | Provisioned | State machine ARN         | Arn, Name                                       |
| `AWS::ApiGateway::RestApi`                 | Provisioned | REST API ID               | RestApiId, RootResourceId                       |
| `AWS::ApiGateway::Resource`                | Provisioned | `apiId/resourceId`        | ResourceId                                      |
| `AWS::ApiGateway::Method`                  | Provisioned | `apiId/resourceId/method` | —                                               |
| `AWS::ApiGateway::Deployment`              | Provisioned | `apiId/deploymentId`      | DeploymentId                                    |
| `AWS::ApiGateway::Stage`                   | Provisioned | `apiId/stageName`         | —                                               |
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
with unsupported resources can still partially deploy.

---

## Notes

- **JSON and YAML templates.** Both JSON templates and YAML templates (including short-form tags like `!Ref`, `!Sub`, `!GetAtt`) are supported.
- **Bounded async provisioner.** Stack create/update/delete happens in background goroutines, but handlers wait briefly for fast stacks so SDK waiters can observe terminal status on their first `DescribeStacks` call. `OVERCAST_CFN_SYNC_WAIT_MS` controls the budget in milliseconds (default `1000`; `0` restores fully asynchronous returns). Resources are created via internal HTTP requests through the emulator's router, enabling CloudFormation to orchestrate any implemented service.
- **Stacks are addressable by name or ARN.** Every stack-scoped operation's `StackName` member accepts either the stack name or the unique stack ID (the ARN `CreateStack` returned), as on AWS. A deleted stack's record is retained, so its final state and events remain readable — by ARN, which is what CDK polls with while a delete completes, and, more leniently than AWS, by name too. A stale ARN from a deleted-and-recreated stack of the same name resolves to nothing. Mutating operations treat a `DELETE_COMPLETE` stack as nonexistent: `UpdateStack` and `CreateChangeSet` report it does not exist, and a repeat `DeleteStack` is a no-op success.
- **DependsOn.** Resource dependency ordering is supported via topological sort.
- **Status state machine.** Stacks follow the full AWS status lifecycle: `CREATE_IN_PROGRESS` → `CREATE_COMPLETE` / `CREATE_FAILED`, etc., including both cleanup states (`UPDATE_COMPLETE_CLEANUP_IN_PROGRESS`, `UPDATE_ROLLBACK_COMPLETE_CLEANUP_IN_PROGRESS`). There is no separate "ordinary" rollback to set against a failure rollback: a rollback is always the response to a failure, or a `RollbackStack` call on a stack a failure already left in `CREATE_FAILED` / `UPDATE_FAILED`. What the status distinguishes is *which* operation unwound — `ROLLBACK_*` a create, `UPDATE_ROLLBACK_*` an update — and the two are not interchangeable for a client. `ROLLBACK_COMPLETE` has no last known stable state, so it is delete-only; `UPDATE_ROLLBACK_COMPLETE` is one of the [stable states](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RollbackStack.html) and can be updated again.
- **Where the rollback reason lives.** In `StackStatusReason` and in each event's `ResourceStatusReason` — never in the status itself. Overcast sets it on every rollback path (`resource X failed: …` for an automatic rollback, `User Initiated` for `RollbackStack`) and clears it once the rollback reaches its terminal `*_ROLLBACK_COMPLETE` state, so on a `ROLLBACK_COMPLETE` stack the surviving explanation is the `ROLLBACK_IN_PROGRESS` event and the `CREATE_FAILED` resource that caused it. `ListStacks` summaries carry `StackStatusReason` alongside the status, as AWS's `StackSummary` does, so a list view can say why a stack failed without a second call.
- **What an update rollback puts back.** An update writes the template, parameters and tags it was given onto the stack record before it touches a resource, so from that moment `DescribeStacks` and `GetTemplate` describe the attempt rather than what is deployed. A rollback therefore restores all three alongside the resource list, and a stack that reaches `UPDATE_ROLLBACK_COMPLETE` describes the generation it actually returned to. A nested stack restores its own, because a child whose update failed is never recorded by the parent as an in-place success and so is never reversed from above. Metadata the rollback could not persist fails it: the stack reports `UPDATE_ROLLBACK_FAILED` rather than a completion it did not achieve. An update that fails with `DisableRollback` keeps the attempted template and parameters, because nothing was unwound.
- **Fn::GetAtt.** Returns real attribute values from service responses (e.g. `!GetAtt MyVpc.CidrBlock` returns `10.0.0.0/16`). Falls back to the physical resource ID when a specific attribute is not captured.
- **Cross-stack references.** `Fn::ImportValue` resolves exports from other active stacks in the same region. `ListExports` and `ListImports` return the export index.
- **Custom resources.** `Custom::*` and `AWS::CloudFormation::CustomResource` types invoke the Lambda function specified by `ServiceToken`. The handler sends a CloudFormation custom resource request to the Lambda and parses the response (`PhysicalResourceId`, `Data`). When Docker is unavailable (Lambda cannot execute), the handler degrades gracefully to a stub physical ID so the stack can still deploy.
- **Nested stacks.** `AWS::CloudFormation::Stack` is supported. The provisioner fetches the child template from the `TemplateURL` (typically an S3 object), creates a child stack, and provisions its resources synchronously within the parent's goroutine. Child stack outputs are exposed via `Fn::GetAtt` as `Outputs.<key>`. Deletion of nested stacks cascades — deleting the parent deletes all child resources, and a child that cannot be torn down fails the parent rather than reporting a deletion that did not happen.
- **Scheduled ECS/Fargate tasks.** `AWS::Events::Rule` provisions `Targets` through EventBridge on create/update, including ECS target parameters used by CDK scheduled Fargate tasks. EventBridge evaluates scheduled rules and invokes ECS/Fargate `RunTask` targets through the emulator router.
- **S3 bucket sub-resources.** `AWS::S3::Bucket` translates `LifecycleConfiguration`, `VersioningConfiguration`, `NotificationConfiguration`, `BucketEncryption`, `Tags`, `CorsConfiguration`, and `WebsiteConfiguration` into the existing S3 REST-XML APIs. `LifecycleConfiguration.TransitionDefaultMinimumObjectSize` travels as the `x-amz-transition-default-minimum-object-size` request header it maps to on AWS, rather than in the body. Updates keep the bucket and replace only changed configurations; removal deletes the configuration, clears notifications, or suspends versioning as AWS's irreversible versioning state requires. Unknown properties and fields the S3 API model cannot preserve fail the resource instead of being silently dropped.
- **AppSync stacks.** `AWS::AppSync::GraphQLApi`, `GraphQLSchema`, `ApiKey`, `DataSource`, `Resolver`, and `FunctionConfiguration` create real AppSync emulator state through the normal AppSync REST routes. CDK-style `Fn::GetAtt` wiring for API IDs, API keys, data source names, resolver ARNs, and function IDs is supported, and deployed APIs execute locally at `POST /_appsync/{apiId}/graphql`. `GraphQLSchema` delete is a no-op because AppSync has no schema-delete API; deleting the parent API cascades child state cleanup.
- **Generated secrets.** `AWS::SecretsManager::Secret` supports `GenerateSecretString`. The password is generated at create time through Secrets Manager's own `GetRandomPassword` (honouring `PasswordLength`, the `Exclude*` settings, `IncludeSpace`, and `RequireEachIncludedType`), and `SecretStringTemplate` + `GenerateStringKey` place it inside the template's JSON object — the shape CDK's `new Secret(..., { generateSecretString })` produces. The template must be a JSON object and may not already contain `GenerateStringKey`; validation completes before the secret is created. `KmsKeyId`, resource tags, and propagated stack tags are applied through Secrets Manager's own APIs. Specifying both `SecretString` and `GenerateSecretString` fails the resource. Unlike AWS, changing `GenerateSecretString` on a stack update does not yet create a new secret version ([#678](https://github.com/Neaox/overcast/issues/678)). Replica regions, target attachments, and rotation schedules are tracked separately in [#679](https://github.com/Neaox/overcast/issues/679), [#680](https://github.com/Neaox/overcast/issues/680), and [#681](https://github.com/Neaox/overcast/issues/681).
- **Change sets.** `ExecuteChangeSet` now advances `ExecutionStatus` to `EXECUTE_COMPLETE` or `EXECUTE_FAILED` when the triggered stack provisioning reaches a terminal status, so clients polling `DescribeChangeSet` do not remain stuck in `EXECUTE_IN_PROGRESS`.
- **Stack updates and drift.** `UpdateStack` (and `ExecuteChangeSet`) detect property drift per-resource via a sha256 hash of the resolved property map stored alongside each `StackResource`. Resources whose hash is unchanged are skipped. When a resource's properties change, the provisioner picks one of three strategies, in order:
  1. **In-place update** — when the resource handler implements an `Update` method, mutable properties are applied via the service's mutation API. Implemented for:
     - `AWS::Lambda::Function` — `UpdateFunctionCode` + `UpdateFunctionConfiguration`
     - `AWS::SQS::Queue` — `SetQueueAttributes`
     - `AWS::SNS::Topic` — `SetTopicAttributes` (DisplayName, KmsMasterKeyId)
     - `AWS::DynamoDB::Table` — `UpdateTable` (BillingMode, ProvisionedThroughput, AttributeDefinitions, StreamSpecification) + `UpdateTimeToLive`
     - `AWS::SecretsManager::Secret` — `UpdateSecret` (Description, SecretString, KmsKeyId) plus tag reconciliation
     - `AWS::SSM::Parameter` — `PutParameter` with `Overwrite=true`
     - `AWS::Logs::LogGroup` — `PutRetentionPolicy` / `DeleteRetentionPolicy`
     - `AWS::CloudWatch::Alarm` — `PutMetricAlarm`, plus `TagResource` / `UntagResource` for tag changes. The tag calls are not redundant: `PutMetricAlarm` applies `Tags` only when it creates an alarm and ignores them when it updates one, as on AWS (see [CloudWatch](./cloudwatch.md)), so an update that changed only the tags would otherwise change nothing
     - `AWS::IAM::Role` — `UpdateAssumeRolePolicy` + `UpdateRole` (Description)
     - `AWS::AppSync::GraphQLApi` — `UpdateGraphqlApi` for mutable API config
     - `AWS::AppSync::GraphQLSchema` — `StartSchemaCreation` for schema definition changes
     - `AWS::AppSync::ApiKey` — `UpdateApiKey` for description/expiration changes
     - `AWS::AppSync::DataSource` — `UpdateDataSource` for mutable data source config
     - `AWS::AppSync::Resolver` — `UpdateResolver` for templates, runtime, pipeline, caching, and metrics config
     - `AWS::AppSync::FunctionConfiguration` — `UpdateFunction` for mutable function config
     - `AWS::S3::Bucket` — S3 lifecycle, versioning, notification, encryption, tagging, CORS, and website PUT/delete operations
  2. **Replacement (delete + create)** — when an Update method signals replacement (immutable property changed) or no `Update` method is registered for the resource type. Immutable identity properties trigger this path:
     - `BucketName` (S3), `TableName` / `KeySchema` (DynamoDB), `QueueName` / `FifoQueue` (SQS), `TopicName` (SNS), `Name` (SSM Parameter, SecretsManager Secret), `LogGroupName` (Logs), `RoleName` (IAM Role), `FunctionName` (Lambda).
  3. **Retain on replace** — `UpdateReplacePolicy: Retain` (or `Snapshot`) skips deletion of the old resource so the new one is created and the old one is orphaned, no longer tracked by the stack. This matches AWS CloudFormation behaviour.
- **DeletionPolicy.** Honoured. `DeletionPolicy: Retain` skips deletion when the stack is deleted or a resource is removed from the template on update — the resource is orphaned and a `DELETE_SKIPPED` event is emitted. `Snapshot` is treated the same as `Retain` (snapshots are not actually taken).
- **Teardown failure.** A resource that *refuses* to be deleted fails the stack: `DeleteStack` stops at that resource, emits a `DELETE_FAILED` event carrying the service's own error, and leaves the stack `DELETE_FAILED` with the resource still listed so a retry knows what is standing. This is what AWS does — a refusal is a real condition the operator has to clear, not something to paper over with `DELETE_COMPLETE`. Today the refusals that reach the stack are:
  - IAM's `DeleteConflict` (HTTP 409) — `DeleteRole`, `DeleteUser` and `DeletePolicy` refuse while the entity still has a dependency the stack cannot clear for itself (see [IAM](./iam.md)).
  - A non-empty `AWS::S3::Bucket`.
  - An `AWS::RDS::DBCluster` with `DeletionProtection` enabled.
  - A nested `AWS::CloudFormation::Stack` whose own teardown failed.

  Everything else stays non-fatal, deliberately: a resource that is already gone, a call that could not be made, and a stub resource type must never wedge a teardown. Resources deleted before the failure stay deleted — CloudFormation does not roll a delete back — so clearing the dependency and calling `DeleteStack` again resumes from what is left.
- **Legacy state compatibility.** Stacks created before property-hash tracking was introduced have an empty hash; on the first update, missing-hash resources are treated as unchanged so re-running an unchanged template (for example `cdk bootstrap`) is a no-op rather than a destructive replace.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category             | ✅ Supported | ❌ Unsupported |
| -------------------- | ------------ | -------------- |
| Stacks               | 6            | 6              |
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

| Operation                | Status         | Notes                                                                                                   | AWS Docs                                                                                                  |
| ------------------------ | -------------- | ------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `CreateStack`            | ✅ Supported   | Async provisioner; JSON templates; intrinsic functions                                                  | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CreateStack.html)            |
| `UpdateStack`            | ✅ Supported   | Re-provisions with updated template                                                                     | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_UpdateStack.html)            |
| `RollbackStack`          | ✅ Supported   | Rolls a CREATE_FAILED, UPDATE_FAILED, or UPDATE_ROLLBACK_FAILED stack back to a terminal rollback state | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_RollbackStack.html)          |
| `DeleteStack`            | ✅ Supported   | Async resource cleanup in reverse dependency order; DELETE_FAILED when a resource refuses deletion      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DeleteStack.html)            |
| `DescribeStacks`         | ✅ Supported   | Status, parameters, outputs, tags                                                                       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStacks.html)         |
| `ListStacks`             | ✅ Supported   | StackStatusFilter; unfiltered lists include DELETE_COMPLETE; summaries carry StackStatusReason          | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStacks.html)             |
| `ContinueUpdateRollback` | ❌ Unsupported | stub; returns 501                                                                                       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ContinueUpdateRollback.html) |
| `CancelUpdateStack`      | ❌ Unsupported | stub; returns 501                                                                                       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CancelUpdateStack.html)      |
| `SignalResource`         | ❌ Unsupported | stub; returns 501                                                                                       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_SignalResource.html)         |
| `GetStackPolicy`         | ❌ Unsupported | stub; returns 501                                                                                       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_GetStackPolicy.html)         |
| `SetStackPolicy`         | ❌ Unsupported | stub; returns 501                                                                                       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_SetStackPolicy.html)         |
| `DescribeAccountLimits`  | ❌ Unsupported | stub; returns 501                                                                                       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeAccountLimits.html)  |

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
