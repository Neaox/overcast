---
title: "Using AWS CDK with Overcast"
description: "Overcast supports cdk deploy and cdk destroy for stacks that use supported resource types. This page explains how to configure CDK to target Overcast and what to expect."
section: "Getting Started"
tags:
  - aws
  - cdk
  - docs
  - overcast
---

# Using AWS CDK with Overcast

Overcast supports `cdk deploy` and `cdk destroy` for stacks that use
[supported resource types](#supported-resource-types). This page explains how to
configure CDK to target Overcast and what to expect.

For local VPC workflows, see [Local VPCs for CDK](./cdk/local-vpc.md). That page
covers letting a local resources stack create the VPC, keeping local-specific
logic out of application stacks, and CDK context cache churn.

---

## Quick start

### 1. Start Overcast

```bash
docker run --rm -p 4566:4566 ghcr.io/overcast-sh/overcast:latest
```

### 2. Configure environment

CDK needs credentials and an endpoint override. The simplest approach:

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1
```

### 3. Bootstrap (first time only)

CDK bootstrap creates an S3 bucket, SSM parameters, and IAM roles. Overcast
supports all of these:

```bash
npx cdk bootstrap aws://000000000000/us-east-1
```

The account ID `000000000000` matches Overcast's default (`OVERCAST_ACCOUNT_ID`).
If you've configured a different account ID, use that instead.

### 4. Deploy

```bash
npx cdk deploy --all --require-approval never
```

`--require-approval never` skips the interactive changeset review since there
are no real resources or costs involved.

### 5. Destroy

```bash
npx cdk destroy --all --force
```

---

## How it works

CDK's deploy workflow is:

1. **`sts:GetCallerIdentity`** — determines account and region. Overcast
   returns the configured `OVERCAST_ACCOUNT_ID` and `OVERCAST_DEFAULT_REGION`.
2. **`sts:AssumeRole`** — assumes the CDK bootstrap roles. Overcast returns
   valid temporary credentials (no real authentication).
3. **S3 upload** — the synthesised CloudFormation template and assets are
   uploaded to the CDK bootstrap bucket.
4. **`CreateChangeSet`** / **`ExecuteChangeSet`** — CloudFormation provisions
   resources by dispatching to the emulated services internally, on a
   background goroutine that keeps running after the call returns.
5. **`DescribeStacks`** — CDK polls until the stack reaches `CREATE_COMPLETE`
   or `UPDATE_COMPLETE`. Overcast waits briefly (`OVERCAST_CFN_SYNC_WAIT_MS`,
   default 1000ms) so a fast stack is already terminal on the first poll, but
   this is a real poll, not a formality — a stack with more resources is
   still `*_IN_PROGRESS` when step 5 starts.

All of these operations are implemented.

---

## Supported resource types

<!--
  Derivation: the counts and tables in this section are transcribed from the
  resourceHandlers map in internal/services/cloudformation/provisioner.go —
  136 registered entries (127 real handlers + 9 stubResourceHandler entries),
  plus the dynamically resolved Custom::* / AWS::CloudFormation::CustomResource
  and AWS::CloudFormation::Stack (see resolveHandler in the same file).
  Re-derive with:  grep -c '"AWS::' on the map literal (stubs are the entries
  whose value is &stubResourceHandler{}). Keep this section in sync when the
  map changes.
-->

Overcast's CloudFormation provisioner supports **136 resource types** today:
127 fully provisioned, 9 recognised as stubs, plus custom resources and
nested stacks (resolved dynamically). Resources with real handlers are
provisioned through the emulated services — they create real state that you
can query via the AWS APIs.

### Real handlers (resources are fully provisioned)

| Service         | Resource Types                                                                                                                                                                                                                                                                    |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| S3              | `AWS::S3::Bucket`, `AWS::S3::BucketPolicy`                                                                                                                                                                                                                                        |
| SQS             | `AWS::SQS::Queue`                                                                                                                                                                                                                                                                 |
| SNS             | `AWS::SNS::Topic`, `AWS::SNS::Subscription`                                                                                                                                                                                                                                       |
| DynamoDB        | `AWS::DynamoDB::Table`, `AWS::DynamoDB::GlobalTable`                                                                                                                                                                                                                              |
| Lambda          | `AWS::Lambda::Function`, `AWS::Lambda::Alias`, `AWS::Lambda::Url`, `AWS::Lambda::EventSourceMapping`, `AWS::Lambda::Permission`, `AWS::Lambda::LayerVersion`, `AWS::Lambda::CodeSigningConfig`                                                                                    |
| IAM             | `AWS::IAM::Role`, `AWS::IAM::Policy`, `AWS::IAM::ManagedPolicy`, `AWS::IAM::InstanceProfile`, `AWS::IAM::ServiceLinkedRole`, `AWS::IAM::User`, `AWS::IAM::Group`, `AWS::IAM::AccessKey`                                                                                           |
| EC2 / VPC       | `AWS::EC2::VPC`, `AWS::EC2::Subnet`, `AWS::EC2::SecurityGroup`, `AWS::EC2::InternetGateway`, `AWS::EC2::VPNGateway`, `AWS::EC2::VPCGatewayAttachment`, `AWS::EC2::RouteTable`, `AWS::EC2::Route`, `AWS::EC2::SubnetRouteTableAssociation`, `AWS::EC2::NatGateway`, `AWS::EC2::EIP` |
| ECS             | `AWS::ECS::Cluster`, `AWS::ECS::TaskDefinition`, `AWS::ECS::Service`                                                                                                                                                                                                              |
| ECR             | `AWS::ECR::Repository`                                                                                                                                                                                                                                                            |
| API Gateway     | `AWS::ApiGateway::RestApi`, `AWS::ApiGateway::Resource`, `AWS::ApiGateway::Method`, `AWS::ApiGateway::Deployment`, `AWS::ApiGateway::Stage`, `AWS::ApiGateway::ApiKey`, `AWS::ApiGateway::UsagePlan`, `AWS::ApiGateway::UsagePlanKey`, `AWS::ApiGateway::Authorizer`, `AWS::ApiGateway::Model`, `AWS::ApiGateway::RequestValidator`                                             |
| API Gateway V2  | `AWS::ApiGatewayV2::Api`, `AWS::ApiGatewayV2::Stage`, `AWS::ApiGatewayV2::Integration`, `AWS::ApiGatewayV2::Route`                                                                                                                                                                |
| AppSync         | `AWS::AppSync::Api`, `AWS::AppSync::GraphQLApi`, `AWS::AppSync::GraphQLSchema`, `AWS::AppSync::ChannelNamespace`, `AWS::AppSync::ApiKey`, `AWS::AppSync::DataSource`, `AWS::AppSync::Resolver`, `AWS::AppSync::FunctionConfiguration`, `AWS::AppSync::DomainName`, `AWS::AppSync::DomainNameApiAssociation`, `AWS::AppSync::ApiCache`, `AWS::AppSync::SourceApiAssociation` |
| AppConfig       | `AWS::AppConfig::Application`, `AWS::AppConfig::Environment`, `AWS::AppConfig::ConfigurationProfile`                                                                                                                                                                              |
| RDS             | `AWS::RDS::DBInstance`, `AWS::RDS::DBCluster`, `AWS::RDS::DBSubnetGroup`, `AWS::RDS::DBParameterGroup`                                                                                                                                                                            |
| ElastiCache     | `AWS::ElastiCache::CacheCluster`, `AWS::ElastiCache::ServerlessCache`, `AWS::ElastiCache::ReplicationGroup`, `AWS::ElastiCache::SubnetGroup`                                                                                                                                      |
| EFS             | `AWS::EFS::FileSystem`, `AWS::EFS::MountTarget`, `AWS::EFS::AccessPoint`                                                                                                                                                                                                          |
| EKS             | `AWS::EKS::Cluster`, `AWS::EKS::Nodegroup`, `AWS::EKS::FargateProfile`, `AWS::EKS::Addon`, `AWS::EKS::AccessEntry`, `AWS::EKS::PodIdentityAssociation`                                                                                                                            |
| MSK             | `AWS::MSK::Cluster`, `AWS::MSK::Configuration`                                                                                                                                                                                                                                    |
| EventBridge     | `AWS::Events::EventBus`, `AWS::Events::Rule`                                                                                                                                                                                                                                      |
| Scheduler       | `AWS::Scheduler::Schedule`, `AWS::Scheduler::ScheduleGroup`                                                                                                                                                                                                                       |
| Pipes           | `AWS::Pipes::Pipe`                                                                                                                                                                                                                                                                |
| Step Functions  | `AWS::StepFunctions::StateMachine`                                                                                                                                                                                                                                                |
| Kinesis         | `AWS::Kinesis::Stream`                                                                                                                                                                                                                                                            |
| Firehose        | `AWS::KinesisFirehose::DeliveryStream`                                                                                                                                                                                                                                            |
| CloudWatch      | `AWS::CloudWatch::Alarm`                                                                                                                                                                                                                                                          |
| CloudWatch Logs | `AWS::Logs::LogGroup`, `AWS::Logs::LogStream`                                                                                                                                                                                                                                     |
| KMS             | `AWS::KMS::Key`, `AWS::KMS::Alias`                                                                                                                                                                                                                                                |
| SSM             | `AWS::SSM::Parameter`                                                                                                                                                                                                                                                             |
| Secrets Manager | `AWS::SecretsManager::Secret`                                                                                                                                                                                                                                                     |
| Cognito         | `AWS::Cognito::UserPool`, `AWS::Cognito::UserPoolClient`                                                                                                                                                                                                                          |
| Route 53        | `AWS::Route53::HostedZone`, `AWS::Route53::RecordSet`, `AWS::Route53::HealthCheck`                                                                                                                                                                                                |
| CloudFront      | `AWS::CloudFront::Distribution`                                                                                                                                                                                                                                                   |
| ELBv2           | `AWS::ElasticLoadBalancingV2::LoadBalancer`, `AWS::ElasticLoadBalancingV2::TargetGroup`, `AWS::ElasticLoadBalancingV2::Listener`                                                                                                                                                  |
| Auto Scaling    | `AWS::AutoScaling::AutoScalingGroup`, `AWS::AutoScaling::LaunchConfiguration`                                                                                                                                                                                                     |
| SES             | `AWS::SES::Template`                                                                                                                                                                                                                                                              |
| ACM             | `AWS::CertificateManager::Certificate`                                                                                                                                                                                                                                            |
| CloudTrail      | `AWS::CloudTrail::Trail`                                                                                                                                                                                                                                                          |
| Backup          | `AWS::Backup::BackupVault`, `AWS::Backup::BackupPlan`                                                                                                                                                                                                                             |
| Transfer Family | `AWS::Transfer::Server`, `AWS::Transfer::User`                                                                                                                                                                                                                                    |
| Glue            | `AWS::Glue::Database`, `AWS::Glue::Table`                                                                                                                                                                                                                                         |
| Athena          | `AWS::Athena::WorkGroup`                                                                                                                                                                                                                                                          |
| OpenSearch      | `AWS::OpenSearchService::Domain`                                                                                                                                                                                                                                                  |
| Shield          | `AWS::Shield::Protection`                                                                                                                                                                                                                                                         |
| WAF v2          | `AWS::WAFv2::WebACL`                                                                                                                                                                                                                                                              |
| AppRegistry     | `AWS::ServiceCatalogAppRegistry::Application`, `AWS::ServiceCatalogAppRegistry::ResourceAssociation`                                                                                                                                                                              |
| CloudFormation  | `AWS::CloudFormation::Stack` (nested stacks), `AWS::CloudFormation::CustomResource`, `Custom::*` (resolved dynamically, in addition to the 136 static handlers)                                                                                                                    |

### Stubs (succeed silently, no real state)

These resource types are recognised and return a synthetic physical ID so the
stack can complete, but no real resources are created:

- `AWS::SQS::QueuePolicy`
- `AWS::ApiGateway::Account`
- `AWS::ApiGatewayV2::Deployment`
- `AWS::ElastiCache::ParameterGroup`
- `AWS::SES::ConfigurationSet`
- `AWS::Events::Connection`
- `AWS::CDK::Metadata`
- `AWS::CloudFormation::WaitConditionHandle`
- `AWS::CloudFormation::WaitCondition`

### Unknown resource types

Resource types not in either list above are handled permissively — they receive
a synthetic physical ID (`<stackName>-<logicalId>-stub`) and succeed. This means
a template with unsupported types will deploy, but those resources won't have
real backing state.

---

## `Fn::GetAtt` support

CloudFormation `Fn::GetAtt` references resolve to real attribute values for
provisioned resources. For example, `!GetAtt MyVPC.VpcId` returns the actual
VPC ID created by the EC2 service. See
[cloudformation.md](./services/cloudformation.md) for the full list of supported
attributes per resource type.

---

## Limitations

### Custom resource invocation requires Docker

`AWS::CloudFormation::CustomResource` and `Custom::*` types invoke the Lambda
function specified by `ServiceToken`. When Docker is available, the Lambda
executes and the response (`PhysicalResourceId`, `Data`) is used as the
resource's physical ID and attributes. When Docker is unavailable, the handler
degrades gracefully to a stub physical ID so the stack can still deploy.

### Container assets are served from Overcast's own registry

A `DockerImageAsset` — an ECS `ContainerImage.fromAsset`, a Lambda
`DockerImageFunction`, or any construct that builds an image — is published to
the ECR repository `cdk bootstrap` created, which is a `registry:2` container
Overcast starts and authenticates. CDK then writes the image into the template
as `{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}`, built from
`AWS::AccountId` and `AWS::Region` rather than read back from the repository.
Overcast recognises that address as its own and pulls from the registry it
serves, so the task or function runs the image the deploy published. See
[ECR § Running an image from here](./services/ecr.md#running-an-image-from-here).

Before building anything, cdk-assets asks ECR whether the asset's tag is
already published and skips the push if it is, so that answer has to be right
or the deploy publishes nothing and fails at pull time instead. Overcast
answers it from the registry rather than from memory of an earlier run — see
[ECR § Asking whether an image is published](./services/ecr.md#asking-whether-an-image-is-published).
The registry's storage is a named Docker volume, so a restarted Overcast still
has the assets the last deploy pushed and the next one skips rebuilding them —
see [ECR § Persistence](./services/ecr.md#persistence). Two cases still re-push:
a registry that fell back to an ephemeral port, and one started with
`OVERCAST_ECR_REGISTRY_PERSIST=false`. That is a rebuild of a few seconds, not a
failure.

The registry publishes on a fixed port (`4510` by default, see
[ECR § Repository URI](./services/ecr.md#repository-uri)) reachable at
`localhost` from the Docker daemon's own vantage — which is the vantage that
matters, because `docker push` and every image pull are performed by the
daemon, not by the client that asked. `repositoryUri` names `localhost` even
when `OVERCAST_HOSTNAME` is set to something else, because that is the address
startup proved the daemon can reach — and because Docker trusts plain HTTP to
`localhost` and bypasses proxies for it, neither of which it does for an
ordinary domain that merely resolves to loopback. This works out of the box on
native Linux and on Docker Desktop; only a remote daemon needs an
`insecure-registries` entry, and Overcast verifies the path at registry startup
and logs the remediation if it is broken.

### Nested stack TemplateURL must be reachable

`AWS::CloudFormation::Stack` (nested stacks) is supported. The `TemplateURL`
must point to an S3 object or any URL reachable by the emulator. The child
template is fetched, parsed, and provisioned synchronously within the parent
stack's provisioning goroutine. Child outputs are exposed via
`Fn::GetAtt ["NestedStack", "Outputs.OutputKey"]`.

### Partial resource coverage

Not every CDK construct maps to a supported resource type. If your stack uses
resource types not listed above, those resources will be silently stubbed. Check
the Overcast logs (`OVERCAST_LOG_LEVEL=debug`) to see which resources were
stubbed during deployment.

### No drift detection or stack policies

`DetectStackDrift`, `SetStackPolicy`, and `GetStackPolicy` return `501`.

---

## Troubleshooting

### `cdk bootstrap` fails

Ensure Overcast is running and `AWS_ENDPOINT_URL` is set. Bootstrap needs S3,
SSM, IAM, and STS — all are supported.

### Stack stuck in `CREATE_IN_PROGRESS`

Overcast provisions resources asynchronously in a background goroutine, so
`CREATE_IN_PROGRESS` on its own is expected — it clears once the goroutine
finishes. If it never clears, a resource handler is likely hung or failing;
check the server logs. A stack that does fail transitions to
`ROLLBACK_COMPLETE`.

### `Fn::GetAtt` returns unexpected values

Only the attributes listed in [cloudformation.md](./services/cloudformation.md)
are supported. Unsupported attributes fall back to the resource's physical ID.

### `--hotswap` deployments

CDK hotswap bypasses CloudFormation and calls service APIs directly (e.g.
`UpdateFunctionCode` for Lambda). This works against Overcast as long as the
underlying service operation is implemented.

### S3 asset upload fails on Windows

**Symptom:** `cdk deploy` fails on Windows with an S3 connection or DNS
resolution error after a successful bootstrap. The error originates in the CDK
asset publisher (Node.js), not in the CloudFormation create/update step.

**Root cause:** CDK's asset publisher sends S3 requests using virtual-hosted
style, constructing a bucket hostname from your endpoint URL:

```
cdk-hnb659fds-assets-<account>-<region>.localhost
```

On Windows, `*.localhost` subdomains do **not** resolve by default — only
`localhost` itself is in the hosts file. On Linux and macOS the system resolver
handles `*.localhost` automatically, so this issue does not affect those
platforms.

**Fix:** Use a wildcard-DNS hostname instead of `localhost`. Overcast treats
the `OVERCAST_HOSTNAME` environment variable as an additional virtual-host base,
so any `<bucket>.<hostname>` request is correctly rewritten to path-style.

`localhost.overcast.sh` is a public domain whose DNS unconditionally resolves
all `*.localhost.overcast.sh` subdomains to `127.0.0.1` (your local machine).
No hosts-file edits required, and it behaves identically on every OS:

```bash
# Start Overcast with the wildcard-DNS hostname
docker run --rm -p 4566:4566 \
  -e OVERCAST_HOSTNAME=localhost.overcast.sh \
  ghcr.io/overcast-sh/overcast:latest

# Point CDK at that hostname
export AWS_ENDPOINT_URL=http://localhost.overcast.sh:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

npx cdk bootstrap aws://000000000000/us-east-1
npx cdk deploy --require-approval never
```

With this configuration, CDK constructs a bucket hostname like
`cdk-hnb659fds-assets-000000000000-us-east-1.localhost.overcast.sh:4566`,
which resolves via public DNS to `127.0.0.1` and is rewritten by Overcast's
S3 virtual-host middleware to the correct path-style route.

> **Note:** This fix also works on Linux and macOS, so
> `OVERCAST_HOSTNAME=localhost.overcast.sh` is safe to use in a shared CI/CD
> environment where developers are on different host operating systems.
>
> `localhost.localstack.cloud` and `localhost.floci.io` are recognised too and
> behave identically, so a setup carried over from either tool keeps working
> unchanged. Neither sends any traffic to those projects — the domains are
> purely a DNS convenience and every request goes to Overcast on your machine.
>
> All three need a public DNS lookup, so none of them works offline or behind
> DNS rebinding protection. See the caveat in
> [networking.md](./networking.md) for the fallbacks.

---

## Example: deploy a Lambda + API Gateway stack

```typescript
import * as cdk from "aws-cdk-lib";
import * as lambda from "aws-cdk-lib/aws-lambda";
import * as apigw from "aws-cdk-lib/aws-apigateway";

const app = new cdk.App();
const stack = new cdk.Stack(app, "MyStack");

const fn = new lambda.Function(stack, "Handler", {
  runtime: lambda.Runtime.NODEJS_20_X,
  handler: "index.handler",
  code: lambda.Code.fromInline(`
    exports.handler = async () => ({
      statusCode: 200,
      body: JSON.stringify({ message: 'Hello from Overcast!' }),
    });
  `),
});

new apigw.LambdaRestApi(stack, "Api", { handler: fn });
```

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
npx cdk deploy --require-approval never
```
