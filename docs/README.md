---
title: "Documentation"
description: "This directory contains the full Overcast documentation. For a quick overview, see the root README."
section: "Getting Started"
tags:
  - docs
  - documentation
---

# Documentation

This directory contains the full Overcast documentation. For a quick overview,
see the [root README](../README.md).

## Contents

### Getting started

- [Using AWS SDKs and CLI](./sdk-cli.md) — configure the AWS CLI (`--endpoint-url`), Node.js, Python, Go, Java, .NET, Rust, Terraform
- [CLI reference](./cli.md) — every `overcast` subcommand: background instances, introspection, AWS environment helpers, networking and TLS
- [Using AWS CDK](./cdk.md) — `cdk bootstrap`, `cdk deploy`, supported resource types, troubleshooting
- [CDK guides](./cdk/) — focused CDK workflow guides
- [Local VPCs for CDK](./cdk/local-vpc.md) — local resources stack that creates the VPC, environment-agnostic application stacks, CDK context cache behavior
- [Networking and host-based addressing](./networking.md) — path-style vs. Host-routed endpoints (API Gateway, Lambda function URLs, AppSync), wildcard DNS setup
- [Multi-container networking](./multi-container-networking.md) — `OVERCAST_HOSTNAME` for Docker Compose sibling containers
- [The inner loop](./local-dev.md) — edit a file and see it take effect: `cdk watch`, Lambda and ECS hot reload, and a Laravel-on-Fargate walkthrough
- [Testcontainers](./testcontainers.md) — start Overcast from integration tests with the Go module; options, port-mapping caveats
- [Migrating from LocalStack](./migration-from-localstack.md) — drop-in replacement guide

### Reference

- [Service emulation reference](./services/README.md) — per-service endpoint coverage tables
- [Configuration reference](./configuration.md) — all ~90 environment variables, service names, log levels
- [Persistence](./persistence.md) — storage backend configuration; see [Storage backends](./storage.md) for the durability comparison
- [HTTPS / TLS](./https.md) — browser-trusted HTTPS and HTTP/2 in two commands
- [Debug endpoints](./debug-endpoints.md) — health, metrics, state dump, request tracing, pprof
- [Troubleshooting](./troubleshooting.md) — startup preflight warnings and what they mean

### Storage and performance

- [Performance](./performance.md) — startup expectations, storage tuning, and where "feels slow" time actually goes
- [Storage backends](./storage.md) — durability comparison and what survives a restart, per backend

---

## Support level legend

Every endpoint in the service docs carries one of these statuses:

| Status         | Meaning                                                        |
| -------------- | -------------------------------------------------------------- |
| ✅ Supported   | Fully implemented. AWS SDK calls work as expected.             |
| ⚠️ Partial     | Implemented but with caveats. See the notes column for detail. |
| 🚧 WIP         | Under active development. May be broken or incomplete.         |
| ❌ Unsupported | Not implemented. Returns `501 Not Implemented`.                |

### Service emulation tiers

Each service also has an overall emulation tier, visible on the health
endpoint (`/_overcast/health`) and the web dashboard:

| Tier        | Meaning                                                                                                                                                                           |
| ----------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Full**    | P1+P2 operations implemented. Real SDK clients can use it end-to-end.                                                                                                             |
| **Partial** | P1 operations implemented. Basic workflows work.                                                                                                                                  |
| **Inert**   | Full CRUD works — resources are created and stored — but no side-effects or enforcement occur. For example, IAM stores users, roles, and policies but never enforces permissions. |
| **Stub**    | Registered so discovery works: at most a hardcoded, stateless answer to the service's describe call; every other operation returns `501 Not Implemented`.                        |

Endpoints marked **Unsupported** return a well-formed AWS error response so
that SDKs surface a clear error rather than a connection failure:

```
HTTP 501 Not Implemented
x-emulator-unsupported: true

{
  "__type": "NotImplemented",
  "message": "This operation is not yet emulated. See https://github.com/overcast-sh/overcast/docs/services/<service>.md"
}
```

---

## Services

