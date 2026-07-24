# Changelog

All notable changes to Overcast are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## Versioning rules

| Change type                                            | Version bump  | Examples                                                                     |
| ------------------------------------------------------ | ------------- | ---------------------------------------------------------------------------- |
| Breaking change to any supported API endpoint          | MAJOR (x.0.0) | Changing response format, removing a supported field, changing env var names |
| New service or new supported endpoint                  | MINOR (0.x.0) | Adding DynamoDB support, adding S3 multipart upload                          |
| Bug fix, performance improvement, new internal feature | PATCH (0.0.x) | Fixing a response field, adding a missing header, improving error messages   |
| Documentation, test, CI changes only                   | PATCH (0.0.x) | No user-facing change                                                        |

**When in doubt, bump MINOR.** We would rather ship a minor bump that didn't
need it than accidentally ship a breaking change as a patch.

### What counts as a breaking change

- Removing or renaming an environment variable
- Changing the format of a response that was previously emitting a different format
- Removing a previously supported endpoint (demoting ✅ to ❌)
- Changing the default value of a configuration option in a way that alters existing behaviour
- Changing the port default

### What does NOT count as a breaking change

- Adding new fields to a response (AWS SDKs ignore unknown fields)
- Adding new endpoints
- Adding new environment variables
- Improving error messages
- Performance improvements
- Fixing a response that was wrong (bug fix > compatibility with wrong behaviour)

---

## [Unreleased]

<!-- AGENT INSTRUCTIONS — updating this section between releases
     - Do NOT add a new bullet for every small change.
     - This file is used as release-note input. Only include shipped/runtime or
       user-visible changes: service/API behaviour, compatibility fixes,
       config/env vars, Docker/binary packaging, measured performance changes,
       or user-facing docs guidance.
     - Do NOT add entries for CI-only changes, test-only changes, local tooling,
       internal refactors, or cleanup unless they affect shipped artifacts or
       runtime behaviour.
     - Find the existing bullet for the relevant service/area and extend it inline.
     - Only add a new bullet when introducing a genuinely new service or a cross-cutting
       feature that has no natural home in an existing bullet.
     - Exception: if a change must reference a specific commit (e.g. a targeted bug fix
       for a shipped version), add a dedicated bullet with the commit reference so it
       can be promoted to a versioned entry cleanly.
     - Keep bullets concise — one sentence per service is the target; use semicolons to
       append new capabilities rather than splitting into sub-bullets.
     - For bug fixes, describe the full affected scope discovered during investigation,
       not only the original symptom or repro case. If one root cause affected multiple
       services, resource types, commands, or user-visible paths, mention those impacts
       in the release note while keeping the entry concise.
     - The changelog only needs changes that affect shipped artifacts or release
       notes: runtime behaviour, AWS compatibility, config/env vars, Docker or
       binary packaging, release process, or user-facing docs.
     - Release section dates use UTC in YYYY-MM-DD format.
-->

### Added

