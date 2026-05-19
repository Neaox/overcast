# compat/suites/cdk — AWS CDK v2 (TypeScript)

> **Status: implemented.** End-to-end CDK v2 deployment compatibility tests.

## What this suite covers

Deploys a real CDK app via the native `cdk` CLI and verifies the
created resources work end-to-end. The app is wrapped in a CDK Stage
and deploys a single stack to exercise the full CloudFormation lifecycle.

### Resources deployed

| Service | Resources |
|---|---|
| S3 | Bucket |
| SQS | Queue, DLQ, QueuePolicy |
| SNS | Topic + SQS Subscription |
| DynamoDB | Table with GSI + Stream |
| IAM | Role (Lambda), Role (Step Functions), ManagedPolicy |
| Lambda | Function + 2 EventSourceMappings (SQS, DynamoDB Stream) |
| CloudWatch Logs | LogGroup |
| KMS | Key + Alias |
| Secrets Manager | Secret |
| SSM | StringParameter |
| EC2/VPC | VPC (1 AZ, public subnet), SecurityGroup |
| API Gateway | REST API + Mock method on root |
| Step Functions | StateMachine (single Pass state) |
| EventBridge | EventBus + Rule targeting SQS queue |

### Lifecycle phases

| Phase | Tests |
|---|---|
| Bootstrap | `cdk bootstrap` |
| Synth | `cdk synth` |
| Deploy | `cdk deploy --require-approval never` |
| Verify (CREATE) | Stack status + 6 original resource verifications |
| Verify (extended) | 10 additional resource verifications (Logs, KMS, Secrets, SSM, IAM, EC2, API GW, EventBridge, Step Functions) |
| Update | Modify Lambda timeout (10s -> 15s), redeploy, verify UPDATE_COMPLETE |
| Destroy | `cdk destroy --force` + verify stack absent |

## Prerequisites

- Node.js 18+
- `npm install -g aws-cdk`
- Overcast running on `http://localhost:4566`

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `OVERCAST_ENDPOINT` | `http://localhost:4566` | Emulator endpoint |
| `AWS_ACCESS_KEY_ID` | `test` | Fake credentials |
| `AWS_SECRET_ACCESS_KEY` | `test` | Fake credentials |
| `AWS_DEFAULT_REGION` | `us-east-1` | AWS region |
| `CDK_COMPAT_LAMBDA_TIMEOUT` | `10` | Lambda timeout (used for update test) |

## Wire format

This suite emits NDJSON to stdout matching the format documented in
[compat/README.md](../../README.md#wire-format-ndjson). All 31 tests
run sequentially within a single `cdk-lifecycle` group.