For a shorter overview, start with the [service reference index](./services/README.md).

<!-- BEGIN overcast:service-index -->

| Service          | Doc                                                 | Ops | Coverage tier                 |
| ---------------- | --------------------------------------------------- | --- | ----------------------------- |
| S3               | [s3.md](./services/s3.md)                           | 53  | Comprehensive / broad support |
| SQS              | [sqs.md](./services/sqs.md)                         | 21  | Comprehensive / broad support |
| DynamoDB         | [dynamodb.md](./services/dynamodb.md)               | 28  | Comprehensive / broad support |
| Lambda           | [lambda.md](./services/lambda.md)                   | 59  | Comprehensive / broad support |
| API Gateway      | [apigateway.md](./services/apigateway.md)           | 106 | Comprehensive / broad support |
| AppSync          | [appsync.md](./services/appsync.md)                 | 82  | Comprehensive / broad support |
| CloudFront       | [cloudfront.md](./services/cloudfront.md)           | 89  | Comprehensive / broad support |
| Cognito          | [cognito.md](./services/cognito.md)                 | 70  | Comprehensive / broad support |
| EC2 / VPC        | [ec2.md](./services/ec2.md)                         | 72  | Comprehensive / broad support |
| SNS              | [sns.md](./services/sns.md)                         | 30  | Comprehensive / broad support |
| IAM              | [iam.md](./services/iam.md)                         | 74  | Core CRUD + common workflows  |
| ECS              | [ecs.md](./services/ecs.md)                         | 48  | Core CRUD + common workflows  |
| ECR              | [ecr.md](./services/ecr.md)                         | 22  | Core CRUD + common workflows  |
| KMS              | [kms.md](./services/kms.md)                         | 33  | Core CRUD + common workflows  |
| Kinesis          | [kinesis.md](./services/kinesis.md)                 | 23  | Core CRUD + common workflows  |
| EventBridge      | [eventbridge.md](./services/eventbridge.md)         | 29  | Core CRUD + common workflows  |
| Scheduler        | [scheduler.md](./services/scheduler.md)             | 12  | Core CRUD + common workflows  |
| CloudFormation   | [cloudformation.md](./services/cloudformation.md)   | 52  | Core CRUD + common workflows  |
| RDS              | [rds.md](./services/rds.md)                         | 34  | Core CRUD + common workflows  |
| ElastiCache      | [elasticache.md](./services/elasticache.md)         | 24  | Core CRUD + common workflows  |
| EFS              | [efs.md](./services/efs.md)                         | 31  | Core CRUD + common workflows  |
| AppConfig        | [appconfig.md](./services/appconfig.md)             | 20  | Core CRUD + common workflows  |
| AppConfigData    | [appconfigdata.md](./services/appconfigdata.md)     | 2   | Core CRUD + common workflows  |
| Secrets Manager  | [secretsmanager.md](./services/secretsmanager.md)   | 22  | Core CRUD + common workflows  |
| SSM              | [ssm.md](./services/ssm.md)                         | 18  | Core CRUD + common workflows  |
| CloudWatch Logs  | [cloudwatch-logs.md](./services/cloudwatch-logs.md) | 22  | Core CRUD + common workflows  |
| SES              | [ses.md](./services/ses.md)                         | 45  | Core CRUD + common workflows  |
| STS              | [sts.md](./services/sts.md)                         | 11  | Core CRUD + common workflows  |
| Route 53         | [route53.md](./services/route53.md)                 | 25  | Core CRUD + common workflows  |
| Auto Scaling     | [autoscaling.md](./services/autoscaling.md)         | 25  | Core CRUD + common workflows  |
| Step Functions   | [stepfunctions.md](./services/stepfunctions.md)     | 15  | Minimal / targeted support    |
| Pipes            | [pipes.md](./services/pipes.md)                     | 8   | Minimal / targeted support    |
| WAF v2           | [waf.md](./services/waf.md)                         | 7   | Minimal / targeted support    |
| Shield           | [shield.md](./services/shield.md)                   | 8   | Minimal / targeted support    |
| ACM              | [acm.md](./services/acm.md)                         | 10  | Minimal / targeted support    |
| Athena           | [athena.md](./services/athena.md)                   | 11  | Minimal / targeted support    |
| Bedrock          | [bedrock.md](./services/bedrock.md)                 | 2   | Minimal / targeted support    |
| CloudWatch       | [cloudwatch.md](./services/cloudwatch.md)           | 17  | Minimal / targeted support    |
| DynamoDB Streams | [dynamodbstreams.md](./services/dynamodbstreams.md) | 4   | Minimal / targeted support    |
| Firehose         | [firehose.md](./services/firehose.md)               | 9   | Minimal / targeted support    |
| Glue             | [glue.md](./services/glue.md)                       | 11  | Minimal / targeted support    |
| OpenSearch       | [opensearch.md](./services/opensearch.md)           | 8   | Minimal / targeted support    |
| AppRegistry      | [appregistry.md](./services/appregistry.md)         | 22  | IaC/discovery-oriented stub   |
| Backup           | [backup.md](./services/backup.md)                   | 12  | IaC/discovery-oriented stub   |
| CloudTrail       | [cloudtrail.md](./services/cloudtrail.md)           | 12  | IaC/discovery-oriented stub   |
| EKS              | [eks.md](./services/eks.md)                         | 50  | IaC/discovery-oriented stub   |
| ELBv2            | [elb.md](./services/elb.md)                         | 21  | IaC/discovery-oriented stub   |
| MSK              | [msk.md](./services/msk.md)                         | 30  | IaC/discovery-oriented stub   |
| Organizations    | [organizations.md](./services/organizations.md)     | 9   | IaC/discovery-oriented stub   |
| Transfer Family  | [transfer.md](./services/transfer.md)               | 13  | IaC/discovery-oriented stub   |

