# STATUS.md — Current implementation status

> Tracks what's built and what's next. Update as features land.
> For coding conventions see [CONTRIBUTING.md](./CONTRIBUTING.md).
> For agent workflow rules see [AGENTS.md](./AGENTS.md).

---

## Service coverage

49 AWS services are registered. Coverage varies from comprehensive to stub.

### Comprehensive — core + advanced features

| Service     | Ops | Highlights                                                                                                       |
| ----------- | --- | ---------------------------------------------------------------------------------------------------------------- |
| S3          | 44  | Bucket CRUD, object CRUD, list, copy, multipart, notifications                                                   |
| SQS         | 21  | Queue + message CRUD, batches, purge, attributes, visibility, DLQ, FIFO, long polling                            |
| DynamoDB    | 19  | Table/item CRUD, Scan, Query, Streams, TTL, batch ops, transactions                                              |
| Lambda      | 33  | Function CRUD, Invoke (Docker), versions, aliases, layers, event source mappings                                 |
| API Gateway | 105 | REST v1 + HTTP v2: full CRUD, stages, deployments, Lambda/MOCK/HTTP proxy execution, authorizers, API keys       |
| AppSync     | 82  | Full CRUD, GraphQL execution (NONE/HTTP/Lambda/DynamoDB), merged APIs, Events API, channel namespaces            |
| CloudFront  | 89  | Distribution CRUD, invalidations, OAC/OAI, cache policies, CloudFront Functions, key groups, field-level encrypt |
| Cognito     | 67  | User Pools + Clients, Users, Auth flows, TOTP MFA, Groups, RS256 JWT + JWKS endpoint                             |
| EC2 / VPC   | 72  | Instances, VPCs, subnets, security groups, key pairs, route tables, IGWs, VPC peering                            |
| SNS         | 24  | Topics, subscriptions (SQS/email), Publish/PublishBatch, FilterPolicy message filtering                          |

### Core operations — basic CRUD + common features

| Service         | Ops | Highlights                                                                                                                                                                                                 |
| --------------- | --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| IAM             | 61  | Users, roles, groups, policies, instance profiles; **no enforcement**                                                                                                                                      |
| ECS             | 48  | Clusters, task definitions, tasks (Docker), services with reconciler                                                                                                                                       |
| ECR             | 20  | Repository CRUD + registry metadata (DescribeRegistry), image metadata (PutImage/DescribeImages/BatchGetImage/BatchDeleteImage/DescribeImageScanFindings), auth token, repository+lifecycle policies, tags |
| KMS             | 32  | Keys, aliases, symmetric AES-256-GCM + RSA-2048 signing                                                                                                                                                    |
| Kinesis         | 17  | Streams, records, shards, tags, retention                                                                                                                                                                  |
| EventBridge     | 28  | Event buses, rules, targets, PutEvents, tags                                                                                                                                                               |
| Scheduler       | 12  | Schedule groups, schedules, tags, clock-driven Lambda/SQS target firing                                                                                                                                    |
| CloudFormation  | 47  | Stacks, change sets, async provisioner (~50 resource types), intrinsic functions, GetAtt                                                                                                                   |
| RDS             | 33  | DB instances (Docker), start/stop, modify, subnet/parameter groups                                                                                                                                         |
| ElastiCache     | 24  | Clusters (Docker Redis), replication groups, subnet groups, tagging                                                                                                                                        |
| AppConfig       | 12  | Apps, environments, profiles, hosted config versions (CRUD + version counter)                                                                                                                              |
| AppConfigData   | 3   | StartConfigurationSession, GetLatestConfiguration; poll-based delivery with "unchanged" detection                                                                                                          |
| Secrets Manager | 21  | Secret CRUD, versioning, tags, rotation config (11 of 21 operations)                                                                                                                                       |
| SSM             | 18  | Parameter Store: put, get, get-by-path, history, tags                                                                                                                                                      |
| CloudWatch Logs | 19  | Log groups, streams, events, FilterLogEvents, DeleteLogStream                                                                                                                                              |
| SES             | 42  | v1 + v2: SendEmail, SendRawEmail, identities, mail capture                                                                                                                                                 |
| STS             | 11  | GetCallerIdentity, AssumeRole, GetSessionToken, temp credentials                                                                                                                                           |

### Minimal / Stub

| Service        | Ops | Highlights                                                      |
| -------------- | --- | --------------------------------------------------------------- |
| Step Functions | 5   | State machine CRUD, StartExecution; **no execution engine yet** |
| Pipes          | 5   | CreatePipe, DescribePipe, DeletePipe, ListPipes; DDB→SQS only   |
| WAF v2         | 4   | Web ACL CRUD only                                               |
| Shield         | 5   | Stub — all ops return 501; satisfies CDK/CF discovery calls     |

### Op counts from capability registry

<!--The tables below are Auto-generated by `go run -tags dev ./cmd/capgen --write-docs`. Do NOT edit manually, your changes will be lost! See ./CONTRIBUTING.md for details -->

<!-- BEGIN overcast:status -->

| Service         | Ops |
| --------------- | --- |
| S3              | 44  |
| SQS             | 21  |
| DynamoDB        | 19  |
| Lambda          | 33  |
| API Gateway     | 105 |
| AppSync         | 82  |
| CloudFront      | 89  |
| Cognito         | 67  |
| EC2 / VPC       | 72  |
| SNS             | 24  |
| IAM             | 61  |
| ECS             | 48  |
| ECR             | 20  |
| KMS             | 32  |
| Kinesis         | 17  |
| EventBridge     | 28  |
| Scheduler       | 12  |
| CloudFormation  | 47  |
| RDS             | 33  |
| ElastiCache     | 24  |
| AppConfig       | 12  |
| AppConfigData   | 3   |
| Secrets Manager | 21  |
| SSM             | 18  |
| CloudWatch Logs | 19  |
| SES             | 42  |
| STS             | 11  |
| Step Functions  | 5   |
| Pipes           | 5   |
| WAF v2          | 4   |
| Shield          | 5   |
| ACM             | 7   |
| Athena          | 8   |
| Bedrock         | 2   |
| CloudWatch      | 12  |
| DynamoDB Streams | 4   |
| Firehose        | 6   |
| Glue            | 8   |
| OpenSearch      | 8   |
| AppRegistry     | 21  |
| Auto Scaling    | 19  |
| Backup          | 9   |
| CloudTrail      | 9   |
| EKS             | 52  |
| ELBv2           | 15  |
| MSK             | 29  |
| Organizations   | 1   |
| Route 53        | 10  |
| Transfer Family | 10  |

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

- Step Functions execution engine (currently no-op)
- API Gateway advanced features (throttle/quota enforcement, cache settings)
- Lambda `ImageConfig` overrides for container image functions
- Topology graph enhancements (`internal/router/topology.go`)
