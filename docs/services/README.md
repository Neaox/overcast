---
title: "Service Reference"
description: "Start here for Overcast service coverage, support tiers, and links to per-service endpoint compatibility tables."
section: "Services"
tags:
  - coverage
  - docs
  - reference
  - services
---

# Service Reference

Overcast implements the most common local-development workflows across AWS service APIs. Each service page lists supported, partial, work-in-progress, and unsupported operations with notes about emulator-specific limitations.

## Start Here

- [S3](./s3.md), [SQS](./sqs.md), [DynamoDB](./dynamodb.md), [Lambda](./lambda.md), [API Gateway](./apigateway.md), and [EC2 / VPC](./ec2.md) have the broadest workflow coverage.
- [CloudFormation](./cloudformation.md) is the main entry point for CDK and IaC-driven deployments.
- [IAM](./iam.md) stores roles, policies, users, groups, and instance profiles, but is intentionally not a security boundary.
- Unsupported operations return AWS-shaped `501 Not Implemented` responses with `x-emulator-unsupported: true`.

## Common Workflows

| Workflow | Relevant Docs |
| -------- | ------------- |
| CDK deployments | [CloudFormation](./cloudformation.md), [EC2 / VPC](./ec2.md), [IAM](./iam.md), [Lambda](./lambda.md), [ECS](./ecs.md) |
| Messaging and events | [SQS](./sqs.md), [SNS](./sns.md), [EventBridge](./eventbridge.md), [Scheduler](./scheduler.md), [Pipes](./pipes.md) |
| Serverless APIs | [Lambda](./lambda.md), [API Gateway](./apigateway.md), [DynamoDB](./dynamodb.md), [CloudWatch Logs](./cloudwatch-logs.md) |
| Containers and registries | [ECS](./ecs.md), [ECR](./ecr.md), [EC2 / VPC](./ec2.md), [CloudWatch Logs](./cloudwatch-logs.md) |
| Storage and data | [S3](./s3.md), [DynamoDB](./dynamodb.md), [RDS](./rds.md), [ElastiCache](./elasticache.md), [Kinesis](./kinesis.md) |

## Support Levels

| Status | Meaning |
| ------ | ------- |
| Supported | Implemented for normal SDK/CLI use. |
| Partial | Implemented with documented caveats or missing edge cases. |
| WIP | Present but still under active development. |
| Unsupported | Not implemented; returns a modeled unsupported response. |

For the full generated service table, see [Documentation § Services](../README.md#services).
