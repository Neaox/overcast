# CloudFormation Update — coverage plan

> Status: P0 ✅, P1 ✅, P2 ✅, P3 ✅. Cross-cutting: resourceUpdater oldProps ✅.
> Remaining: ApiGateway PATCH builder (P1.2–P1.6 stubs), Kinesis UpdateShardCount.
> Goal: make `UpdateStack` / `ExecuteChangeSet` behave like real AWS.

This plan extends the existing property-hash drift detection in
[internal/services/cloudformation/provisioner.go](../../internal/services/cloudformation/provisioner.go).
The mechanics are already in place: `resourceUpdater` interface, sentinel
`errReplacementRequired` for "Replacement: Yes" properties, three-strategy
update path (in-place / replacement / retain-on-replace) — see
[docs/services/cloudformation.md § Stack updates and drift](../services/cloudformation.md).

What's missing is **handler-level coverage** plus two protocol-level bugs.
Items are listed in priority order. Each entry names the AWS API to call,
the immutable identity property that should signal replacement, and the
mutable properties to apply in place.

## P0 — Protocol-level correctness bugs

These are wrong today regardless of which handlers have `Update` methods.

### P0.1 — Custom resources: send `RequestType: Update` ✅

[customResourceHandler](../../internal/services/cloudformation/provisioner.go)
now sends `RequestType: Update` with `OldResourceProperties` populated.
Implemented via `customResourceHandler.Update` method.

### P0.2 — Nested stacks: call `UpdateStack` ✅

`nestedStackHandler` now has an `Update` method that fetches the new
template, resolves parameters, and calls `updateStackResources`
synchronously on the child stack.

## P1 — High impact (every dev iteration)

### P1.1 — `AWS::StepFunctions::StateMachine` ✅

Calls `UpdateStateMachine`. Mutable: Definition, RoleArn. Replacement: StateMachineName, Type.

### P1.2 — `AWS::ApiGateway::RestApi` ✅ (stub)

Returns `errReplacementRequired` — full update needs ApiGateway PATCH builder (cross-cutting).

### P1.3 — `AWS::ApiGateway::Stage` ✅ (stub)

Returns `errReplacementRequired` — full update needs ApiGateway PATCH builder.

### P1.4 — `AWS::ApiGateway::Deployment` ✅

Always returns `errReplacementRequired` — deployments are immutable in AWS.

### P1.5 — `AWS::ApiGateway::Method` ✅ (stub)

Returns `errReplacementRequired` — full update needs ApiGateway PATCH builder.

### P1.6 — `AWS::ApiGateway::UsagePlan` ✅ (stub)

Returns `errReplacementRequired` — full update needs ApiGateway PATCH builder.

### P1.7 — `AWS::Events::Rule` ✅

Calls `PutRule` (idempotent) + target diffing via RemoveTargets/PutTargets.

### P1.8 — `AWS::Lambda::EventSourceMapping` ✅

Calls PUT `/2015-03-31/event-source-mappings/{uuid}` (UpdateEventSourceMapping).

### P1.9 — `AWS::S3::BucketPolicy` ✅

Calls `PUT /{bucket}?policy` (PutBucketPolicy — idempotent).

### P1.10 — `AWS::IAM::Policy` (inline policy) ✅

Calls `PutRolePolicy`/`PutGroupPolicy`/`PutUserPolicy` (idempotent overwrite).

### P1.11 — `AWS::IAM::ManagedPolicy` ✅

Calls `CreatePolicyVersion` with `SetAsDefault=true`.

## P2 — Medium impact ✅ (13 REAL + 4 stubs)

### P2.1 — `AWS::ECS::Service`

- API: `AmazonEC2ContainerServiceV20141113.UpdateService`
- Mutable: `DesiredCount`, `TaskDefinition`, `DeploymentConfiguration`,
  `NetworkConfiguration`, `PlacementStrategies`, `PlacementConstraints`,
  `HealthCheckGracePeriodSeconds`, `EnableExecuteCommand`
- Replacement: `ServiceName`, `Cluster`, `LaunchType`,
  `SchedulingStrategy`, `LoadBalancers` (some)

### P2.2 — `AWS::Cognito::UserPool`

- API: `AWSCognitoIdentityProviderService.UpdateUserPool`
- Mutable: `Policies`, `MfaConfiguration`, `LambdaConfig`,
  `EmailConfiguration`, `SmsConfiguration`, `AutoVerifiedAttributes`,
  `AdminCreateUserConfig`, `UserPoolTags`, `DeviceConfiguration`,
  `Schema` (additive only)
- Replacement: `UserPoolName`, schema deletes

### P2.3 — `AWS::Cognito::UserPoolClient`

