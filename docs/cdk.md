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
covers CDK context cache churn, `Vpc.fromLookup` vs `Vpc.fromVpcAttributes`, and
the VPC provider pattern for keeping local-specific logic out of stacks.

---

## Quick start

### 1. Start Overcast

```bash
docker run --rm -p 4566:4566 ghcr.io/neaox/overcast:latest
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
   resources by dispatching to the emulated services internally.
5. **`DescribeStacks`** — CDK polls until the stack reaches `CREATE_COMPLETE`
   or `UPDATE_COMPLETE`.

All of these operations are implemented.

---

## Supported resource types

Overcast's CloudFormation provisioner has handlers for ~50 resource types.
Resources with real handlers are provisioned through the emulated services —
they create real state that you can query via the AWS APIs.

### Real handlers (resources are fully provisioned)

| Service         | Resource Types                                                                                                                                                                                                                                             |
| --------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --- | -------------- | ------------------------------------------------------------------------------------------------ |
| S3              | `AWS::S3::Bucket`, `AWS::S3::BucketPolicy`                                                                                                                                                                                                                 |
| SQS             | `AWS::SQS::Queue`                                                                                                                                                                                                                                          |
| SNS             | `AWS::SNS::Topic`, `AWS::SNS::Subscription`                                                                                                                                                                                                                |
| DynamoDB        | `AWS::DynamoDB::Table`                                                                                                                                                                                                                                     |
| Lambda          | `AWS::Lambda::Function`, `AWS::Lambda::EventSourceMapping`, `AWS::Lambda::LayerVersion`                                                                                                                                                                    |
| IAM             | `AWS::IAM::Role`, `AWS::IAM::Policy`, `AWS::IAM::ManagedPolicy`, `AWS::IAM::InstanceProfile`, `AWS::IAM::ServiceLinkedRole`                                                                                                                                |
| EC2 / VPC       | `AWS::EC2::VPC`, `AWS::EC2::Subnet`, `AWS::EC2::SecurityGroup`, `AWS::EC2::InternetGateway`, `AWS::EC2::VPCGatewayAttachment`, `AWS::EC2::RouteTable`, `AWS::EC2::Route`, `AWS::EC2::SubnetRouteTableAssociation`, `AWS::EC2::NatGateway`, `AWS::EC2::EIP` |
| ECS             | `AWS::ECS::Cluster`, `AWS::ECS::TaskDefinition`, `AWS::ECS::Service`                                                                                                                                                                                       |
| API Gateway     | `AWS::ApiGateway::RestApi`, `AWS::ApiGateway::Resource`, `AWS::ApiGateway::Method`, `AWS::ApiGateway::Deployment`, `AWS::ApiGateway::Stage`                                                                                                                |
| API Gateway V2  | `AWS::ApiGatewayV2::Api`, `AWS::ApiGatewayV2::Stage`, `AWS::ApiGatewayV2::Integration`, `AWS::ApiGatewayV2::Route`                                                                                                                                         |
| EventBridge     | `AWS::Events::EventBus`, `AWS::Events::Rule`                                                                                                                                                                                                               |
| KMS             | `AWS::KMS::Key`, `AWS::KMS::Alias`                                                                                                                                                                                                                         |
| Step Functions  | `AWS::StepFunctions::StateMachine`                                                                                                                                                                                                                         |
| CloudWatch Logs | `AWS::Logs::LogGroup`, `AWS::Logs::LogStream`                                                                                                                                                                                                              |
| SSM             | `AWS::SSM::Parameter`                                                                                                                                                                                                                                      |
| Secrets Manager | `AWS::SecretsManager::Secret`                                                                                                                                                                                                                              |     | CloudFormation | `AWS::CloudFormation::Stack` (nested stacks), `AWS::CloudFormation::CustomResource`, `Custom::*` |

### Stubs (succeed silently, no real state)

These resource types are recognised and return a synthetic physical ID so the
stack can complete, but no real resources are created:

- `AWS::SQS::QueuePolicy`
- `AWS::Lambda::Permission`
- `AWS::CDK::Metadata`
- `AWS::CloudFormation::WaitConditionHandle`
- `AWS::CloudFormation::WaitCondition`
- `AWS::ApiGateway::Account`

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

Overcast provisions resources synchronously in a background goroutine. If a
resource handler fails, the stack transitions to `ROLLBACK_COMPLETE`. Check the
server logs for errors.

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

`localhost.localstack.cloud` is a public domain maintained by LocalStack whose
DNS unconditionally resolves all `*.localhost.localstack.cloud` subdomains to
`127.0.0.1` (your local machine). Using it here does **not** involve LocalStack in any way — the
domain is purely a convenience DNS service. All traffic goes directly to
Overcast on your machine; nothing is sent to LocalStack's servers. No
hosts-file edits required:

```bash
# Start Overcast with the wildcard-DNS hostname
docker run --rm -p 4566:4566 \
  -e OVERCAST_HOSTNAME=localhost.localstack.cloud \
  ghcr.io/neaox/overcast:latest

# Point CDK at that hostname
export AWS_ENDPOINT_URL=http://localhost.localstack.cloud:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

npx cdk bootstrap aws://000000000000/us-east-1
npx cdk deploy --require-approval never
```

With this configuration, CDK constructs a bucket hostname like
`cdk-hnb659fds-assets-000000000000-us-east-1.localhost.localstack.cloud:4566`,
which resolves via public DNS to `127.0.0.1` and is rewritten by Overcast's
S3 virtual-host middleware to the correct path-style route.

> **Note:** This fix also works on Linux and macOS, so
> `OVERCAST_HOSTNAME=localhost.localstack.cloud` is safe to use in a shared
> CI/CD environment where developers use different host operating systems.

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
