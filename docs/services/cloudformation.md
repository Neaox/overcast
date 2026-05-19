# CloudFormation — endpoint support

> AWS docs: [CloudFormation API Reference](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/Welcome.html)

CloudFormation uses the AWS Query protocol (`POST /` with form-encoded body). Overcast
implements stack lifecycle, change sets, and resource provisioning with an async provisioner
that dispatches internal HTTP requests through the emulator to create/delete resources.

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

## Supported resource types

The provisioner dispatches internal HTTP requests to the emulated services. Resources
listed as **Provisioned** create real state in the target service; **Stub** resources
generate a placeholder ID without side effects.

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
- **Async provisioner.** Stack create/update/delete happens asynchronously in background goroutines. Resources are created via internal HTTP requests through the emulator's router, enabling CloudFormation to orchestrate any implemented service.
- **DependsOn.** Resource dependency ordering is supported via topological sort.
- **Status state machine.** Stacks follow the full AWS status lifecycle: `CREATE_IN_PROGRESS` → `CREATE_COMPLETE` / `CREATE_FAILED`, etc.
- **Fn::GetAtt.** Returns real attribute values from service responses (e.g. `!GetAtt MyVpc.CidrBlock` returns `10.0.0.0/16`). Falls back to the physical resource ID when a specific attribute is not captured.
- **Cross-stack references.** `Fn::ImportValue` resolves exports from other active stacks in the same region. `ListExports` and `ListImports` return the export index.
- **Custom resources.** `Custom::*` and `AWS::CloudFormation::CustomResource` types invoke the Lambda function specified by `ServiceToken`. The handler sends a CloudFormation custom resource request to the Lambda and parses the response (`PhysicalResourceId`, `Data`). When Docker is unavailable (Lambda cannot execute), the handler degrades gracefully to a stub physical ID so the stack can still deploy.
- **Nested stacks.** `AWS::CloudFormation::Stack` is supported. The provisioner fetches the child template from the `TemplateURL` (typically an S3 object), creates a child stack, and provisions its resources synchronously within the parent's goroutine. Child stack outputs are exposed via `Fn::GetAtt` as `Outputs.<key>`. Deletion of nested stacks cascades — deleting the parent deletes all child resources.
- **Stack updates and drift.** `UpdateStack` (and `ExecuteChangeSet`) detect property drift per-resource via a sha256 hash of the resolved property map stored alongside each `StackResource`. Resources whose hash is unchanged are skipped. When a resource's properties change, the provisioner picks one of three strategies, in order:
  1. **In-place update** — when the resource handler implements an `Update` method, mutable properties are applied via the service's mutation API. Implemented for:
     - `AWS::Lambda::Function` — `UpdateFunctionCode` + `UpdateFunctionConfiguration`
     - `AWS::SQS::Queue` — `SetQueueAttributes`
     - `AWS::SNS::Topic` — `SetTopicAttributes` (DisplayName, KmsMasterKeyId)
     - `AWS::DynamoDB::Table` — `UpdateTable` (BillingMode, ProvisionedThroughput, AttributeDefinitions, StreamSpecification) + `UpdateTimeToLive`
     - `AWS::SecretsManager::Secret` — `UpdateSecret` (Description, SecretString, KmsKeyId)
     - `AWS::SSM::Parameter` — `PutParameter` with `Overwrite=true`
     - `AWS::Logs::LogGroup` — `PutRetentionPolicy` / `DeleteRetentionPolicy`
     - `AWS::IAM::Role` — `UpdateAssumeRolePolicy` + `UpdateRole` (Description)
     - `AWS::S3::Bucket` — accepts mutable sub-resource properties (Tags, CORS, Versioning, Policy, etc.) without rebuilding the bucket. Wiring of those sub-resource PUT calls from CFN is incremental; for now the data is preserved and the user can apply the change directly via the S3 API if needed.
  2. **Replacement (delete + create)** — when an Update method signals replacement (immutable property changed) or no `Update` method is registered for the resource type. Immutable identity properties trigger this path:
     - `BucketName` (S3), `TableName` / `KeySchema` (DynamoDB), `QueueName` / `FifoQueue` (SQS), `TopicName` (SNS), `Name` (SSM Parameter, SecretsManager Secret), `LogGroupName` (Logs), `RoleName` (IAM Role), `FunctionName` (Lambda).
  3. **Retain on replace** — `UpdateReplacePolicy: Retain` (or `Snapshot`) skips deletion of the old resource so the new one is created and the old one is orphaned, no longer tracked by the stack. This matches AWS CloudFormation behaviour.