- API: `AWSCognitoIdentityProviderService.UpdateUserPoolClient`
- Mutable: nearly every property except `ClientName` and `UserPoolId`
- Replacement: `ClientName`, `UserPoolId`

### P2.4 — `AWS::AppSync::GraphQLApi`

- API: `POST /v1/apis/{apiId}` (UpdateGraphqlApi) +
  `PUT /v1/apis/{apiId}/schemacreation` (StartSchemaCreation) when schema changes
- Mutable: `Name`, `AuthenticationType`, `LogConfig`, `UserPoolConfig`,
  `OpenIDConnectConfig`, `XrayEnabled`, schema text
- Replacement: `ApiType`

### P2.5 — `AWS::AppSync::DataSource`

- API: `POST /v1/apis/{apiId}/datasources/{name}` (UpdateDataSource)
- Mutable: `Type`, `Description`, `ServiceRoleArn`, all source-specific
  configs (`DynamoDBConfig`, `LambdaConfig`, `HttpConfig`, etc.)
- Replacement: `Name`, `ApiId`

### P2.6 — `AWS::AppSync::Resolver`

- API: `POST /v1/apis/{apiId}/types/{typeName}/resolvers/{fieldName}` (UpdateResolver)
- Mutable: `RequestMappingTemplate`, `ResponseMappingTemplate`, `DataSourceName`, `Kind`, `PipelineConfig`, `CachingConfig`
- Replacement: `TypeName`, `FieldName`, `ApiId`

### P2.7 — `AWS::KMS::Key`

- APIs: `TrentService.UpdateKeyDescription`, `TrentService.PutKeyPolicy`,
  `TrentService.EnableKey` / `DisableKey`
- Mutable: `Description`, `KeyPolicy`, `Enabled`, `EnableKeyRotation`
  (via `EnableKeyRotation`/`DisableKeyRotation`)
- Replacement: `KeySpec`, `KeyUsage`, `Origin`, `MultiRegion`

### P2.8 — `AWS::Kinesis::Stream`

- APIs: `Kinesis_20131202.UpdateShardCount`,
  `IncreaseStreamRetentionPeriod` / `DecreaseStreamRetentionPeriod`,
  `EnableEnhancedMonitoring` / `DisableEnhancedMonitoring`
- Mutable: `ShardCount` (UPLIFT only in real AWS),
  `RetentionPeriodHours`, `StreamModeDetails`, `StreamEncryption`
- Replacement: `Name`

### P2.9 — `AWS::ElastiCache::CacheCluster`

- API: `ModifyCacheCluster`
- Mutable: `NumCacheNodes`, `CacheNodeType`, `EngineVersion`,
  `SecurityGroupIds`, `PreferredMaintenanceWindow`, `NotificationTopicArn`
- Replacement: `ClusterName`, `Engine`, `CacheSubnetGroupName`

### P2.10 — `AWS::ElastiCache::ReplicationGroup`

- API: `ModifyReplicationGroup`
- Mutable: most operational properties
- Replacement: `ReplicationGroupId`, `Engine`, `CacheSubnetGroupName`

### P2.11 — `AWS::CloudFront::Distribution`

- API: `GET /2020-05-31/distribution/{id}/config` then `PUT` (UpdateDistribution)
- Mutable: nearly all `DistributionConfig` properties
- Replacement: `CallerReference`

### P2.12 — `AWS::RDS::DBInstance`

- API: `ModifyDBInstance`
- Mutable: `AllocatedStorage`, `DBInstanceClass`, `EngineVersion`,
  `MasterUserPassword`, `BackupRetentionPeriod`,
  `PreferredBackupWindow`, `PreferredMaintenanceWindow`,
  `MultiAZ`, `VpcSecurityGroups`, `DBParameterGroupName`,
  `PubliclyAccessible`, `StorageType`, `AllowMajorVersionUpgrade`,
  `AutoMinorVersionUpgrade`, `Iops`, `StorageEncrypted`,
  `DeletionProtection`
- Replacement: `DBInstanceIdentifier`, `Engine` (cross-engine),
  `DBSubnetGroupName`, `AvailabilityZone`, `CharacterSetName`,
  `DBClusterIdentifier`

### P2.13 — `AWS::RDS::DBCluster`

- API: `ModifyDBCluster`
- Mutable: `MasterUserPassword`, `BackupRetentionPeriod`,
  `PreferredBackupWindow`, `PreferredMaintenanceWindow`,
  `VpcSecurityGroupIds`, `DBClusterParameterGroupName`,
  `Port`, `EnableCloudwatchLogsExports`, `DeletionProtection`
- Replacement: `DBClusterIdentifier`, `Engine`, `DBSubnetGroupName`

### P2.14 — `AWS::RDS::DBParameterGroup`