- **Debug endpoints** — `GET /_debug/metrics` now reports storage diagnostics (recent flush history, seed duration, pending-log size, and opt-in per-namespace row counts via `?includeRowCounts=true`) instead of a "not yet implemented" stub; `GET /_debug/state/{namespace}` is now paginated, returning `{values, nextKey}` pages (`?after=` exclusive cursor, `?limit=` capped at 5000, default 500) instead of a single flat key→value map — a response-shape change for anything scripting against it (`?key=` single-value fetches are unchanged); the web Raw State Debugger now pages incrementally (fetching further pages only as the user scrolls near the end of what's loaded) instead of eagerly merging every page, virtualizes both the flat key table and the key tree (which also gained per-node collapse/expand) so large namespaces render a bounded number of DOM rows, lazily fetches a deep-linked key's value via the single-key endpoint when it hasn't loaded yet, and restricts search to key-only matching over loaded rows.
- **Storage** — SQLite-backed storage now applies versioned schema migrations automatically on startup instead of ad-hoc `CREATE TABLE IF NOT EXISTS` calls, writing a one-time backup file before the first pending migration runs against an existing database, and periodically checkpoints its WAL and reclaims free pages in the background instead of only growing the database file over time; new `OVERCAST_HYBRID_SYNC`/`OVERCAST_HYBRID_SYNC_INTERVAL` (pending-log fsync policy — the hybrid pending log is now fsynced on a 100ms interval by default, where it was previously never fsynced and an OS crash could lose the whole unflushed window), `OVERCAST_HYBRID_DIRTY_ENTRY_THRESHOLD`/`OVERCAST_HYBRID_DIRTY_BYTE_THRESHOLD` (size-triggered early flush, so write bursts flush ahead of the timer instead of accumulating unboundedly), and `OVERCAST_HYBRID_MAINTENANCE_INTERVAL` (WAL checkpoint/vacuum cadence) config options.

### Fixed

- **Pagination** — invalid continuation tokens passed to CloudFormation `DescribeStackEvents`, CloudFront's listing operations, and SSM `DescribeParameters`/`GetParametersByPath`/`GetParameterHistory` now return each operation's documented AWS error (`ValidationError`/`InvalidArgument`/`InvalidNextToken`) instead of silently restarting from the first page — the silent restart caused duplicate delivery for any client paging with a stale or corrupted token; SSM's `MaxResults` defaults and caps now match AWS's documented per-operation values.
- **Storage** — a per-service `OVERCAST_STATE_<SERVICE>` override (e.g. `OVERCAST_STATE_S3=memory`) could silently switch an unrelated service's persistence to memory-only instead of only the overridden service — confirmed for DynamoDB, whose items/streams lost persistence across restarts with no warning whenever any other service had an override configured; every code path that specially detects the storage backend (persistence-capability detection, startup-readiness waiting, persistent-health reporting, CloudFormation's explicit flush) now resolves the correct underlying store first instead of silently losing that capability. A crash or unclean stop leaving a torn final line in the WAL-mode pending log previously refused to start the daemon at all; startup now tolerates and warns instead of aborting, matching the hybrid store's existing behavior. A corrupt or unopenable SQLite database previously poisoned every subsequent read/write with the same error forever; the store now degrades to memory-only (reporting itself unhealthy via the health endpoint) instead of failing every request, and a single undecodable row encountered while seeding memory at startup is skipped with a warning instead of aborting the whole seed. Requests arriving during a startup schema migration previously returned an incorrect empty/not-found result in hybrid mode, or hung indefinitely in persistent mode; they now get an immediate, AWS-shaped `ServiceUnavailable` 503 (already retried automatically by AWS SDKs) until migration finishes. Shutdown now bounds the final store flush to the configured shutdown timeout instead of potentially hanging past it, and the server-error shutdown path runs the same cleanup as a normal signal-triggered shutdown instead of skipping it. CloudWatch metric-data retention enforcement, previously memory-mode only, now also runs periodically in persistent/hybrid modes. Separately: hybrid-mode writes that crossed the size-trigger flush threshold while the background seed was still running could miss their early-flush wakeup and sit unflushed until the next timer interval; S3, Kinesis, CloudWatch Logs, and SNS list operations now skip a malformed persisted record instead of failing the whole listing, and large listings use single-scan reads instead of per-key lookups; hybrid-mode reads of high-volume data (queue messages, log events, metric datapoints) no longer wait behind an in-flight flush transaction — they run on a dedicated read connection pool; bulk deletes (queue purge, log-group deletion, stack teardown) record one ranged tombstone and execute one ranged SQL delete instead of one log entry and one statement per key; API Gateway's REST and HTTP API resource/stage/deployment/route/integration listings now also skip a malformed persisted record instead of failing the whole listing, and their per-API delete-all-by-prefix helpers use a single ranged delete when the store supports it instead of one delete per key (ECS's task-definition-family listing was audited too and confirmed already free of the per-key-lookup pattern, so it needed no change); a store degraded to memory-only now caps its pending-log file at 64 MiB instead of growing it on every write for the rest of the run, and startup log replay streams the file instead of loading it whole. Per-service overrides for CloudFormation, API Gateway, and EventBridge (whose storage prefixes `cfn`/`apigw`/`eb` differ from their config names) and for SSM, KMS, Step Functions, AppSync, and CloudFront (whose storage namespaces contain no `:` separator) were previously accepted but silently never took effect; both routing gaps are fixed and the overrides now work — note that a previously-set override for one of these services activates for the first time against its own fresh per-service store, while data written during the inert period stays in the default store. Overrides that can never have an effect (`dynamodbstreams` — a facade over DynamoDB; `sts` — its state lives under IAM; `bedrock`/`organizations` — stateless stubs) now log a startup warning instead of failing silently.
- **CloudWatch** — `PutMetricData` no longer rescans and re-decodes the metric's already-retained datapoints on every write (a cost that grew with the number of points in the retention window, making sustained bursts to one metric progressively slower on the hybrid/persistent backends); retention is unchanged — reads still filter expired points and the periodic background sweep still deletes them.
- **CloudWatch Logs** — log event storage moved from one read-modify-write JSON blob per stream to a dedicated indexed table, with existing data converted by the one-time startup migration (after the automatic pre-migration database backup); this fixes append/read cost that previously grew with a stream's history — measured at 1,000,000 pre-existing events, appends went from ~1.46s/903MB to ~1.2µs/216B per call, and a full-stream read went from ~1.88s/401MB to ~10ms/32MB. `RetentionInDays` is now enforced in persistent/hybrid modes via a periodic ranged delete instead of being stored but never acted on outside memory mode. Log events remain visible via the Raw State Debugger and resettable via `/_debug/reset` despite no longer living in the generic key-value store.
- **CloudFormation** — stack events moved from one read-modify-write JSON blob per stack (cost grew with a stack's event history, and concurrent appends could silently drop events) to one row per event, with an existing stack's history converted automatically the first time it's read.
- **Web UI** — the system map log-stream peek drawer now switches directly to a newly clicked lambda instance or log stream row while already open instead of swallowing the click and just closing.

## [0.0.1-alpha.23] - 2026-07-23

### Fixed

- **Web UI** — Raw State Debugger namespace views now truncate large stored value strings for responsive browsing and provide an `Open` action for the selected full raw value with JSON/text content-type detection.
- **Debug endpoints** — `POST /_debug/reset/{service}` now succeeds for enabled services even when they have no stored resources yet instead of reporting them as unknown.
- **AppSync** — `CreateGraphqlApi`, `GetGraphqlApi`, and `ListGraphqlApis` now return local executable GraphQL URLs for the connected Overcast endpoint, Lambda data sources strip ARN aliases/versions before invocation, direct Lambda resolver events include AWS/Powertools-compatible context fields while mapped Lambda requests pass their evaluated JSON payloads through, resolver identities now better match AWS shapes for Cognito/OIDC/IAM including IAM user-store lookup, configured Lambda authorizers now invoke Lambda with GraphQL request context and propagate/cache `resolverContext`/`deniedFields`, mapped Lambda request objects validate operation/invocation type with async `Event` null results, and direct plus VTL/APPSYNC_JS-mapped Lambda nested-list batching now supports `maxBatchSize`.
- **CloudFormation/Lambda** — `AWS::Lambda::Alias` resources now create and delete real Lambda aliases instead of being treated as unsupported/stubbed resources.
- **DynamoDB/Lambda** — DynamoDB stream event source mappings now process records through a bounded per-mapping worker, honor `BatchSize` and `MaximumBatchingWindowInSeconds`, apply filters per record before Lambda invocation, and expose filter decision evidence in the system map through a compact filter node with a searchable ordered receipt-history drawer.
- **DynamoDB** — `BatchWriteItem` and `TransactWriteItems` writes now emit stream records, and composite-key `PutItem` condition checks such as `attribute_not_exists(PK) AND attribute_not_exists(SK)` reject existing items consistently.
- **S3** — bucket encryption APIs now support CDK asset bucket checks by returning default SSE-S3 encryption and round-tripping AES256/KMS encryption configuration.
- **State** — hybrid SQLite-backed reads now retry canceled, busy, locked, and interrupted SQLite operations with bounded backoff, serialize SQLite flushes to avoid concurrent transaction contention, seed hot control-plane namespaces into memory while keeping bulk data-plane namespaces lazy, expose persistent backend health and pending-write counts, persist accepted writes to a pending log before async flush, and force CloudFormation terminal stack state through synchronous persistence.
- **Lambda** — startup no longer pre-pulls every managed runtime image by default; set `LAMBDA_SEED_RUNTIME_IMAGES=true` to opt back into broad startup seeding while per-function prewarming and lazy first-use pulls remain enabled.
- **Web UI** — bundled console builds now include the Tailwind typography plugin as a production build dependency, fixing Docker release builds that install only runtime dependencies.

## [0.0.1-alpha.22] - 2026-07-22

### Added

- **Agent tooling** — repo-local OpenCode skill registration now discovers the Overcast agent skills without manual setup.

### Fixed

- **AppSync/CloudFormation** — `CreateApiKey` now accepts CDK-style `Expires` values 365 days from creation without rolling back `AWS::AppSync::ApiKey` resources.
- **Web UI** — the sidebar now defaults to collapsed on narrow viewports, keeps separate narrow/wide collapse preferences across refreshes, and shows immediate tooltips for collapsed icon navigation.

## [0.0.1-alpha.21] - 2026-07-22

### Fixed

- **AppSync/CloudFormation** — `StartSchemaCreation` now accepts AppSync built-in scalar types in GraphQL SDL, fixing CDK `AWS::AppSync::GraphQLSchema` rollbacks on types such as `AWSDateTime`, while rejecting unsupported custom scalars and custom object types using the reserved `AWS` prefix.

## [0.0.1-alpha.20] - 2026-07-22

### Fixed

- **AppSync/CloudFormation** — `StartSchemaCreation` now accepts AppSync built-in auth and subscription directives in GraphQL SDL, fixing CDK `AWS::AppSync::GraphQLSchema` rollbacks on directives such as `@aws_api_key`.
- **CloudWatch Logs** — `StartLiveTail` now subscribes before emitting `sessionStart`, fixing a race where events written immediately after the session opened could be missed.

## [0.0.1-alpha.19] - 2026-07-22

### Fixed

- **AppSync/CloudFormation** — `AWS::AppSync::GraphQLApi` now accepts CDK/CloudFormation tag arrays, provisions environment variables, exposes `GraphQLEndpointArn`, supports S3-backed schema/resolver/function/channel namespace assets, provisions domain names, domain associations, API caches, source API associations, Events APIs, and channel namespaces, routes AppSync tag APIs without colliding with MSK, and validates `CreateGraphqlApi`/`UpdateGraphqlApi` consistently across REST and typed protocol paths.
- **SQS** — `ReceiveMessage` replay with `ReceiveRequestAttemptId` is invalidated after deletes or visibility changes, `CreateQueue` rejects FIFO-only attributes on standard queues and returns `QueueNameExists` on idempotent conflicts, and `SendMessage` validates delay, FIFO delay, and body size constraints.
- **Web UI** — debug sidebar navigation and raw-state links now stay hidden unless `OVERCAST_DEBUG` is enabled for the connected emulator.

## [0.0.1-alpha.18] - 2026-07-22

### Fixed

- **CloudFormation** — scalar properties forwarded to service APIs now preserve decimal numeric formatting instead of scientific notation across SQS queues, nested stack parameters, RDS flags, and ElastiCache replication group updates; `AWS::SQS::Queue` also forwards KMS/SSE properties so CDK-created FIFO queues with `MessageRetentionPeriod: 1209600` and `KmsMasterKeyId` deploy successfully.

## [0.0.1-alpha.17] - 2026-07-22

### Added

- **CloudFormation / AppSync** — AppSync CDK-style stacks now provision real GraphQL APIs, schemas, API keys, data sources, resolvers, and pipeline functions through CloudFormation, including AWS-shaped `Ref`/`Fn::GetAtt` outputs and local GraphQL execution via `/_appsync/{apiId}/graphql`.
- **Web UI** — added a read-only Raw State Debugger with shipped Go BFF and dev BFF support, service/namespace/resource deep links, search/filtering, copy actions, refresh controls, and contextual raw-state links from S3, SQS, SNS, and DynamoDB resource pages.

### Fixed

- **SQS** — `CreateQueue` and `SetQueueAttributes` now reject invalid queue attributes, including out-of-range `MessageRetentionPeriod`, `DelaySeconds`, `MaximumMessageSize`, `KmsDataKeyReusePeriodSeconds`, invalid boolean/enum values, malformed policy JSON, incompatible SSE settings, and unknown attribute names instead of persisting invalid queue state.
- **Web UI** — S3 object previews now indent XML/RSS closing tags correctly, use file extensions for syntax highlighting when the stored content type is generic, and apply Prism colours for HTML/XML, CSS, and JavaScript tokens instead of only highlighting JSON-like tokens; DynamoDB table detail controls no longer require `crypto.randomUUID` on insecure origins.

## [0.0.1-alpha.16] - 2026-07-21

### Added

- **Docs / Web UI** — the bundled docs now include a browsable documentation route (with a sidebar entry alongside Map/Events/Metrics/Inbox) with server-backed search, generated metadata, full markdown typography for headers/tables/scrollable code blocks, no more raw YAML frontmatter leaking into rendered pages, and CDK local VPC guidance, while excluding developer-only planning notes and contributor-only content (e.g. "how to add a service") from the shipped docs index.

### Fixed

- **Startup metrics** — `startup_duration_ms` now measures Go-side startup work from the earliest Overcast Go timestamp to readiness, while new `pre_init_ms` and an environment timeline phase separately report OS loader, antivirus, container init, entrypoint, and exec time.
- **CloudWatch Logs** — `StartLiveTail` now streams AWS event-stream `sessionStart`/`sessionUpdate` frames with filter pattern and stream name/prefix support, and the web console uses the SDK-backed Live Tail path for log tailing.
- **CloudFormation** — stack create/update/delete now wait briefly for fast provisioning before returning so SDK waiters can observe terminal status immediately, and executed change sets now advance to `EXECUTE_COMPLETE`/`EXECUTE_FAILED` instead of remaining `EXECUTE_IN_PROGRESS`.
- **Lambda** — skipped Lambda layers now emit a warning in the function logs when layer content is unavailable locally and remote AWS fetching is not configured or fails.
- **Web UI** — system map and global search resource links now support middle-click/new-tab navigation with full-name tooltips, CloudWatch log streams are sorted by latest write time, and S3 object previews better format self-closing XML and content types with parameters.

## [0.0.1-alpha.15] - 2026-07-17

### Fixed

- **SQS** — FIFO `ReceiveMessage` retries with the same `ReceiveRequestAttemptId` now return the same in-flight messages and receipt handles instead of returning an empty response, invalid attempt IDs are rejected with `InvalidParameterValue`, `CreateQueue`/`SetQueueAttributes` reject invalid `ReceiveMessageWaitTimeSeconds` queue defaults instead of persisting malformed or out-of-range values, redrive policies now move messages to a DLQ only after the receive count exceeds `maxReceiveCount`, and `ReceiveRequestAttemptId` replay is now correctly invalidated when messages are deleted or have their visibility changed via `ChangeMessageVisibility`/`ChangeMessageVisibilityBatch`; `CreateQueue` now rejects FIFO-only attributes on standard queues and returns `QueueNameExists` on idempotent conflicts; `SendMessage` now validates `DelaySeconds` range (0-900), rejects `DelaySeconds` on FIFO queues, and enforces the 1 MiB message body size limit.
- **SQS / DynamoDB** — `CreateQueue` and `CreateTable` now reject invalid resource names with AWS-modeled validation errors instead of creating resources with unsupported names.
- **Web UI** — CloudWatch Logs viewers now keep formatted log rows stable while scrolling, support separate compact syntax highlighting and pretty-printing, add group-wide interleaved log views with plaintext/table toggles and live tailing, and avoid duplicated system map peek log lines; S3 object inspection now previews common images and text-like objects with JSON/XML/HTML formatting and syntax highlighting.
- **CloudFormation / EventBridge / EC2** — CDK scheduled Fargate task stacks now preserve EventBridge ECS target metadata on `AWS::Events::Rule` create/update, EventBridge can deliver matching events to SQS targets and scheduled ECS/Fargate targets to `RunTask`, and CloudFormation-created VPCs expose subnet tags plus NAT gateway route metadata so CDK can classify private subnet groups during VPC lookups.

## [0.0.1-alpha.14] - 2026-07-14

### Fixed

- **Lambda** — Docker-backed Lambda image pulls now use the same platform as container creation, so x86_64 external extension layers are not paired with host-native arm64 runtime images on Apple Silicon/ARM hosts; Lambda runtime endpoint and credential environment variables now override blank function placeholders so endpoint-aware AWS-calling extensions route SDK requests to Overcast; Lambda docs now describe using recent AWS-calling extension layer versions, matching function architecture, and troubleshooting old extension layers.

## [0.0.1-alpha.13] - 2026-07-14

### Fixed

- **Lambda / Release** — Docker-backed Lambda containers now run the Docker image platform matching the function architecture so extension layer binaries match the runtime architecture on Apple Silicon/ARM hosts, and the hybrid store coverage test no longer fails on race/coverage instrumentation overhead.

## [0.0.1-alpha.12] - 2026-07-14

### Added

- **Lambda** — Docker-backed zip functions now start external extensions from attached layers, expose the Lambda Extensions API lifecycle paths, deliver best-effort Logs API HTTP batches, and route AWS SDK-backed extension calls to Overcast by default.

### Fixed

- **Web UI** — Event Stream source filters now start with visible sources checked while leaving requests and heartbeats off by default, and the separate "Hide requests" toggle was removed in favour of the Requests source checkbox; System Map Lambda trigger event tabs now decode base64 payloads and pretty-print JSON instead of showing encoded strings.

## [0.0.1-alpha.11] - 2026-07-14

### Fixed

- **ElastiCache / RDS / Lambda** — Docker-backed RDS and ElastiCache endpoints now register their advertised synthetic hostnames as Docker DNS aliases on the Lambda network, so Lambda execution containers can resolve cache/database endpoint hostnames returned by CloudFormation.

## [0.0.1-alpha.10] - 2026-07-14

### Fixed

- **API Gateway** — REST and HTTP API Lambda proxy events now encode empty request bodies as JSON `null` for payload format 1.0 instead of `""`, matching AWS event shapes for no-body requests.

## [0.0.1-alpha.9] - 2026-07-14

### Fixed

- **Web UI** — S3 bucket lifecycle events in the Event Stream now summarize bucket names across create/delete payload variants, and the system map now shows ElastiCache serverless caches and replication groups instead of only cache clusters.
- **Lambda** — functions that reference foreign-account AWS-managed layer ARNs now pass invoke-time layer validation when the documented layer cache contains the layer zip, instead of failing before cached layer content can be resolved.

## [0.0.1-alpha.8] - 2026-07-14

### Fixed

- **CloudFront / CloudFormation** — `AWS::CloudFront::Distribution` now accepts cache behaviors whose `TargetOriginId` references an origin group, matching the raw CloudFront API path for origin-failover distributions.
- **Route53 / CloudFormation** — `AWS::Route53::RecordSet` now preserves alias targets when provisioning through CloudFormation instead of dropping them from the underlying Route53 request.
- **Web UI** — S3 bucket lifecycle events in the Event Stream now show `s3://bucket-name` instead of `s3:///`.

## [0.0.1-alpha.7] - 2026-07-14

### Fixed

- **CloudFront / CloudFormation** — `CreateDistribution` now accepts cache behaviors whose `TargetOriginId` references an `OriginGroups.Items[].Id`, matching CloudFront origin-failover distributions generated by CDK.
- **Web UI** — the Event Stream page no longer crashes when SSE events include non-navigable sources such as service errors or inbox deliveries.

## [0.0.1-alpha.6] - 2026-07-14

### Changed

- **STS / SSM** — capability docs now list the unsupported operations for each service (STS SAML/OIDC/federation misc; SSM non-Parameter-Store operations), correcting stale manual matrices that referenced non-existent operations. No runtime behaviour change — these operations already returned `NotImplemented`.

### Fixed

- **ElastiCache / CloudFormation** — added Docker-backed `CreateServerlessCache`/`DescribeServerlessCaches`/`ModifyServerlessCache`/`DeleteServerlessCache` support, serverless cache pagination/metadata fields, and `AWS::ElastiCache::ServerlessCache` provisioning/update support with `ARN` and `Endpoint.*` attributes for CDK `CfnServerlessCache` stacks.
- **SQS** — `ReceiveMessage` now returns system attributes only when requested via `AttributeNames`/`MessageSystemAttributeNames` and user message attributes only when requested via `MessageAttributeNames` (honouring `All`, `.*`, and `prefix.*` selectors), matching AWS (previously all attributes were returned unconditionally); `VisibilityTimeout` on `ReceiveMessage`, `ChangeMessageVisibility`, and `ChangeMessageVisibilityBatch` now validates against AWS's 0–43200 range instead of silently accepting arbitrary values.

## [0.0.1-alpha.5] - 2026-07-13

### Fixed

- **EC2** — `CreateSubnet` now preserves explicit `AvailabilityZone` values in create and describe responses for multi-AZ VPC lookup scenarios.
- **Lambda** — fixed a data race while wiring VPC resolution during background Docker runtime initialization.
- **Release** — release publishing now runs the same race+coverage test command as the coverage workflow and explicitly gates publishing jobs on successful test/build dependencies.

## [0.0.1-alpha.4] - 2026-07-13

### Fixed

- **CloudFormation** — `DescribeChangeSet` and `ExecuteChangeSet` now accept ARN-only change set lookup, and `ListChangeSets` returns created change sets for the stack.

### Changed

- **Docs** — updated the release process to document the automated `VERSION`-driven release workflow.

## [0.0.1-alpha.2] - 2026-07-12

### Added

- **EC2** — virtual private gateways are metadata-backed (`CreateVpnGateway`, `AttachVpnGateway`, `DescribeVpnGateways`, `DetachVpnGateway`, `DeleteVpnGateway`), unblocking CDK `Vpc.fromLookup()` while also returning attached gateways when present.

## [0.0.1-alpha.1] - 2026-07-12

### Changed

- [docs] refreshed the generated service support index and alpha Docker image references for prerelease installation and verification.

### Fixed

- **EC2** — `CreateVpc` now creates the AWS-documented main route table, and EC2 Query errors return the EC2-specific error envelope expected by SDK clients such as aws-sdk-js v3.

## [0.0.1-alpha.0] - 2026-07-12

### Docker Images

- Full image with web management console: [`ghcr.io/neaox/overcast:0.0.1-alpha.0`](https://github.com/Neaox/overcast/pkgs/container/overcast) and `ghcr.io/neaox/overcast:alpha`
- Headless slim image for CI pipelines: [`ghcr.io/neaox/overcast-slim:0.0.1-alpha.0`](https://github.com/Neaox/overcast/pkgs/container/overcast-slim) and `ghcr.io/neaox/overcast-slim:alpha`

### Added

- **S3** — P1+P2 complete: bucket and object CRUD, `ListObjectsV2` with continuation-token pagination, multipart uploads (`CreateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`, `AbortMultipartUpload`, `ListParts`, `ListMultipartUploads`), `PutBucketNotificationConfiguration` with Lambda invocation on object events; object and bucket tagging (`PutObjectTagging`, `GetObjectTagging`, `DeleteObjectTagging`, `PutBucketTagging`, `GetBucketTagging`, `DeleteBucketTagging`); versioning status management (`GetBucketVersioning`, `PutBucketVersioning`); `ListObjectVersions` with prefix filtering and key-marker pagination (returns all objects as `VersionId=null` entries)
- **SQS** — P1+P2 complete; FIFO queues with `MessageGroupId` ordering, `MessageDeduplicationId` (5-minute window), content-based deduplication, and sequence numbers; dead-letter queues (`RedrivePolicy`, `ListDeadLetterSourceQueues`); queue tagging (`TagQueue`, `UntagQueue`, `ListQueueTags`); `ChangeMessageVisibilityBatch` (batch visibility timeout changes with per-entry success/failure); `ReceiveMessage` long polling (`WaitTimeSeconds` up to 20 s, returns early when a message arrives)
- **SNS** — topic lifecycle, subscription management, `Publish`/`PublishBatch` async fan-out to `sqs`, `email`, `email-json`, and `sms` subscribers (email and SMS captured in the Inbox); `GetSubscriptionAttributes`, `SetSubscriptionAttributes` (stores any attribute, including `FilterPolicy`); `ConfirmSubscription` (emulator auto-confirms); subscription message filtering via `FilterPolicy` (String/Number value matching); `Subscribe` rejects `application` (mobile push) and `firehose` protocols with `400 InvalidParameter`; web UI subscribe dialog lists all AWS SNS protocols with unsupported ones (lambda, application, firehose) disabled
- **DynamoDB** — table lifecycle (`CreateTable`, `DescribeTable`, `ListTables`, `DeleteTable`), item operations (`PutItem`, `GetItem`, `DeleteItem`, `UpdateItem`, `Scan`, `Query`, `BatchGetItem`, `BatchWriteItem`), GSI and LSI support (define at CreateTable, Query and Scan with `IndexName`), `StreamSpecification` on create and `UpdateTable`; `UpdateTable` supports BillingMode changes, `ProvisionedThroughput` updates, GSI create/delete/throughput-update, and AttributeDefinition updates; TTL support (`UpdateTimeToLive`, `DescribeTimeToLive`) with background expiry sweeper; transactions (`TransactWriteItems`, `TransactGetItems`) with ConditionExpression support and all-or-nothing semantics; Query supports `Limit` and `ExclusiveStartKey`/`LastEvaluatedKey` pagination and `ScanIndexForward`; Scan supports `Limit`/`ExclusiveStartKey` pagination and parallel scan (`Segment`/`TotalSegments`); `PutItem` and `DeleteItem` support `ReturnValues=ALL_OLD`; `DeleteItem` supports `ConditionExpression`; `UpdateItem` supports all `ReturnValues` variants (`ALL_OLD`, `ALL_NEW`, `UPDATED_OLD`, `UPDATED_NEW`); `Limit` is now correctly applied before `FilterExpression` (matching AWS semantics — `ScannedCount` reflects items read up to `Limit`, not items after filtering); `Select=COUNT` on Query and Scan returns only `Count`/`ScannedCount` without an `Items` array; `LastEvaluatedKey` for GSI Query/Scan now includes both table primary-key attributes and index key attributes; `CreateTable`/`UpdateTable` enable streams when `StreamViewType` is set without an explicit `StreamEnabled: true` (matches real AWS behaviour)
- **DynamoDB Streams** — `ListStreams`, `DescribeStream`, `GetShardIterator`, `GetRecords`; INSERT/MODIFY/REMOVE records with all image projection modes; `dynamodb:insert/modify/remove` bus events
- **Lambda** — function CRUD (`CreateFunction`, `GetFunction`, `GetFunctionConfiguration`, `UpdateFunctionCode`, `UpdateFunctionConfiguration`, `DeleteFunction`), synchronous `Invoke` with container-based execution via Docker (official AWS Lambda ECR images; falls back to stub runtime when Docker unavailable), `InvokeWithResponseStream` (AWS event stream binary encoding with initial-response → PayloadChunk → InvokeComplete events; RequestResponse only), container image support (`PackageType: Image` — pull and run user-provided images directly), versions (`PublishVersion`, `ListVersionsByFunction`), aliases (`CreateAlias`, `GetAlias`, `UpdateAlias`, `ListAliases`, `DeleteAlias`), layers (`PublishLayerVersion`, `GetLayerVersion`, `ListLayerVersions`, `ListLayers`, `DeleteLayerVersion`), event source mappings (`CreateEventSourceMapping`, `GetEventSourceMapping`, `UpdateEventSourceMapping`, `DeleteEventSourceMapping`, `ListEventSourceMappings`) with SQS and DynamoDB Streams sources; `UpdateFunctionConfiguration` accepts a `Layers` ARN list to attach layer versions to functions; VPC support (`VpcConfig` accepted on create/update, VpcId auto-resolved from subnets via EC2, Lambda containers connected to VPC Docker networks); auto-creates CloudWatch Logs log group on function creation; S3 event notification invocation
- **CloudWatch Logs** — `CreateLogGroup`, `DescribeLogGroups`, `DeleteLogGroup`, `CreateLogStream`, `DescribeLogStreams`, `DeleteLogStream`, `PutLogEvents`, `GetLogEvents`, `FilterLogEvents`; retention policy (`PutRetentionPolicy`, `DeleteRetentionPolicy`) with `retentionInDays` reflected in `DescribeLogGroups`; `FilterLogEvents` full AWS filter pattern syntax — JSON patterns (`{ $.field = "value" }` with comparison/logical operators, `EXISTS`, `IS NULL`), text OR patterns (`?ERROR ?WARN`), space-delimited patterns with glob/regex/numeric comparison, plus stream-level time-range skipping and binary-search optimization
- **SES** — v1 Query protocol and v2 REST-JSON; `SendEmail`, `SendRawEmail`, identity verification and listing; built-in mail capture; email templates (`CreateTemplate`, `GetTemplate`, `UpdateTemplate`, `ListTemplates`, `DeleteTemplate`, `SendTemplatedEmail`) with `{{key}}` variable substitution
- **Secrets Manager** — full secret lifecycle (`CreateSecret`, `GetSecretValue`, `DescribeSecret`, `PutSecretValue`, `UpdateSecret`, `ListSecrets`, `ListSecretVersionIds`, `DeleteSecret`, `BatchGetSecretValue`), tagging (`TagResource`, `UntagResource`), rotation config (`RotateSecret`, `CancelRotateSecret`), `GetRandomPassword` (configurable length and character exclusions); versioning with AWSCURRENT/AWSPREVIOUS staging labels; lookup by name or ARN
- **STS** — `GetCallerIdentity`, `GetSessionToken`, `GetFederationToken`, `AssumeRole`, `AssumeRoleWithWebIdentity`; returns fake temporary credentials (ASIA-prefixed access key, random secret + session token); uses AWS Query protocol
- **SSM Parameter Store** — `PutParameter` (String/SecureString/StringList, versioning, Overwrite), `GetParameter` (SecureString masked without `WithDecryption`), `GetParameters`, `GetParametersByPath` (recursive + non-recursive, pagination), `DescribeParameters` (Name BeginsWith filter), `GetParameterHistory`, `AddTagsToResource`, `ListTagsForResource`, `DeleteParameter`, `DeleteParameters`
- **KMS** — key lifecycle (`CreateKey`, `DescribeKey`, `ListKeys`, `EnableKey`, `DisableKey`, `ScheduleKeyDeletion`, `CancelKeyDeletion`), aliases (`CreateAlias`, `DeleteAlias`, `ListAliases`), symmetric AES-256-GCM crypto (`Encrypt`, `Decrypt`, `GenerateDataKey`, `GenerateDataKeyWithoutPlaintext`), asymmetric RSA-2048 signing (`Sign`, `Verify`); supports SYMMETRIC_DEFAULT and RSA_2048 key specs; fixed `CancelKeyDeletion` state transition (key state now correctly set to Disabled)
- **EventBridge Pipes** — `CreatePipe`, `DescribePipe`, `DeletePipe`, `ListPipes`; DynamoDB Streams → SQS delivery using Lambda ESM record format
- **EC2** — new service: `DeleteRoute` for removing routes from route tables; instance lifecycle (`RunInstances`, `DescribeInstances` with filters, `TerminateInstances`, `StartInstances`, `StopInstances` with async state transitions), VPC CRUD (`CreateVpc`, `DescribeVpcs`, `DeleteVpc`) backed by Docker bridge networks (one per VPC, CIDR→subnet mapping, `--internal` when no IGW attached); `ModifyVpcAttribute` (DNS metadata), subnet CRUD (`CreateSubnet`, `DescribeSubnets` with filters, `DeleteSubnet`), security group management (`CreateSecurityGroup`, `DeleteSecurityGroup`, `DescribeSecurityGroups` with filters, `AuthorizeSecurityGroupIngress`, `AuthorizeSecurityGroupEgress`, `RevokeSecurityGroupIngress`, `RevokeSecurityGroupEgress` with default egress allow-all), metadata queries (`DescribeRegions`, `DescribeAvailabilityZones`, `DescribeInstanceTypes`); CDK/IaC support: `DescribeImages` (synthetic AMI metadata), key pair CRUD (`CreateKeyPair`, `DescribeKeyPairs`, `DeleteKeyPair`), route table management (`CreateRouteTable`, `DescribeRouteTables`, `DeleteRouteTable`, `CreateRoute`, `AssociateRouteTable`, `DisassociateRouteTable`), internet gateway lifecycle (`CreateInternetGateway`, `DescribeInternetGateways`, `DeleteInternetGateway`, `AttachInternetGateway`, `DetachInternetGateway`); VPC peering connections (`CreateVpcPeeringConnection`, `AcceptVpcPeeringConnection`, `DescribeVpcPeeringConnections`, `DeleteVpcPeeringConnection`) with `pending-acceptance`→`active`→`deleted` state machine; `DescribeDhcpOptions`; startup reconciliation syncs VPC state against Docker networks
- **ECS** — new service: cluster lifecycle (`CreateCluster`, `DescribeClusters`, `ListClusters`, `DeleteCluster`), task definitions (`RegisterTaskDefinition`, `DescribeTaskDefinition`, `ListTaskDefinitions`, `DeregisterTaskDefinition` with family:revision versioning), task execution (`RunTask`, `StopTask`, `DescribeTasks`, `ListTasks` with async PROVISIONING→RUNNING transitions; Docker-backed container execution when Docker is available, with image pull, port mappings, environment variables, `AWS_ENDPOINT_URL` injection, and automatic exit detection; falls back to metadata-only when Docker unavailable); service lifecycle (`CreateService`, `UpdateService`, `DeleteService`, `DescribeServices`, `ListServices` with metadata-only reconciler — adjusts RunningCount/PendingCount via internal RunTask/StopTask, deployment tracking with PRIMARY/ACTIVE, service events capped at 100); **Fargate support** — `RegisterTaskDefinition` validates FARGATE constraints (`networkMode=awsvpc` required, cpu+memory required with valid combos), `RunTask`/`CreateService` require `networkConfiguration` for FARGATE launch type with synthetic ENI attachment generation and `platformVersion` defaulting to `LATEST`; capacity providers (`CreateCapacityProvider`, `DescribeCapacityProviders`, `UpdateCapacityProvider`, `PutClusterCapacityProviders`) with `FARGATE` and `FARGATE_SPOT` built-ins seeded automatically; task sets (`CreateTaskSet`, `UpdateTaskSet`, `DeleteTaskSet`, `DescribeTaskSets`, `UpdateServicePrimaryTaskSet`) for CODE_DEPLOY/EXTERNAL deployment controllers with Scale-based `ComputedDesiredCount`
- **RDS** — new service: DB instance lifecycle (`CreateDBInstance`, `DescribeDBInstances`, `DeleteDBInstance` with async creating→available transitions; Docker-backed when available — starts real mysql/postgres/mariadb/aurora-mysql/aurora-postgresql containers with automatic port allocation from `RDS_PORT_BASE`), `StopDBInstance`/`StartDBInstance` (stop/start containers with proper state machine transitions), `ModifyDBInstance` (metadata updates: class, storage, engine version, multi-AZ), subnet group CRUD (`CreateDBSubnetGroup`, `DescribeDBSubnetGroups`, `DeleteDBSubnetGroup`), parameter group CRUD (`CreateDBParameterGroup`, `DescribeDBParameterGroups`, `DeleteDBParameterGroup`), engine metadata (`DescribeDBEngineVersions`, `DescribeOrderableDBInstanceOptions` with mysql/postgres/mariadb/aurora-mysql/aurora-postgresql support); Aurora cluster lifecycle (`CreateDBCluster`, `DescribeDBClusters`, `DeleteDBCluster`, `ModifyDBCluster`, `StartDBCluster`, `StopDBCluster`) — aurora-mysql uses MySQL Docker image, aurora-postgresql uses PostgreSQL Docker image (same wire protocol); `CreateDBInstance` accepts `DBClusterIdentifier` to link to an Aurora cluster; web UI with instance list and detail pages
- **CloudFormation** — Stack and change set lifecycle with async provisioner; ~40 resource type handlers wired to real service implementations: EC2/VPC (VPC, Subnet, SecurityGroup, InternetGateway, VPCGatewayAttachment, RouteTable, Route, SubnetRouteTableAssociation, EIP, NatGateway), API Gateway (RestApi, Resource, Method, Deployment, Stage, V2 Api/Stage/Integration/Route), ECS (Cluster, TaskDefinition, Service), IAM (Policy, ManagedPolicy, InstanceProfile, ServiceLinkedRole), KMS (Key, Alias), EventBridge (EventBus, Rule), Step Functions (StateMachine), Lambda (Function, EventSourceMapping, LayerVersion), DynamoDB Table (with `StreamSpecification` forwarded so `StreamArn` GetAtt resolves correctly for Lambda ESM bindings), S3 BucketPolicy, Logs (LogGroup, LogStream), SSM Parameter, Secrets Manager Secret, SNS Topic/Subscription, SQS Queue; `Fn::GetAtt` returns real attribute values from service responses; physical IDs match AWS format (vpc-xxx, sg-xxx, etc.); `AWS::SQS::Queue` `Ref` now resolves to the queue URL for CDK `queue.queueUrl` outputs and `FifoQueue` creates `.fifo` generated names with FIFO attributes; `cdk deploy` works for stacks using supported resource types; CDK/CloudFormation discovery calls accepted so deployments proceed without errors; `AWS::ServiceCatalogAppRegistry::Application` and `AWS::ServiceCatalogAppRegistry::ResourceAssociation` resource types; auto-association of resources tagged with CDK's `awsApplication=<app-arn>` tag; `ListExports`/`ListImports` endpoints; `Fn::ImportValue` intrinsic for cross-stack references; custom resource invocation (`Custom::*` and `AWS::CloudFormation::CustomResource` invoke Lambda via CloudFormation protocol, graceful stub fallback without Docker); nested stacks (`AWS::CloudFormation::Stack` with `TemplateURL` fetch, child provisioning, `Fn::GetAtt` on child outputs, cascade deletion); `AWS::Lambda::Function` now passes `LoggingConfig` through so custom log groups suppress the default `/aws/lambda/<name>` group
- **Stub services** — IAM, Step Functions, and Shield are now registered and return HTTP 501 for all operations
- **CloudWatch** — new service: alarm lifecycle (`PutMetricAlarm`, `DescribeAlarms`, `DeleteAlarms`, `DescribeAlarmsForMetric`, `SetAlarmState`), metrics (`PutMetricData`, `ListMetrics`), tagging (`ListTagsForResource`, `TagResource`, `UntagResource`); AWS Query protocol with XML responses
- **ACM** — new service: certificate lifecycle (`RequestCertificate`, `DescribeCertificate`, `ListCertificates`, `DeleteCertificate`), tagging (`ListTagsForCertificate`, `AddTagsToCertificate`, `RemoveTagsFromCertificate`); certificates auto-ISSUED immediately
- **OpenSearch** — new service: domain CRUD (`CreateDomain`, `DescribeDomain`, `ListDomainNames`, `DeleteDomain`, `DescribeDomains`), tagging (`AddTags`, `ListTags`, `RemoveTags`); REST JSON at `/_opensearch/*`
- **AppConfig** — new service: application/environment/configuration-profile CRUD (12 ops); REST JSON at `/_appconfig/*`
- **Bedrock Runtime** — new service: `InvokeModel`, `Converse` returning canned text responses; REST JSON at `/_bedrock/*`
- **Glue Data Catalog** — new service: database CRUD (`CreateDatabase`, `GetDatabase`, `GetDatabases`, `DeleteDatabase`), table CRUD (`CreateTable`, `GetTable`, `GetTables`, `DeleteTable`); JSON 1.1 via `AWSGlue.*` target
- **Data Firehose** — new service: delivery stream lifecycle (`CreateDeliveryStream`, `DescribeDeliveryStream`, `ListDeliveryStreams`, `DeleteDeliveryStream`), record ingestion (`PutRecord`, `PutRecordBatch` — accepted, silently discarded); JSON 1.1 via `Firehose_20150804.*` target
- **Athena** — new service: query execution (`StartQueryExecution`, `GetQueryExecution`, `GetQueryResults`, `ListQueryExecutions` — queries succeed immediately with empty results), workgroup CRUD (`CreateWorkGroup`, `GetWorkGroup`, `ListWorkGroups`, `DeleteWorkGroup`); JSON 1.1 via `AmazonAthena.*` target
- **WAF v2** — `CreateWebACL`, `GetWebACL`, `ListWebACLs`, `DeleteWebACL`; all other operations still return 501
- **Cognito** — full User Pools implementation: pool lifecycle, pool client lifecycle, admin user management, self-service auth flows; USER_PASSWORD_AUTH, REFRESH_TOKEN_AUTH, NEW_PASSWORD_REQUIRED challenge, and SOFTWARE_TOKEN_MFA challenge; RS256-signed JWT access and ID tokens with per-pool JWKS discovery endpoint (`/{region}/{poolId}/.well-known/jwks.json`); **AWS-fidelity token claims** — UUID `sub` on all users (auto-assigned on creation, backward-compatible migration on load), `origin_jti`/`event_id`/`cognito:groups` claims on access tokens, `event_id` on ID tokens, `scope` is `aws.cognito.signin.user.admin`, `jti` in UUID format, `email_verified`/`phone_number_verified` emitted as booleans in ID tokens, `origin_jti` preserved through refresh token flows; `ConfirmSignUp`/`AdminConfirmSignUp` auto-set `email_verified` when user has email attribute; `UserLastModifiedDate` tracked on user mutations; async email delivery with timeout-bounded goroutines and `sync.WaitGroup` shutdown (no goroutine leaks); configurable token lifetimes (`AccessTokenValidity`, `IdTokenValidity`, `RefreshTokenValidity`, `TokenValidityUnits`) on `CreateUserPoolClient` and `UpdateUserPoolClient` — issued tokens respect configured durations; TOTP MFA enrollment and verification (`AssociateSoftwareToken`, `VerifySoftwareToken`, `SetUserMFAPreference`, `AdminSetUserMFAPreference`); group management (`CreateGroup`, `GetGroup`, `DeleteGroup`, `UpdateGroup`, `ListGroups`, `AdminAddUserToGroup`, `AdminRemoveUserFromGroup`, `AdminListGroupsForUser`, `ListUsersInGroup`); **user attribute management** — `UpdateUserAttributes` (self-service), `DeleteUserAttributes` (self-service), `AdminDeleteUserAttributes`; `GetUser` now returns full profile (`UserCreateDate`, `UserLastModifiedDate`); token revocation via JTI store and `GlobalSignOut`; bcrypt password hashing; email delivery via configured SMTP server (built-in mock by default); per-pool email templates (`VerificationMessageTemplate`, `AdminCreateUserConfig.InviteMessageTemplate`, `EmailConfiguration`) with `{username}` and `{####}` placeholder substitution — configurable on `CreateUserPool` and `UpdateUserPool`, returned by `DescribeUserPool`
- **CloudFront** — distribution CRUD (`CreateDistribution`, `GetDistribution`, `GetDistributionConfig`, `UpdateDistribution`, `DeleteDistribution`, `ListDistributions`) with ETag-based optimistic concurrency, CallerReference idempotency, disable-before-delete enforcement; invalidations (`CreateInvalidation`, `GetInvalidation`, `ListInvalidations`) with instant "Completed" status; tagging (`TagResource`, `UntagResource`, `ListTagsForResource`) with merge/remove semantics; `CreateDistributionWithTags` with `_custom_id_` tag support for deterministic IDs; origin access control CRUD (`CreateOriginAccessControl`, `GetOriginAccessControl`, `UpdateOriginAccessControl`, `DeleteOriginAccessControl`, `ListOriginAccessControls`) with ETag concurrency; cache policy CRUD, origin request policy CRUD, response headers policy CRUD (all with ETag concurrency and Marker/MaxItems pagination); legacy origin access identity CRUD with CallerReference validation and synthetic S3CanonicalUserId; origin proxy (`/_cloudfront/{distId}/*`) forwarding requests through configured origins with S3 rewriting, path pattern matching, DefaultRootObject handling, and CloudFront response headers; key group CRUD (`CreateKeyGroup`, `GetKeyGroup`, `GetKeyGroupConfig`, `UpdateKeyGroup`, `DeleteKeyGroup`, `ListKeyGroups`); public key CRUD (`CreatePublicKey`, `GetPublicKey`, `GetPublicKeyConfig`, `UpdatePublicKey`, `DeletePublicKey`, `ListPublicKeys`) with CallerReference dedup; CloudFront Functions (`CreateFunction`, `DescribeFunction`, `GetFunction`, `UpdateFunction`, `DeleteFunction`, `ListFunctions`, `PublishFunction`, `TestFunction`) with DEVELOPMENT→LIVE stage promotion and mock test execution; monitoring subscriptions (`CreateMonitoringSubscription`, `GetMonitoringSubscription`, `DeleteMonitoringSubscription`) per-distribution; realtime log configs (`CreateRealtimeLogConfig`, `GetRealtimeLogConfig`, `UpdateRealtimeLogConfig`, `DeleteRealtimeLogConfig`, `ListRealtimeLogConfigs`) with name-based lookup and ARN generation; field-level encryption config and profile CRUD (12 ops) with ETag concurrency; continuous deployment policy CRUD (6 ops) with ETag concurrency; web UI with distribution list/detail pages, origin viewer, invalidation management, and topology map integration
- **AppSync** — expanded from stub to config-tier emulation: GraphQL API CRUD with enriched response (URIs, DNS, defaults, complex config passthrough); schema management (`StartSchemaCreation`, `GetSchemaCreationStatus`, `GetIntrospectionSchema`); API key CRUD with `da2-` prefixed key generation; data source CRUD with ARN generation and backend config passthrough; function CRUD (pipeline resolver functions); resolver CRUD (UNIT/PIPELINE) with type-scoped routing; tagging (`TagResource`, `UntagResource`, `ListTagsForResource`); environment variables (`PutGraphqlApiEnvironmentVariables`, `GetGraphqlApiEnvironmentVariables`) with validation (max 50, key 2-64 chars, value ≤512 chars); domain name CRUD (`CreateDomainName`, `GetDomainName`, `ListDomainNames`, `UpdateDomainName`, `DeleteDomainName`) with synthetic DNS; API associations (`AssociateApi`, `GetApiAssociation`, `DisassociateApi`); API cache CRUD (`CreateApiCache`, `GetApiCache`, `UpdateApiCache`, `DeleteApiCache`, `FlushApiCache`); cascade delete (deleting API removes all children, deleting domain removes association); GraphQL query execution (`POST /_appsync/{apiId}/graphql`) with SDL schema validation (gqlparser), full authentication (API_KEY with expiry, AMAZON_COGNITO_USER_POOLS with Bearer+JWT claims, OPENID_CONNECT with issuer, AWS_LAMBDA authorizer, AWS_IAM with SigV4 stub, multi-auth via additionalAuthenticationProviders with fallback chain), NONE data source resolution (requestMappingTemplate payload extraction), HTTP data source proxy (endpoint + resourcePath/method/headers/body from request template), operationName selection for multi-operation documents, mutation support; Types CRUD (`CreateType`, `GetType`, `ListTypes`, `UpdateType`, `DeleteType`) with schema-derived type introspection; `ListResolversByFunction` (pipeline function filtering); pipeline resolver execution (ordered function calls with data source dispatch); sub-field selection filtering; AWS_LAMBDA data source dispatch (synchronous invocation with AppSync resolver event format — arguments, source, info, headers); AMAZON_DYNAMODB data source dispatch (GetItem, PutItem, DeleteItem, Query, Scan, UpdateItem, BatchGetItem, BatchWriteItem, TransactGetItems, TransactWriteItems via DynamoDBInvoker internal bridge with argument substitution and DynamoDB JSON unwrapping); VTL mapping template evaluation — full Go interpreter with $context/$ctx, #set/#if/#elseif/#else/#foreach/#return directives, $util (toJson, parseJson, autoId, isNull, matches, error, validate), $util.time.*, $util.dynamodb.*, string/map/list methods, quiet $! references; `EvaluateMappingTemplate` API endpoint; APPSYNC_JS runtime (`goja` pure-Go JS engine) — UNIT and PIPELINE resolvers with `code` + `runtime` fields; `request(ctx)` → data source → `response(ctx)` execution flow; expanded `@aws-appsync/utils` module (util.autoId, util.toJson, util.parseJson, util.time.*, util.dynamodb.* with full DynamoDB type conversions, util.str for string ops, util.math for numeric ops, type checking, null coalescing, util.matches, util.validate); `runtime.earlyReturn` support; `ctx.stash` propagation across pipeline functions; `ctx.env` injection from EnvironmentVariables store; `EvaluateCode` API endpoint for interactive JS sandbox testing; real-time WebSocket subscriptions (`/_appsync/{apiId}/realtime`) — connection_init/connection_ack, start/start_ack, stop/complete, 30s keepalive; mutation-to-subscription fan-out (createFoo→onCreateFoo convention); identity propagation ($context.identity) through VTL and JS resolvers; GraphQL argument extraction and variable resolution; nested field resolution (child types with independent resolvers); system map topology integration (AppSync API nodes with auth type, data source/resolver counts; edges to Lambda functions and DynamoDB tables via data sources); web UI upgraded to Tier 1 with detail pages (data sources, resolvers, functions, API keys, schema tabs); comprehensive compat test suites (50 tests across 11 groups covering APIs, schemas, API keys, data sources, functions, resolvers, types, tags, environment variables, domains, and cache — node-js-sdk + CLI suites); GraphQL introspection: `__schema`, `__type`, `__typename` meta-fields with full fragment expansion (inline fragments and named fragment spreads); `IntrospectionConfig: DISABLED` enforcement; `GetIntrospectionSchema?format=JSON` returns standard introspection JSON; VTL evaluator extended with `$util.transform.toDynamoDBFilterExpression/toDynamoDBConditionExpression` (AppSync filter DSL → DynamoDB expression), `$util.http.encodeUrl/decodeUrl/copyHeaders`, `$util.str` (toLower/toUpper/toReplace/trim/isEmpty/beginsWith/endsWith/normalize), `$ctx.info.selectionSetGraphQL`; APPSYNC_JS runtime extended with `util.transform.toDynamoDBFilterExpression/toDynamoDBConditionExpression`, `util.http.encodeUrl/decodeUrl`, `util.str.trim/isEmpty/beginsWith/endsWith/padStart/padEnd` (plus real Unicode normalization), `ctx.info.selectionSetGraphQL`; fixed `transformESM` to handle multiple `export` keywords on a single line; DynamoDB batch and transact operations added: `BatchGetItem` (multi-table key lookup with response aggregation), `BatchWriteItem` (multi-table bulk put/delete), `TransactGetItems` (cross-table transactional read), `TransactWriteItems` (cross-table transactional put/delete/update/conditionCheck); error enrichment: `$util.error` errorType and data propagated to `extensions.errorType`/`extensions.data` in GraphQL errors (both VTL and APPSYNC_JS); field error path enrichment (`path: ["fieldName"]` on resolver errors); Merged APIs — `AssociateSourceGraphqlApi`, `AssociateMergedGraphqlApi`, `GetSourceApiAssociation`, `ListSourceApiAssociations`, `DisassociateSourceGraphqlApi`, `DisassociateMergedGraphqlApi`, `StartSchemaMerge` with automatic schema merge via gqlparser; Events API (v2) — `CreateApi`, `GetApi`, `ListApis`, `UpdateApi`, `DeleteApi` for Event APIs on `/v2/apis` coexisting with API Gateway v2 via SigV4 service-name dispatch; Channel Namespaces — `CreateChannelNamespace`, `GetChannelNamespace`, `ListChannelNamespaces`, `UpdateChannelNamespace`, `DeleteChannelNamespace` with cascade-delete on parent Event API removal
- **API Gateway** — full emulation: REST API v1 (`CreateRestApi`, `GetRestApi`, `GetRestApis`, `DeleteRestApi`, `UpdateRestApi`, resource CRUD, method CRUD, integration CRUD with AWS_PROXY/MOCK/HTTP_PROXY/HTTP/AWS types, method/integration responses, deployments, stages), `UpdateResource` (patch pathPart), `UpdateMethod` (patch authorizationType, authorizerId, apiKeyRequired), `UpdateIntegration` (patch type, uri, httpMethod, credentials, etc.); HTTP API v2 (`CreateApi`, `GetApi`, `GetApis`, `UpdateApi`, `DeleteApi`, route CRUD, integration CRUD, deployments, stages), `UpdateRoute` (patch routeKey, target, authorizationType), `UpdateIntegration` (patch integrationType, integrationUri, payloadFormatVersion), `UpdateStage` (patch description, autoDeploy, deploymentId, stageVariables); Lambda proxy execution engine (v1 proxy event format for REST APIs, v2 payload 2.0 for HTTP APIs), Lambda non-proxy execution (AWS type), HTTP_PROXY and HTTP integration types (outbound HTTP forwarding for v1 and v2), MOCK integration execution with response templates; stage variable substitution (`${stageVariables.name}`) in integration URIs; base64 response decoding for Lambda proxy responses; **`COGNITO_USER_POOLS` authorizer enforcement on REST v1 methods and `JWT` authorizer enforcement on HTTP v2 routes** (RS256 signature verification + expiry check + issuer/audience validation, 401 on missing/invalid token, 403 on pool/audience mismatch) wired to the Cognito emulator's per-pool signing key; Authorizer CRUD for REST v1 (TOKEN/REQUEST/COGNITO_USER_POOLS) and HTTP v2 (JWT/REQUEST); API Key CRUD with auto-generated key values; Usage Plan CRUD with key association (`CreateUsagePlanKey`, `GetUsagePlanKeys`, `DeleteUsagePlanKey`); Model CRUD (`CreateModel`, `GetModel`, `GetModels`, `DeleteModel`); Request Validator CRUD (`CreateRequestValidator`, `GetRequestValidators`, `DeleteRequestValidator`); GET and DELETE for method responses and integration responses; inert infrastructure metadata: REST v1 domain name CRUD (`CreateDomainName`, `GetDomainNames`, `DeleteDomainName`), base path mapping CRUD (`CreateBasePathMapping`, `GetBasePathMappings`), VPC link CRUD (`CreateVpcLink`, `GetVpcLinks`, `DeleteVpcLink`), resource tags (`TagResource`, `UntagResource`, `GetTags` on ARN paths with slashes); HTTP v2 domain name CRUD, VPC link CRUD, API mapping CRUD (`CreateApiMapping`, `GetApiMappings`), v2 tags (`TagResource`, `UntagResource`, `GetTags`); all unimplemented operations return 501; web UI enhanced with Authorizers tabs on REST and HTTP API detail pages, standalone API Keys and Usage Plans management pages
- **IAM** — added `ListAccessKeys` so IAM user cleanup can enumerate and delete access keys before removing a user
- **Kinesis Data Streams** — new service: stream lifecycle (`CreateStream`, `DeleteStream`, `DescribeStream`, `DescribeStreamSummary`, `ListStreams`), record put/get (`PutRecord`, `PutRecords`, `GetShardIterator`, `GetRecords`), shard management (`ListShards`, `SplitShard`, `MergeShards`), tagging (`AddTagsToStream`, `ListTagsForStream`, `RemoveTagsFromStream`), retention (`IncreaseStreamRetentionPeriod`, `DecreaseStreamRetentionPeriod`); hash-based partition key routing; opaque base64 shard iterators with TRIM_HORIZON/LATEST/AT_SEQUENCE_NUMBER/AFTER_SEQUENCE_NUMBER support
- **ECR** — new service: repository CRUD (`CreateRepository`, `DescribeRepositories`, `DeleteRepository`); registry metadata (`DescribeRegistry`); `GetAuthorizationToken` (synthetic `AWS:test` bearer token + proxy endpoint + 12h expiry); image metadata (`PutImage` with deterministic SHA256(manifest) digest generation, `DescribeImages`, `ListImages`, `BatchGetImage`, `BatchDeleteImage`, `DescribeImageScanFindings` with scanner-unavailable empty findings); repository policies (`SetRepositoryPolicy`, `GetRepositoryPolicy`, `DeleteRepositoryPolicy`); lifecycle policies (`PutLifecyclePolicy`, `GetLifecyclePolicy`, `DeleteLifecyclePolicy`); tagging (`TagResource`, `UntagResource`, `ListTagsForResource`); 20 operations via `AmazonEC2ContainerRegistry_V20150921.*` JSON 1.1 target; repository URI uses configured external base URL
- **ElastiCache** — new service: cache cluster lifecycle (`CreateCacheCluster`, `DescribeCacheClusters`, `DeleteCacheCluster`, `ModifyCacheCluster`) with Docker-backed Redis (`redis:7`) containers and async creating→available health-check transition; replication group CRUD (`CreateReplicationGroup`, `DescribeReplicationGroups`, `DeleteReplicationGroup`, `ModifyReplicationGroup`, metadata-only); subnet group CRUD (`CreateCacheSubnetGroup`, `DescribeCacheSubnetGroups`, `DeleteCacheSubnetGroup`); parameter group CRUD (`CreateCacheParameterGroup`, `DescribeCacheParameterGroups`, `DeleteCacheParameterGroup`); tagging (`AddTagsToResource`, `ListTagsForResource`, `RemoveTagsFromResource`); CloudFormation resource handlers for `AWS::ElastiCache::CacheCluster`, `AWS::ElastiCache::ReplicationGroup`, `AWS::ElastiCache::SubnetGroup`; topology map integration; web UI cluster list page; compat tests for all resource types
- **Router** — added `QueryVersionOwner` interface enabling Query-protocol stubs to be dispatched by API version string (e.g. `2010-05-08`) rather than requiring an exhaustive action list
- **Web UI** — management console covering all implemented services: S3, SQS, SNS, DynamoDB (with item editor and stream controls), Lambda (code editor, test/invoke, versions & aliases, monitor/log tail), CloudWatch Logs (groups, streams, events viewer, cross-stream search), SES (identity management), EventBridge Pipes, ECS (cluster list/detail with services tab, task detail page with container info and port copy-to-clipboard, task definition registry), EC2 (tabbed dashboard with state filter pills, instance detail page with overview grid and security rules, VPC detail page with subnets/route tables/IGWs/peering connections/security groups tabs, VPC and security group views, networking and tags tabs), RDS (DB instance list with start/stop, instance detail with engine-specific connection strings — CLI/JDBC/DSN copy-to-clipboard, logs tab), ElastiCache (cluster list with create/delete); topology map with ECS/EC2/RDS/ElastiCache/VPC icons and navigation; SSE-based real-time cache invalidation for EC2/ECS/RDS/ElastiCache mutations; Applications section (list + detail) and "This resource belongs to application X" banner on every resource detail page across all services
- **Infrastructure** — in-memory and SQLite state backends; host binding and HTTPS/TLS support; debug endpoints (`/_debug/*`); health endpoint (`/_health`); Docker/docker-compose; full test suite (unit + integration, GWT pattern, mock store); SQLite schema migration on `persistent`/`wal`/`hybrid` backends now runs in a background goroutine — startup-equivalent to `memory` (~40 ms wall) instead of paying the modernc/sqlite cold-init cost (200–340 ms) on the critical path; first DB-touching request blocks until the migration finishes; opt-in startup phase profiler via `OVERCAST_PROFILE_STARTUP=1`; `/_health` now includes a `storage` object showing the active backend and per-service overrides; `/_debug/health` and `/_debug/config` include `serviceStates`; dashboard footer shows storage mode with per-service override tooltip; `OVERCAST_HOSTNAME` env var for multi-container networking — SQS queue URLs, SNS unsubscribe links, RDS endpoints, and CloudFormation internal calls all respect the configured hostname instead of hardcoding `localhost`
- **Emulator-only endpoints** — `/_ecs/tasks/{arn}/logs/{container}`, `/_ecs/clusters/{cluster}/tasks`, `/_rds/instances/{id}/logs` for UI container log tailing and listing
- **Dashboard** — added service cards for KMS, SSM, STS, CloudWatch Logs, and EventBridge; docs buttons on all services with documentation; corrected tier badges (promoted IAM, CloudFormation, EC2, ECS, RDS, AppSync, Step Functions from Stub to Partial)
- **Web UI detail pages** — Cognito (user pool list, detail with users table), KMS (key list with status badges, detail with key policy/rotation), SSM (parameter list with type badges, detail with version history), STS (caller identity display), EventBridge (event bus list, detail with rules tab)
- **ECR** — new service: repository CRUD (`CreateRepository`, `DescribeRepositories`, `DeleteRepository`, `ListTagsForResource`, `TagResource`, `UntagResource`), image metadata (`PutImage`, `BatchGetImage`, `BatchDeleteImage`, `DescribeImages`, `DescribeImageScanFindings`), auth (`GetAuthorizationToken`), lifecycle/registry policies (`PutLifecyclePolicy`, `GetLifecyclePolicy`, `DeleteLifecyclePolicy`, `SetRepositoryPolicy`, `GetRepositoryPolicy`, `DeleteRepositoryPolicy`); 14 operations total
- **AppConfig** — extended with hosted configuration versions: `CreateHostedConfigurationVersion`, `GetHostedConfigurationVersion`, `ListHostedConfigurationVersions`, `DeleteHostedConfigurationVersion`; version counter per profile enables data-plane unchanged-detection
- **AppConfigData** — new service: runtime data plane for AppConfig; `StartConfigurationSession` (returns opaque poll token), `GetLatestConfiguration` (delivers config content on first call and after version changes; returns empty body when config is unchanged — matching AWS poll semantics); tokens rotate on every call; 2 operations total
- **Scheduler** — new service: EventBridge Scheduler REST-JSON API under `/_scheduler/*`; schedule group CRUD, schedule CRUD, tag operations, `rate(...)`/`at(...)`/AWS-style `cron(...)` expressions, default group auto-seeding, and clock-driven Lambda/SQS target dispatch
- **Security / protocol fidelity** — SigV4 validation now verifies header-signed and presigned requests when `OVERCAST_SIGV4_VALIDATE=true`, enforces the standard clock-skew window, and returns AWS-shaped JSON/XML signature errors instead of accepting every request
- **Documentation** — created endpoint support docs for IAM, CloudFormation, EventBridge, Step Functions, AppSync, and EventBridge Pipes; updated README and STATUS service tables with all 22 services
- **Docker** — two published images: `ghcr.io/neaox/overcast` (full, with web management console on port 8080) and `ghcr.io/neaox/overcast-slim` (headless, for CI pipelines); multi-stage Dockerfile with `go-builder`, `web-builder`, `slim`, and default (console) targets; multi-platform builds (linux/amd64 + linux/arm64); `VERSION` file as single source of truth for version injection via ldflags; `/usr/local/bin/awslocal` wrapper — thin `aws` CLI shim that auto-sets `--endpoint-url` to the local Overcast instance (LocalStack-compatible); removed Node.js from slim image (~91 MB → ~36 MB) — Lambda functions run in their own Docker containers, so the host `node` binary was dead weight; Node.js is now only installed in the console image (for the BFF server)
- **Release pipeline** — GitHub release publishing verifies the tag against `VERSION`, runs tests, attaches native Linux/macOS/Windows binaries with `SHA256SUMS`, publishes multi-platform Docker images to GHCR, and updates release notes from the changelog with Docker links and checksum details
- **CI** — PR checks expanded: web UI lint/typecheck/build, Docker build smoke test, Go 1.24 checks, and release binary cross-builds

### Fixed

- **S3** — `ListObjectsV2` now honors `start-after` and echoes `StartAfter` in the XML response, matching AWS pagination semantics.
- **Cognito** — username-attribute pools now resolve email/phone sign-in identifiers across admin, auth challenge, self-service, MFA, and group operations instead of requiring the generated UUID username; generated `Username` values now match the `sub` attribute and JWT subject for AWS-compatible identity lookup; `ConfirmSignUp` sessions now work with `InitiateAuth`/`AdminInitiateAuth` `USER_AUTH` immediate sign-in; choice auth now supports `PASSWORD`, `PASSWORD_SRP`, `WEB_AUTHN`, `EMAIL_OTP`, and `SMS_OTP` first factors through `InitiateAuth`/`AdminInitiateAuth` and challenge responses; `USER_SRP_AUTH` returns AWS-shaped `PASSWORD_VERIFIER` challenges; `CUSTOM_AUTH` now returns AWS-shaped `CUSTOM_CHALLENGE` and SRP-prelude challenges and completes challenge responses with tokens; device tracking now returns `NewDeviceMetadata`, supports `ConfirmDevice`, `GetDevice`, `ListDevices`, `UpdateDeviceStatus`, `ForgetDevice`, and admin device management operations, and completes remembered-device `DEVICE_SRP_AUTH` / `DEVICE_PASSWORD_VERIFIER` challenges; `SetUserPoolMfaConfig`, `GetUserPoolMfaConfig`, `StartWebAuthnRegistration`, and `CompleteWebAuthnRegistration` support AWS-shaped passkey configuration/registration; `ExplicitAuthFlows` is stored and validated, `ALLOW_USER_AUTH` is enforced for `USER_AUTH`, `ALLOW_CUSTOM_AUTH` is enforced for `CUSTOM_AUTH`, `SignInPolicy.AllowedFirstAuthFactors` gates `USER_AUTH` with value validation, and `UserAttributeUpdateSettings` now delays email/phone changes until `VerifyUserAttribute` unless admin requests set the corresponding `*_verified=true` flag; `GetUserAttributeVerificationCode` resends pending email/phone verification codes; `AdminCreateUser` temporary passwords now conform to pool password policies whether supplied by the caller or generated by the emulator; malformed persisted pool/client/user records are isolated so list/admin APIs return usable results or modeled not-found errors instead of service-wide internal errors.
- **API Gateway** — Lambda proxy responses now give `multiValueHeaders` precedence over duplicate `headers` entries, matching AWS REST/HTTP API proxy merge semantics; REST Lambda proxy request events now encode absent query/path parameter maps as JSON `null` instead of `{}`.
- **SQS** — `ReceiveMessage` long polling now treats request-context cancellation before or during an empty poll as an empty receive instead of surfacing a transient internal error; empty JSON receives omit the `Messages` member and invalid `MaxNumberOfMessages`/`WaitTimeSeconds` values are rejected with `InvalidParameterValue`; malformed persisted queue/message records are isolated from `ListQueues` and `ReceiveMessage` instead of producing service-wide internal errors.
- **AppSync** — `CreateGraphqlApi` now returns AWS-compatible HTTP 200 and rejects missing required fields plus invalid top-level enum/limit values with `BadRequestException`.
- **Lambda** — `CreateFunction` now rejects missing Zip code/runtime/handler, invalid package types, image requests with `Runtime`, invalid architectures, timeout > 900, and memory outside 128..32768 with AWS-shaped `InvalidParameterValueException` errors; Docker-backed Lambda starts are bounded with `LAMBDA_DOCKER_MAX_CONCURRENT_STARTS`, runtime INIT waits are separated from function timeout via `LAMBDA_INIT_TIMEOUT_SECONDS`, container cleanup now runs during the session, and Docker-backed user log capture now preserves equal-timestamp bursts, truncates oversized lines instead of stalling, retries CloudWatch writes, and uses Docker emit timestamps to reduce flakiness under parallel test load.

---

<!-- New releases are prepended above this line -->
<!-- Template:

## [x.y.z] - YYYY-MM-DD

### Added
- ...

### Changed
- ...

### Deprecated
- ...

### Removed
- ...

### Fixed
- ...

### Security
- ...

[x.y.z]: https://github.com/Neaox/overcast/compare/vA.B.C...vx.y.z
-->

[Unreleased]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.23...HEAD
[0.0.1-alpha.23]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.22...v0.0.1-alpha.23
[0.0.1-alpha.22]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.21...v0.0.1-alpha.22
[0.0.1-alpha.21]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.20...v0.0.1-alpha.21
[0.0.1-alpha.20]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.19...v0.0.1-alpha.20
[0.0.1-alpha.19]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.18...v0.0.1-alpha.19
[0.0.1-alpha.18]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.17...v0.0.1-alpha.18
[0.0.1-alpha.17]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.16...v0.0.1-alpha.17
[0.0.1-alpha.16]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.15...v0.0.1-alpha.16
[0.0.1-alpha.15]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.14...v0.0.1-alpha.15
[0.0.1-alpha.14]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.13...v0.0.1-alpha.14
[0.0.1-alpha.13]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.12...v0.0.1-alpha.13
[0.0.1-alpha.12]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.11...v0.0.1-alpha.12
[0.0.1-alpha.11]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.10...v0.0.1-alpha.11
[0.0.1-alpha.10]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.9...v0.0.1-alpha.10
[0.0.1-alpha.9]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.8...v0.0.1-alpha.9
[0.0.1-alpha.8]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.7...v0.0.1-alpha.8
[0.0.1-alpha.7]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.6...v0.0.1-alpha.7
[0.0.1-alpha.6]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.5...v0.0.1-alpha.6
[0.0.1-alpha.5]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.4...v0.0.1-alpha.5
[0.0.1-alpha.4]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.2...v0.0.1-alpha.4
[0.0.1-alpha.2]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.1...v0.0.1-alpha.2
[0.0.1-alpha.1]: https://github.com/Neaox/overcast/compare/v0.0.1-alpha.0...v0.0.1-alpha.1
[0.0.1-alpha.0]: https://github.com/Neaox/overcast/releases/tag/v0.0.1-alpha.0
