---
title: "Service Reference"
description: "Index of every AWS service Overcast emulates, grouped by what you are building, with the coverage tiers and status tokens the service pages use."
section: "Services"
tags:
  - coverage
  - docs
  - reference
  - services
---

# Service Reference

One page per AWS service: a quick start, what works, and how it differs from
AWS. Point any SDK or CLI at `http://localhost:4566` and pick your service
below.

```bash
export AWS_ENDPOINT_URL=http://localhost:4566
aws sqs create-queue --queue-name orders
```

## Where to start

| If you're… | Start with |
| --- | --- |
| Deploying a CDK or CloudFormation app | [CloudFormation](./cloudformation.md), then [Using AWS CDK](../cdk.md) |
| Building a serverless API | [Lambda](./lambda.md), [API Gateway](./apigateway.md), [DynamoDB](./dynamodb.md) |
| Wiring messaging and events | [SQS](./sqs.md), [SNS](./sns.md), [EventBridge](./eventbridge.md) |
| Running containers | [ECS](./ecs.md), [ECR](./ecr.md), [ELBv2](./elb.md) |
| Storing objects or data | [S3](./s3.md), [DynamoDB](./dynamodb.md), [RDS](./rds.md) |

## Services

**Compute and containers** — [Lambda](./lambda.md) · [ECS](./ecs.md) ·
[ECR](./ecr.md) · [EC2 / VPC](./ec2.md) · [Auto Scaling](./autoscaling.md) ·
[EKS](./eks.md)

**Storage and data** — [S3](./s3.md) · [DynamoDB](./dynamodb.md) ·
[DynamoDB Streams](./dynamodbstreams.md) · [RDS](./rds.md) ·
[ElastiCache](./elasticache.md) · [EFS](./efs.md) · [Kinesis](./kinesis.md) ·
[Firehose](./firehose.md) · [MSK](./msk.md) · [Athena](./athena.md) ·
[Glue](./glue.md) · [OpenSearch](./opensearch.md) · [Backup](./backup.md) ·
[Transfer Family](./transfer.md)

**Messaging and events** — [SQS](./sqs.md) · [SNS](./sns.md) ·
[EventBridge](./eventbridge.md) · [Scheduler](./scheduler.md) ·
[Pipes](./pipes.md) · [Step Functions](./stepfunctions.md) · [SES](./ses.md)

**APIs and delivery** — [API Gateway](./apigateway.md) ·
[AppSync](./appsync.md) · [CloudFront](./cloudfront.md) · [ELBv2](./elb.md) ·
[Route 53](./route53.md) · [ACM](./acm.md)

**Identity and security** — [IAM](./iam.md) · [STS](./sts.md) ·
[Cognito](./cognito.md) · [KMS](./kms.md) ·
[Secrets Manager](./secretsmanager.md) · [SSM](./ssm.md) · [WAF v2](./waf.md) ·
[Shield](./shield.md) · [Organizations](./organizations.md)

**Observability** — [CloudWatch](./cloudwatch.md) ·
[CloudWatch Logs](./cloudwatch-logs.md) · [CloudTrail](./cloudtrail.md)

**Provisioning and config** — [CloudFormation](./cloudformation.md) ·
[AppConfig](./appconfig.md) · [AppConfigData](./appconfigdata.md) ·
[AppRegistry](./appregistry.md) · [Bedrock](./bedrock.md)

## Coverage tiers

Each service has an overall tier, and it decides the **Status:** token at the top
of that service's page. The full generated table, with operation counts and
tiers, is on [Reference index § Services](../README.md#services).

| Tier | Meaning | Page status |
| --- | --- | --- |
| Comprehensive | Real SDK clients use it end to end. | ✅ Supported |
| Core CRUD | Resources are created, stored and returned; common workflows work. | ⚠️ Partial |
| Minimal | A targeted subset — enough for one or two workflows. | ⚠️ Partial |
| Stub | Registered so discovery and IaC work; most operations return `501`. | ⚠️ Partial |

Each operation carries its own status token, and the service pages use the same
five words for anything they qualify:

| Status | Meaning |
| --- | --- |
| ✅ Supported | Works for normal SDK and CLI use. |
| ⚠️ Partial | Works with documented caveats or missing edge cases. |
| 🧊 Inert | Accepted and answered correctly, but nothing happens as a result. |
| 🚧 WIP | Present, still moving. |
| ❌ Unsupported | Modelled but not implemented. |

An unsupported operation answers with an AWS-shaped error rather than hanging or
failing to connect, so an SDK raises something you can read:

```
HTTP 501 Not Implemented
x-emulator-unsupported: true

{
  "__type": "NotImplemented",
  "message": "This operation is not yet emulated. Check docs/services/ for the support matrix."
}
```

A running service also reports a **runtime emulation tier** — full, partial,
inert or stub — on `/_overcast/health`, in `overcast services` and in the web
console. That is a different axis, defined in
[Reference index § Runtime emulation tiers](../README.md#runtime-emulation-tiers).

## Related

- [Using AWS SDKs and CLI](../sdk-cli.md) — endpoint configuration per language
- [Configuration reference](../configuration.md) — environment variables and service names
- [Networking and host-based addressing](../networking.md) — path-style vs. host-routed endpoints