- API: `ModifyDBParameterGroup`
- Mutable: `Parameters`, `Tags`
- Replacement: `DBParameterGroupName`, `Family`, `Description`

### P2.15 — `AWS::ServiceCatalogAppRegistry::Application`

- API: `UpdateApplication` (PATCH `/applications/{id}`)
- Mutable: `Name`, `Description`
- Replacement: none common

### P2.16 — `AWS::SES::Template`

- API: `SimpleEmailService.UpdateTemplate`
- Mutable: `Subject`, `HtmlPart`, `TextPart`
- Replacement: `TemplateName`

### P2.17 — `AWS::IAM::InstanceProfile`

- API: `AddRoleToInstanceProfile` / `RemoveRoleFromInstanceProfile`
- Mutable: `Roles` (single role per profile in practice — diff and swap)
- Replacement: `InstanceProfileName`, `Path`

## P3 — Low impact / niche ✅ (5 stubs)

### P3.1 — `AWS::Lambda::Permission`

Already a stub. AWS does not have an UpdatePermission API — every change
is a remove + add. Current behaviour (delete + create) is correct;
nothing to do.

### P3.2 — `AWS::SNS::Subscription`

Only `FilterPolicy` / `RawMessageDelivery` / `RedrivePolicy` /
`DeliveryPolicy` are mutable, via `SetSubscriptionAttributes`.
Worth adding once subscription churn shows up in dev workflows.

### P3.3 — `AWS::Events::EventBus`

Only `Policy` is mutable, via `PutPermission` / `RemovePermission`.

### P3.4 — `AWS::ElastiCache::SubnetGroup` / `AWS::RDS::DBSubnetGroup`

`Modify*SubnetGroup` exists; rarely changed in practice.

### P3.5 — `AWS::ECS::Cluster`

`UpdateClusterSettings` (containerInsights) is the only common change.

### P3.6 — `AWS::KMS::Alias`

`UpdateAlias` exists; aliases are rarely retargeted in stacks.

## Resources that are already correct (replacement is the AWS behaviour)

No `Update` method is needed for these — listing here so we don't
re-audit them later.

- `AWS::ECS::TaskDefinition` — every change is a new revision; AWS itself replaces.
- `AWS::Lambda::LayerVersion` — every publish is a new version.
- `AWS::Logs::LogStream` — only mutable property is the name (immutable).
- `AWS::IAM::ServiceLinkedRole` — created/deleted only.
- `AWS::ApiGateway::Resource` — `PathPart`/`ParentId` immutable.
- `AWS::ApiGateway::ApiKey`, `UsagePlanKey`, `Account` — mostly immutable.
- `AWS::SQS::QueuePolicy` — stub (policy applied via SQS attribute).
- `AWS::CDK::Metadata`, `AWS::CloudFormation::WaitCondition*` — stubs.
- EC2 resources — most properties are immutable; remaining mutable
  surface (tags, security group rules) is small and changed via
  separate sub-resources (`AWS::EC2::SecurityGroupIngress` etc.).

## Cross-cutting work

- ✅ **`resourceUpdater` extended with `oldProps`.** The interface now takes
  `oldProps map[string]any`. Threaded `nil` from `updateResource()` call site
  until old-properties storage is added to `StackResource`.
- 🟡 **Tag diffing helper.** `serviceutil.DiffTags(old, new)` returning
  add/remove sets — not yet implemented. Most handlers don't use it yet.
- 🟡 **Pass old properties to `Update`.** The signature now accepts oldProps,
  but they are `nil` at the call site. `StackResource` needs a `Properties`
  field to store resolved properties during creation.
- 🟡 **PATCH operation builder for ApiGateway.** All ApiGateway updates
  (P1.2–P1.6) return `errReplacementRequired` until this is built.
- 🟡 **Test coverage.** Each handler's Update needs integration tests.

## Suggested execution order

1. **P0** — protocol bugs first; both items are small and unblock CDK
   custom resources / nested stack workflows.
2. **Cross-cutting prep** — extend `resourceUpdater` to receive old
   props; add tag-diff helper; add ApiGateway patch-op builder.
3. **P1.1, P1.7, P1.8** — StateMachine, EventBridge Rule,
   EventSourceMapping. These are the three resources that change every
   single Lambda code edit in serverless dev loops.
4. **P1.2 – P1.6, P1.9 – P1.11** — ApiGateway tier + S3 BucketPolicy +
   IAM inline/managed policies. Big quality-of-life win for any
   serverless API stack.
5. **P2** — sweep the medium tier as user demand surfaces. RDS, Cognito,
   AppSync, ECS Service are the most commonly hit; CloudFront, Kinesis,
   ElastiCache lower.
6. **P3** — only if reported.
