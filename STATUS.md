# STATUS.md — Current implementation status

> Tracks what's built and what's next. Update as features land.
> For coding conventions see [CONTRIBUTING.md](./CONTRIBUTING.md).
> For agent workflow rules see [AGENTS.md](./AGENTS.md).

---

## Service coverage

50 AWS services are registered. Coverage varies from comprehensive to stub.

### Comprehensive — core + advanced features

| Service     | Ops | Highlights                                                                                                       |
| ----------- | --- | ---------------------------------------------------------------------------------------------------------------- |
| S3          | 53  | Bucket CRUD, object CRUD, list, copy, multipart, notifications                                                   |
| SQS         | 21  | Queue + message CRUD, batches, purge, attributes, visibility, DLQ, FIFO, long polling                            |
| DynamoDB    | 28  | Table/item CRUD, Scan, Query, Streams, TTL, batch ops, transactions                                              |
| Lambda      | 54  | Function CRUD, Invoke (Docker), versions, aliases, layers, event source mappings, function URLs (Host-routed invoke) |
| API Gateway | 106 | REST v1 + HTTP v2: full CRUD, stages, deployments, Lambda/MOCK/HTTP proxy execution, authorizers, API keys       |
| AppSync     | 82  | Full CRUD, GraphQL execution (NONE/HTTP/Lambda/DynamoDB), CloudFormation/CDK provisioning, merged APIs, Events API, channel namespaces |
| CloudFront  | 89  | Distribution CRUD, invalidations, OAC/OAI, cache policies, CloudFront Functions, key groups, field-level encrypt |
| Cognito     | 70  | User Pools + Clients, Users, Auth flows, TOTP MFA, Groups, RS256 JWT + JWKS endpoint                             |
| EC2 / VPC   | 72  | Instances, VPCs, subnets, security groups, key pairs, route tables, IGWs, VPC peering                            |
| SNS         | 29  | Topics, subscriptions (SQS/email), Publish/PublishBatch, FilterPolicy message filtering                          |

### Core operations — basic CRUD + common features

| Service         | Ops | Highlights                                                                                                                                                                                                 |
| --------------- | --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| IAM             | 72  | Users, roles, groups, policies, instance profiles; real policy simulation, **opt-in** request-time enforcement (`OVERCAST_ENFORCE_IAM`, default off)                                                                                                                                    |
| ECS             | 48  | Clusters, task definitions, tasks (Docker), services with reconciler                                                                                                                                       |
| ECR             | 20  | Repository CRUD + registry metadata (DescribeRegistry), image metadata (PutImage/DescribeImages/BatchGetImage/BatchDeleteImage/DescribeImageScanFindings), auth token, repository+lifecycle policies, tags |
| KMS             | 33  | Keys, aliases, symmetric AES-256-GCM + RSA-2048 signing                                                                                                                                                    |
| Kinesis         | 20  | Streams, records, shards, tags, retention                                                                                                                                                                  |
| EventBridge     | 28  | Event buses, rules, targets, PutEvents, tags                                                                                                                                                               |
| Scheduler       | 12  | Schedule groups, schedules, tags, clock-driven Lambda/SQS target firing                                                                                                                                    |
| CloudFormation  | 52  | Stacks, change sets, async provisioner (~55 resource types including AppSync), intrinsic functions, GetAtt                                                                                                  |
| RDS             | 34  | DB instances (Docker), start/stop, modify, subnet/parameter groups                                                                                                                                         |
| ElastiCache     | 24  | Clusters (Docker Redis), replication groups, subnet groups, tagging                                                                                                                                        |
| AppConfig       | 19  | Apps, environments, profiles, hosted config versions (CRUD + version counter)                                                                                                                              |
| AppConfigData   | 3   | StartConfigurationSession, GetLatestConfiguration; poll-based delivery with "unchanged" detection                                                                                                          |
| Secrets Manager | 22  | Secret CRUD, versioning, tags, rotation config (11 of 21 operations)                                                                                                                                       |
| SSM             | 18  | Parameter Store: put, get, get-by-path, history, tags                                                                                                                                                      |
| CloudWatch Logs | 19  | Log groups, streams, events, FilterLogEvents, DeleteLogStream                                                                                                                                              |
| SES             | 45  | v1 + v2: SendEmail, SendRawEmail, identities, mail capture                                                                                                                                                 |
| STS             | 11  | GetCallerIdentity, AssumeRole, GetSessionToken, temp credentials                                                                                                                                           |
| Route 53        | 25  | Hosted zones (default NS/SOA, delegation sets), validated change batches, DNS-order pagination, tags, health checks — inert (no DNS served)                                                                |
| Step Functions  | 14  | State machine CRUD plus a real ASL interpreter: all eight state types, Retry/Catch, Lambda/SQS/SNS/DynamoDB/nested-execution Task integrations, real GetExecutionHistory. Executions run synchronously; unsupported ASL fails loudly |

