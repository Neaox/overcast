# AWS Compatibility Review

This directory tracks Overcast service fidelity against AWS documentation. It exists so compatibility reviews can resume across sessions and agents without relying on conversation history.

## Rules

- AWS API Reference and Developer Guide docs are the primary source of truth.
- Real AWS must not be used unless the user explicitly approves it in the current conversation.
- Every operation marked `reviewed` must have evidence in the per-service review file and tests for the behavior Overcast claims to support.
- If Overcast intentionally does not emulate an AWS behavior, mark it `intentionally_partial` or `unsupported` and document the gap.
- Add tests before implementation fixes.
- Update this tracker before ending a compatibility review session.

## Files

- `matrix.yaml` is the global service-level index for priority, claims, progress counts, and next actions.
- `services/<service>.yaml` is the operation/scenario source of truth for AWS docs links, tests, findings, gaps, and handoff notes.
- `review-template.yaml` is the template for new service review files.

## Status Values

- `not_started`: no review has started.
- `docs_reviewed`: AWS docs were read, but tests/implementation are not fully checked.
- `tests_missing`: documented behavior lacks tests.
- `tests_added`: tests were added and are expected to fail or have not yet been fixed.
- `implementation_mismatch`: Overcast behavior differs from documented AWS behavior.
- `fixed`: a mismatch was fixed, but the broader operation review is not complete.
- `reviewed`: docs, implementation, tests, and service docs agree for the claimed support level.
- `blocked`: review cannot continue without a decision or approved real AWS check.
- `intentionally_partial`: known AWS behavior is deliberately not implemented and documented.
- `unsupported`: operation or behavior is not supported.

## Resume Workflow

1. Read `.agents/skills/aws-compatibility-review/SKILL.md`.
2. Read `matrix.yaml`.
3. Open the highest-priority service file in `services/`.
4. Continue from that file's `Next Agent Handoff` section.
5. Fetch and read the AWS docs linked for the selected operation.
6. Add failing tests for missing or mismatched behavior.
7. Implement the smallest correct fix.
8. Run scoped tests and vet.
9. Update the service review file, summarize progress in `matrix.yaml`, and update `CHANGELOG.md` when behavior changed.

## Tracking Granularity

Avoid duplicating operation details in both the global and per-service files.

- Global `matrix.yaml`: service-level status, priority, optional claim, progress counts, focus areas, and next action.
- Per-service YAML files: operation matrix, scenario matrix, AWS docs URLs, tests, evidence, known gaps, findings, and detailed handoff.

If two files disagree, treat the per-service file as authoritative for operation-level detail and update the global summary to match.

## Required Operation Checklist

Each reviewed operation must cover applicable items:

- [ ] Request protocol, target/path, and content type.
- [ ] Required parameters.
- [ ] Optional parameters and defaults.
- [ ] Validation rules and constraints.
- [ ] Success response shape and status.
- [ ] Empty-result behavior.
- [ ] Error codes and HTTP statuses.
- [ ] Pagination behavior.
- [ ] Idempotency and duplicate handling.
- [ ] State transitions.
- [ ] ARN, account, region, URL, and identifier formats.
- [ ] Timestamp formats and units.
- [ ] SDK compatibility.
- [ ] Capability docs match behavior.
- [ ] Tests prove the claimed behavior.

## Required Scenario Matrix

Each operation review must identify resource configurations and documented callouts that can change behavior. Do not mark an operation `reviewed` when only the default configuration has been checked.

Track applicable scenarios for:

- Default/minimal resources.
- Common configured resources.
- Mutually exclusive modes.
- Optional feature flags enabled and disabled.
- Resource lifecycle states.
- Name/ARN/URL/generated-ID/alias identifier variants.
- Duplicate, stale, missing, and deleted resources.
- Cross-resource relationships.
- Boundary values and invalid enums.
- All supported protocol variants.

For Cognito specifically, username behavior must distinguish at least plain username pools, `UsernameAttributes`, `AliasAttributes`, verified aliases, duplicate aliases, `ForceAliasCreation`, auth challenges, admin APIs, and `ListUsers` filter behavior.

## Priority Guidance

Every service in `matrix.yaml` must have a priority.

- `p0`: highest-impact services for local app runtime, CDK deployments, SDK compatibility, or known active bugs.
- `p1`: important services with broad usage or meaningful IaC/runtime interaction, but lower immediate risk.
- `p2`: smaller surfaces, metadata-only/stub-like services, or services less likely to block local runtime.

Work `p0` first, unless the user reports a live issue in another service.

Recommended `p0` start order:

1. `sqs` — active Motenova runtime issue; finish `ReceiveMessage`, FIFO/DLQ behavior, validation, and JSON/Query empty-response shapes.
2. `cognito` — active Motenova runtime issue; finish username attributes, aliases, `ForceAliasCreation`, auth challenges, and `ListUsers` filters.
3. `s3` — core SDK and CDK dependency; review listing, policies, versioning, tagging, and bootstrap flows.
4. `dynamodb` — core app data path; review expressions, pagination, indexes, streams, and SDK protocol coverage.
5. `lambda` — core runtime and CDK dependency; review invoke semantics, errors, layers, and event source mappings.
6. `cloudformation` — CDK deployment critical path; review stack lifecycle, resource handlers, and unsupported-resource handling.
7. `iam` — CDK and auth-shape critical path; review roles, policies, instance profiles, and enforcement documentation.
8. `ec2` — CDK/VPC critical path; review VPCs, subnets, routes, security groups, ENIs, and Query XML shapes.
9. `ecs` — container workload critical path; review task definitions, `RunTask`, services, and Fargate `awsvpc` behavior.
10. `apigateway` — API execution and CDK path; review route matching, integrations, authorizers, and CloudFormation coverage.
11. `appsync` — large surface with auth/execution complexity; review claims category by category.
12. `ses` — app messaging path and mixed protocol surface; review v1 Query and v2 REST-JSON behavior.
13. `ssm` — common config dependency; reconcile capabilities/docs and review Parameter Store semantics.
14. `sts` — credential-shape dependency; review fake credential responses and unsupported operation tracking.
15. `bedrock` — likely wire-path divergence; review runtime paths and canned response shapes.
16. `opensearch` — likely wire-path divergence; review AWS OpenSearch Service REST paths.
17. `appconfig` — capability tracking gap; reconcile hosted configuration version operations.

Then work `p1`, prioritizing services with current users or CDK paths: `rds`, `sns`, `eventbridge`, `cloudwatch`, `cloudwatch-logs`, `kms`, `kinesis`, `ecr`, `scheduler`, `pipes`, `route53`, `elbv2`, `autoscaling`, `backup`, `cloudtrail`, `eks`, `msk`, `secretsmanager`, `stepfunctions`, `transfer`, `waf`, and `appregistry`.

Work `p2` last unless requested: `acm`, `athena`, `elasticache`, `firehose`, `glue`, `organizations`, `shield`, and `appconfigdata`.