- **DeletionPolicy.** Honoured. `DeletionPolicy: Retain` skips deletion when the stack is deleted or a resource is removed from the template on update — the resource is orphaned and a `DELETE_SKIPPED` event is emitted. `Snapshot` is treated the same as `Retain` (snapshots are not actually taken).
- **Legacy state compatibility.** Stacks created before property-hash tracking was introduced have an empty hash; on the first update, missing-hash resources are treated as unchanged so re-running an unchanged template (for example `cdk bootstrap`) is a no-op rather than a destructive replace.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category             | ✅ Supported | ❌ Unsupported |
| -------------------- | ------------ | -------------- |
| Stacks               | 5            | 6              |
| Change sets          | 5            |                |
| Resources and events | 3            |                |
| Templates            | 3            | 1              |
| Exports              | 2            |                |
| Intrinsic functions  | 1            |                |
| Resource types       |              | 1              |
| StackSets            |              | 13             |
| Type registry        |              | 7              |

---

## Endpoints

### Stacks

| Operation                | Status         | Notes                                                  | AWS Docs                                                                                                  |
| ------------------------ | -------------- | ------------------------------------------------------ | --------------------------------------------------------------------------------------------------------- |
| `CreateStack`            | ✅ Supported   | Async provisioner; JSON templates; intrinsic functions | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CreateStack.html)            |
| `UpdateStack`            | ✅ Supported   | Re-provisions with updated template                    | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_UpdateStack.html)            |
| `DeleteStack`            | ✅ Supported   | Async resource cleanup in reverse dependency order     | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DeleteStack.html)            |
| `DescribeStacks`         | ✅ Supported   | Status, parameters, outputs, tags                      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStacks.html)         |
| `ListStacks`             | ✅ Supported   | Filter by status                                       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStacks.html)             |
| `ContinueUpdateRollback` | ❌ Unsupported | stub; returns 501                                      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ContinueUpdateRollback.html) |
| `CancelUpdateStack`      | ❌ Unsupported | stub; returns 501                                      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CancelUpdateStack.html)      |
| `SignalResource`         | ❌ Unsupported | stub; returns 501                                      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_SignalResource.html)         |
| `GetStackPolicy`         | ❌ Unsupported | stub; returns 501                                      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_GetStackPolicy.html)         |
| `SetStackPolicy`         | ❌ Unsupported | stub; returns 501                                      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_SetStackPolicy.html)         |
| `DescribeAccountLimits`  | ❌ Unsupported | stub; returns 501                                      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeAccountLimits.html)  |

### Change sets

| Operation           | Status       | Notes                                      | AWS Docs                                                                                             |
| ------------------- | ------------ | ------------------------------------------ | ---------------------------------------------------------------------------------------------------- |
| `CreateChangeSet`   | ✅ Supported | Creates a change set from a template       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_CreateChangeSet.html)   |
| `DescribeChangeSet` | ✅ Supported | Returns change set details and status      | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeChangeSet.html) |
| `ExecuteChangeSet`  | ✅ Supported | Provisions resources via async provisioner | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ExecuteChangeSet.html)  |
| `DeleteChangeSet`   | ✅ Supported |                                            | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DeleteChangeSet.html)   |
| `ListChangeSets`    | ✅ Supported |                                            | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListChangeSets.html)    |

### Resources and events

| Operation                | Status       | Notes                             | AWS Docs                                                                                                  |
| ------------------------ | ------------ | --------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `DescribeStackResources` | ✅ Supported | Lists resources for a stack       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStackResources.html) |
| `ListStackResources`     | ✅ Supported | Lists resources with status       | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStackResources.html)     |
| `DescribeStackEvents`    | ✅ Supported | Returns stack provisioning events | [docs](https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_DescribeStackEvents.html)    |

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