### Minimal / Stub

| Service        | Ops | Highlights                                                      |
| -------------- | --- | --------------------------------------------------------------- |
| Pipes          | 8   | CreatePipe, DescribePipe, DeletePipe, ListPipes; DDB→SQS only   |
| WAF v2         | 7   | Metadata-only Web ACL CRUD; rules are stored but not evaluated or enforced |
| Shield         | 8   | Minimal subscription and protection metadata for CDK/CF workflows |

### Op counts from capability registry

<!--The tables below are Auto-generated by `go run -tags dev ./cmd/capgen --write-docs`. Do NOT edit manually, your changes will be lost! See ./CONTRIBUTING.md for details -->

<!-- BEGIN overcast:status -->

| Service         | Ops |
| --------------- | --- |
| S3              | 53  |
| SQS             | 21  |
| DynamoDB        | 28  |
| Lambda          | 54  |
| API Gateway     | 106 |
| AppSync         | 82  |
| CloudFront      | 89  |
| Cognito         | 70  |
| EC2 / VPC       | 72  |
| SNS             | 29  |
| IAM             | 72  |
| ECS             | 48  |
| ECR             | 20  |
| KMS             | 33  |
| Kinesis         | 20  |
| EventBridge     | 28  |
| Scheduler       | 12  |
| CloudFormation  | 52  |
| RDS             | 34  |
| ElastiCache     | 24  |
| EFS             | 31  |
| AppConfig       | 19  |
| AppConfigData   | 3   |
| Secrets Manager | 22  |
| SSM             | 18  |
| CloudWatch Logs | 19  |
| SES             | 45  |
| STS             | 11  |
| Route 53        | 25  |
| Auto Scaling    | 25  |
| Step Functions  | 14  |
| Pipes           | 8   |
| WAF v2          | 7   |
| Shield          | 8   |
| ACM             | 10  |
| Athena          | 11  |
| Bedrock         | 2   |
| CloudWatch      | 17  |
| DynamoDB Streams | 4   |
| Firehose        | 9   |
| Glue            | 11  |
| OpenSearch      | 8   |
| AppRegistry     | 22  |
| Backup          | 9   |
| CloudTrail      | 12  |
| EKS             | 52  |
| ELBv2           | 18  |
| MSK             | 29  |
| Organizations   | 1   |
| Transfer Family | 13  |

<!-- END overcast:status -->

---

## Current focus

- **DynamoDB** — full UpdateTable (GSI/LSI, provisioned throughput changes)
- **IAM** — integration test coverage (33 ops implemented, zero test coverage)
- **DynamoDB Streams** — dedicated integration tests
- **ECR** — real OCI registry push/pull support behind the existing control plane

## Future roadmap

Tracked in [GitHub Issues](https://github.com/Neaox/overcast/issues).
`// TODO(priority:Pn):` comments in code are auto-converted to issues.

- Step Functions `.waitForTaskToken`, activity tasks and distributed Map (the ASL interpreter landed; these are what it still refuses)
- API Gateway advanced features (throttle/quota enforcement, cache settings)
- Lambda `ImageConfig` overrides for container image functions
- Topology graph enhancements (`internal/router/topology.go`)