<!-- END overcast:service-index -->

Want to add support for a new AWS service? See
[CONTRIBUTING.md § How to add a service](https://github.com/overcast-sh/overcast/blob/main/CONTRIBUTING.md#how-to-add-a-service)
in the repository.

---

## HTTPS / TLS

```bash
overcast https enable            # once per machine: local CA → system trust store
OVERCAST_TLS=auto overcast serve # both listeners now serve HTTPS + HTTP/2
```

Full guide, including Docker setup, offline behaviour, and bringing your own
certificate: **[HTTPS and HTTP/2](./https.md)**.

---

## Event pipelines

| Pipeline                          | Status       |
| --------------------------------- | ------------ |
| SNS → SQS subscription            | ✅ Supported |
| SQS → Lambda event source mapping | ✅ Supported |
| DynamoDB Streams → SQS (Pipes)    | ✅ Supported |
| DynamoDB Streams → Lambda (ESM)   | ✅ Supported |

---

## Web management console

The full image (`ghcr.io/overcast-sh/overcast`) includes a web management console
accessible at **http://localhost:4567** (configurable via `OVERCAST_UI_PORT`
env var / `--ui-port` flag; `0` disables it). It is served in-process by the
same binary — there is no separate console server to start or supervise — and
the AWS API on port 4566 does not depend on it.

| Feature | What it does |
| --- | --- |
| Dashboard | Service cards with real-time status |
| Per-service UI | S3 browser, SQS message inspector, DynamoDB item editor, Lambda test/invoke, and more |
| Live activity feed | Real-time stream of API calls — operation, resource, status code, latency |
| Inbox | Capture inbox for outbound email/SMS from SES, SNS, and Cognito — browse, search, and inspect headers and body without a third-party mail catcher |
| Topology map | Cross-service resource relationships |
| Real-time updates | Server-sent events push changes as they happen |

> [!TIP]
> If the console feels sluggish or stops responding to clicks while many
> Lambdas run or transfers are in flight, you are hitting the browser's
> 6-connection HTTP/1.1 limit — the live feed and progress streams are
> holding the sockets. Serve the console over HTTPS to unlock HTTP/2 and
> keep it responsive under any load: see [HTTPS and HTTP/2](./https.md).
