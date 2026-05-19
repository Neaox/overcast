# Plan: Competitive improvements — Floci / LocalStack analysis

Status: complete.

## Context

Floci (`hectorvent/floci`) is a Java/Quarkus-based AWS emulator that
launched after LocalStack's community edition sunset (March 2026). It
has 34 services, 1,850+ compat tests across 9 SDK/tool suites, a 72 MB
native Docker image (GraalVM), ~24 ms startup, and ~13 MiB idle memory.

Overcast has 37 services, a 36 MB slim Docker image (after removing
dead-weight Node.js), ~22 ms startup, and ~13 MiB idle memory. Overcast
has features Floci lacks: web management console, AppSync execution
engine, CloudFront emulation, real-time topology graph.

This plan covers concrete improvements inspired by the analysis,
ordered by impact-to-effort ratio (biggest wins, easiest first).

---

## ~~1. Document per-service storage overrides~~ (days: 0 — already done)

**Status:** ✅ complete.

`OVERCAST_STATE_<SERVICE>=<backend>` env vars already work. The
`NamespacedStore` in `internal/state/namespaced.go` routes per-namespace
to separate store instances with isolated data directories.

Example:

```bash
OVERCAST_STATE=memory \
OVERCAST_STATE_DYNAMODB=persistent \
OVERCAST_STATE_S3=hybrid \
  docker run ... overcast-slim
```

**Done:**

- Documented per-service overrides in `docs/README.md` (Persistence § Per-service storage overrides).
- `GET /_health` now includes a `storage` object with `default` and `serviceOverrides`.
- `GET /_debug/health` and `GET /_debug/config` now include `serviceStates`.
- Dashboard footer shows the active storage mode with a tooltip listing any overrides.
- Added `NamespacedStore` unit tests (`internal/state/namespaced_test.go`).
- Added integration tests: `TestHealth_includesStorageConfig` and `TestMixedBackend_isolatesPerServiceData`.

---

## ~~2. OVERCAST_HOSTNAME — fix multi-container URL rewriting~~ (~1 day)

**Status:** ✅ complete.

**Problem:** SQS `queueURL()` in `internal/services/sqs/handler.go`
hardcoded `http://localhost:<port>`. When Overcast runs in Docker
Compose alongside an app container, `QueueUrl` responses resolve to the
wrong host. Floci solves this with `FLOCI_HOSTNAME`.

**Design:**

1. Add `Hostname string` to `config.Config` (env: `OVERCAST_HOSTNAME`,
   default: empty → falls back to `localhost`).
2. Add a `config.ExternalBaseURL()` helper that returns
   `http://<Hostname or localhost>:<Port>`.
3. Replace all hardcoded `http://localhost:%d` patterns in service
   handlers with `cfg.ExternalBaseURL()`.

**Done:**

- Added `Hostname` field to `config.Config`, `ExternalHostname()`, and `ExternalBaseURL()` helpers.
- Replaced hardcoded localhost in SQS `queueURL()`, SNS `unsubscribeURL()`, CloudFormation `sqsQueueHandler.Delete`, RDS instance endpoints, and Aurora cluster endpoints.
- CloudFront internal proxy/logging left as `localhost` (server-to-self loopback calls).
- AppSync `.amazonaws.com` URIs left as-is (intentionally fake AWS-format endpoints).
- Added `OVERCAST_HOSTNAME` to `clearEnv` and 3 config unit tests (`TestLoad_hostname`, `TestLoad_hostnameWithTLS`, default assertions).
- Added `WithHostname` test helper and `TestCreateQueue_honorsHostname` integration test.
- Documented in `docs/README.md` config table + new "Multi-container networking" section with docker-compose example.
- Updated CHANGELOG infrastructure bullet.

---

## ~~3. CDK/Terraform-unblocking stub services~~ (~2–3 days)

**Status:** ✅ complete.

**Problem:** CDK and Terraform stacks commonly reference services that
Overcast doesn't register at all. When the SDK sends a request and gets
a connection refused or 404, the deploy fails. Floci unblocks these by
registering metadata-only stubs.

**Target services** (in priority order — each is mostly CRUD metadata):

| Service                | Protocol         | Why it unblocks stacks                                     | Effort                                             |
| ---------------------- | ---------------- | ---------------------------------------------------------- | -------------------------------------------------- |
| **CloudWatch Metrics** | Query + JSON 1.1 | `AWS::CloudWatch::Alarm` in virtually every CDK stack      | Small — similar to CloudWatch Logs                 |
| **OpenSearch**         | REST JSON        | `AWS::OpenSearch::Domain` in data-layer stacks             | Small — domain CRUD only                           |
| **AppConfig**          | REST JSON        | Feature flags in CDK apps                                  | Small — CRUD for apps/envs/profiles                |
| **Bedrock Runtime**    | REST JSON        | AI stacks call `Converse`/`InvokeModel`                    | Tiny — 2 stub endpoints returning canned responses |
| **Glue Data Catalog**  | JSON 1.1         | Data-layer stacks (`AWS::Glue::Database`, `Table`)         | Small — database/table CRUD                        |
| **Data Firehose**      | JSON 1.1         | Delivery streams in logging stacks                         | Small — stream CRUD + `PutRecord` (buffer to S3)   |
| **Athena**             | JSON 1.1         | Analytics stacks; mock mode — accept queries, return empty | Tiny — state machine for query lifecycle           |
| **ACM**                | JSON 1.1         | HTTPS stacks need cert resources                           | Small — cert issuance lifecycle                    |

**Done:**

All 8 services implemented as inert stubs with full CRUD:

- **CloudWatch** (`internal/services/cloudwatch/`) — Query protocol, 10 ops (alarms, metrics, tags)
- **ACM** (`internal/services/acm/`) — JSON 1.1, 7 ops (cert lifecycle + tags, auto-ISSUED)
- **OpenSearch** (`internal/services/opensearch/`) — REST JSON at `/_opensearch/*`, 8 ops (domain CRUD + tags)
- **AppConfig** (`internal/services/appconfig/`) — REST JSON at `/_appconfig/*`, 12 ops (apps/envs/profiles)
- **Bedrock Runtime** (`internal/services/bedrock/`) — REST JSON at `/_bedrock/*`, 2 ops (canned responses)
- **Glue Data Catalog** (`internal/services/glue/`) — JSON 1.1, 8 ops (database + table CRUD)
- **Data Firehose** (`internal/services/firehose/`) — JSON 1.1, 6 ops (stream CRUD + record ingest)
- **Athena** (`internal/services/athena/`) — JSON 1.1, 8 ops (queries auto-succeed + workgroup CRUD)

All registered in router.go, config.go, tiers.go. Integration tests for all 8 services (33 tests total). Documentation in `docs/services/`. Total: 37 services (including ElastiCache added in step 8).

---

## ~~4. Compat test expansion — Python + Go SDKs~~ (~3–4 days)

**Status:** ✅ complete.

The compat orchestrator now drives **11 suites**: `cdk`, `cli`,
`dotnet-sdk`, `go-sdk`, `java-sdk`, `node-js-sdk`, `pulumi`,
`python-sdk`, `rust-sdk`, `terraform`, `tofu`. All share a canonical
test registry (`compat/suites/registry.json`) so unimplemented tests
emit `skip` and the dashboard shows a consistent matrix.

Floci still has more raw test count in some SDKs (e.g. Java 889 vs ours),
but Overcast now matches or exceeds Floci on SDK breadth by adding
Pulumi and .NET — two ecosystems Floci does not test.

---

## ~~5. IAM operation depth~~ — 33 → 61 ops (~3–5 days)

**Status:** ✅ complete.

All high-value gaps are implemented. IAM now has 61 operations across all
categories with comprehensive integration test coverage (62 tests).

| Operation                                 | Status | Notes                                      |
| ----------------------------------------- | ------ | ------------------------------------------ |
| `ListAttachedRolePolicies`                | ✅     | Already present before this plan           |
| `ListRolePolicies`                        | ✅     | Already present                            |
| `AttachGroupPolicy` / `DetachGroupPolicy` | ✅     | Already present                            |
| `ListGroupsForUser`                       | ✅     | Already present                            |
| `GetRole` (trust policy doc)              | ✅     | `AssumeRolePolicyDocument` in response     |
| `CreateServiceLinkedRole`                 | ✅     | Already present                            |
| `SimulatePrincipalPolicy`                 | ✅     | Always returns `allowed` — no enforcement  |
| `TagRole` / `UntagRole` / `ListRoleTags`  | ✅     | Already present                            |
| `GetAccountAuthorizationDetails`          | ✅     | Returns all users, groups, roles, policies |

Additional operations added beyond the original plan: `UpdateAssumeRolePolicy`,
`ListInstanceProfilesForRole`, `ListAttachedGroupPolicies`, `ListAttachedUserPolicies`,
full group/user inline and managed policy operations, user/role tagging.

No enforcement engine — Floci also doesn't enforce.

---

## ~~6. ECS operation depth~~ — 17 → 40 ops (~3–5 days)

**Status:** ✅ complete.

ECS grew from the plan's stated 17 ops to 40 ops. The package already had 32 when
work began; 8 more were added in this session.

**All originally-listed gaps are now implemented:**

| Operation                                 | Status | Notes                                                |
| ----------------------------------------- | ------ | ---------------------------------------------------- |
| `RegisterTaskDefinition` revisions        | ✅     | Family:revision counter, family listing              |
| `UpdateService` (desired count, task def) | ✅     | New deployment created on task def change            |
| `DescribeServices` (multiple)             | ✅     | Batch by name or ARN, failures array                 |
| `ListTaskDefinitionFamilies`              | ✅     | Optional familyPrefix filter                         |
| `DeregisterTaskDefinition`                | ✅     | Marks INACTIVE                                       |
| `DescribeContainerInstances`              | ✅     | Metadata-only; ARN-keyed per cluster                 |
| `ListContainerInstances`                  | ✅     | Filter by cluster and optional status                |
| `DescribeCapacityProviders`               | ✅     | FARGATE/FARGATE_SPOT built-ins seeded automatically  |
| `CreateCapacityProvider`                  | ✅     | Rejects FARGATE\* prefix for custom providers        |
| `TagResource` / `UntagResource`           | ✅     | ARN-scoped tag storage                               |
| `ListAccountSettings`                     | ✅     | Hardcoded defaults; filter by name; overrides stored |
| `PutAccountSetting`                       | ✅     | Per-region override storage                          |

**Additional ops added beyond plan:** `RegisterContainerInstance`, `DeregisterContainerInstance`,
`PutAccountSettingDefault`, `DeleteAccountSetting`, `UpdateCluster`, `UpdateClusterSettings`,
`CreateTaskSet`, `UpdateTaskSet`, `DeleteTaskSet`, `DescribeTaskSets`, `UpdateServicePrimaryTaskSet`,
`UpdateCapacityProvider`, `PutClusterCapacityProviders`.

**Total: 40 implemented ops.** Integration tests: 8 new tests this session (account settings + container instances), full suite passes.

---

## ~~7. EC2 operation depth~~ — 43 → 64 ops (~2–3 days)

**Status:** ✅ complete.

The plan was based on a stale count of 43 — EC2 was already at 60 ops when work
began. All listed gaps were already implemented. Added 4 more this session:

| Operation                                  | Status | Notes                                                  |
| ------------------------------------------ | ------ | ------------------------------------------------------ |
| `DescribeAvailabilityZones`                | ✅     | Already present (3 AZs per region)                     |
| `DescribeRegions`                          | ✅     | Already present (8 hardcoded regions)                  |
| `DescribeImages`                           | ✅     | Already present (4 AMIs: AL2, Ubuntu, Windows, AL2023) |
| `DescribeNatGateways` / `CreateNatGateway` | ✅     | Already present                                        |
| `AllocateAddress` / `AssociateAddress`     | ✅     | Already present                                        |
| `DescribeNetworkInterfaces`                | ✅     | Already present                                        |
| `ModifyInstanceAttribute`                  | ✅     | Added this session — InstanceType.Value persisted      |
| `CreateVpcEndpoint`                        | ✅     | Added this session — metadata-only, Gateway/Interface  |
| `DescribeVpcEndpoints`                     | ✅     | Added this session — vpc-id and service-name filters   |
| `DeleteVpcEndpoints`                       | ✅     | Added this session — VpcEndpointId.N params            |

**Total: 64 implemented ops** (Floci: 61). Integration tests: 5 new tests, full suite passes.

---

## ~~8. ElastiCache service~~ (~5–7 days)

**Status:** ✅ complete.

**Implemented:**

Query-protocol controller (`internal/services/elasticache/`) with 18 operations across five groups:

| Group              | Operations                                                                                                          |
| ------------------ | ------------------------------------------------------------------------------------------------------------------- |
| Clusters           | `CreateCacheCluster`, `DescribeCacheClusters`, `DeleteCacheCluster`, `ModifyCacheCluster`                           |
| Replication groups | `CreateReplicationGroup`, `DescribeReplicationGroups`, `DeleteReplicationGroup`, `ModifyReplicationGroup`           |
| Subnet groups      | `CreateCacheSubnetGroup`, `DescribeCacheSubnetGroups`, `DeleteCacheSubnetGroup`                                     |
| Parameter groups   | `CreateCacheParameterGroup`, `DescribeCacheParameterGroups`, `DeleteCacheParameterGroup`, `DescribeCacheParameters` |
| Tagging            | `AddTagsToResource`, `ListTagsForResource`, `RemoveTagsFromResource`                                                |

**Docker container lifecycle (fully implemented):**

- `CreateCacheCluster` and `CreateReplicationGroup` both launch real containers. Status transitions to "available" once the TCP health check confirms the port is listening — not before.
- Supported engines: **redis** (`redis:6`, `redis:7`), **valkey** (`valkey/valkey:7`, `valkey/valkey:8`), **memcached** (`memcached:1.5`, `memcached:1.6`). Engine-appropriate default versions and ports are applied automatically.
- Container names: `overcast-elasticache-<id>` (clusters), `overcast-elasticache-rg-<id>` (replication groups). Both reuse existing containers on restart.
- `DeleteCacheCluster` and `DeleteReplicationGroup` use async cleanup (mark "deleting" → return → stop/remove container + release port).
- Untracked goroutine leak fixed: Docker start goroutines are tracked via `Handler.dockerWg`; `Stop()` waits for them before container teardown.
- Startup race fixed: metadata-only "available" transition only fires when Docker is unavailable; with Docker, the health check is authoritative.
- Reconciliation at startup corrects status drift for both clusters and replication groups.

**`DescribeCacheParameters`:** Returns a curated static parameter list (17 Redis/Valkey params, 7 Memcached params) based on the group's family. Supports `Source` filter and `MaxRecords`/`Marker` pagination.

Web UI dashboard card and nav entry present. Integration tests: 37 tests. Node.js SDK compat tests included.

**Service count:** 37 services total.

---

## ~~9. MSK (Kafka) service — real Redpanda containers~~ (~5–7 days)

**Status:** ✅ complete.

`internal/services/msk/` implements 13 ops (cluster CRUD,
configurations, tags, `GetBootstrapBrokers`, `ListKafkaVersions`) and
launches real `docker.redpanda.com/redpandadata/redpanda` containers on
`CreateCluster`. Cluster state reaches `ACTIVE` only after a TCP health
check against the bootstrap port succeeds. Integration tests in
`tests/integration/msk/msk_test.go`.

---

# Remaining gaps — 2026-04-20 re-analysis

This section captures gaps found in a fresh diff against Floci (35
services as of April 2026) and LocalStack (community tier). The
earlier items (1–9) covered the biggest feature-parity gaps; what
remains is a mix of newer Floci additions, LocalStack-community
services that CDK/Terraform users commonly need, and a couple of
Overcast quality gaps (SigV4 validation, WAL storage).

## ~~10. ECR — real OCI registry~~ (~3–4 days)

**Status:** ✅ complete.

Implemented so far:

- Shared `registry:2` container now lazy-starts on first ECR auth/repo call when Docker is available.
- Registry endpoint wiring now uses `OVERCAST_HOSTNAME` and dynamic host-port binding.
- ECR `GetAuthorizationToken` now returns the registry endpoint (not the API base URL) and a token coupled to registry htpasswd credentials.
- Added Docker-gated integration coverage for lazy startup, hostname-aware endpoint behavior, and token-to-registry auth coupling.
- Added Docker-gated `docker login` / `docker push` / ECR `ListImages` + `BatchGetImage` / `docker pull` round-trip coverage, with read-path reconciliation so pushed registry manifests appear in control-plane metadata.
- Added the core web/UI slice: ECR is now registered in `service-registry`, removed from the unsupported catalog, has repository list/detail routes with `ServiceDocsButton`, and participates in global search.
- Added backend topology support for ECR repository nodes plus container-image edges to Lambda image functions and ECS services derived from stored image URIs and task definitions.
- Added Docker-gated lifecycle regression coverage that proves the shared managed ECR registry container is removed on server shutdown (no leaked process/container across test teardown).
- Added a web regression harness in `web/` (Vitest + jsdom + Testing Library) and ECR-focused regression tests covering repository list/detail rendering, local-registry guidance visibility, docs action presence, and route head metadata.

Remaining for full completion:

- None for step 10. Topology acceptance coverage remains backed by router regressions (including repository URI propagation and normalized image-ref edge matching for Lambda/ECS consumers), and web ECR regressions now run in the new `web` test harness.

**Why it matters:** ECR is the #1 AWS service Overcast is missing
relative to Floci. CDK stacks that build and push container images
(Lambda container images, ECS task images, Copilot apps) cannot run
end-to-end without it. It's also a prerequisite for EKS (#11) to pull
images built by the developer.

**Design (mirror Floci's approach):**

- JSON controller: `AmazonEC2ContainerRegistry_V20150921.*` for the
  control plane (`CreateRepository`, `DescribeRepositories`,
  `DeleteRepository`, `PutImage`, `BatchGetImage`, `ListImages`,
  `GetAuthorizationToken`, `BatchDeleteImage`, `SetRepositoryPolicy`,
  `GetRepositoryPolicy`, tag ops).
- Launch a shared `registry:2` container once per process
  (Docker-gated, same lazy-init pattern as ElastiCache).
- Map each ECR repository to a registry namespace so `docker push
<hostname>/<account>/<repo>:<tag>` works from the host.
- `GetAuthorizationToken` returns a synthetic bearer token the registry
  accepts (configure `registry:2` with `auth: htpasswd` using a known
  dev credential).

**Gotchas / constraints:**

- **HTTPS expectation.** Docker clients reject plain-HTTP registries
  unless the daemon's `insecure-registries` config lists the hostname.
  Document this in the ECR docs page — don't try to terminate TLS in
  `registry:2` (self-signed certs break `docker push` too).
- **Repository URI shape.** AWS returns
  `<acct>.dkr.ecr.<region>.amazonaws.com/<repo>`. Return the real local
  endpoint (`localhost:<port>/<repo>` or
  `<OVERCAST_HOSTNAME>:<port>/<repo>` via #2's helper) so CDK/Copilot
  consumers can push. Honor `OVERCAST_HOSTNAME` — the registry port is
  reached from other containers, not just the host.
- **Auth token encoding.** `GetAuthorizationToken` returns
  `base64("AWS:<password>")` and the same password must be provisioned
  in the registry's htpasswd file at container start. Token expiry
  field is mandatory even if we don't enforce it.
- **Shared container lifecycle.** One `registry:2` per Overcast
  process, not per-repository. Track it in a `sync.Once`-guarded
  lazy-init method (per [CLAUDE.md](../../CLAUDE.md) startup rules) and
  tear down in `Stop()`. Image pulls block — prewarm on first request,
  not in `New()`.
- **No image scan emulation.** `DescribeImageScanFindings` should
  return an empty/"not scanned" response, not a stub that misleads.
- **Cross-container networking.** An ECS task created inside Overcast
  that needs to pull from ECR must resolve the registry — thread the
  registry endpoint through the task env via the same hostname helper.

**Execution plan (AGENTS.md + CONTRIBUTING.md aligned):**

1. **Write failing tests first (mandatory TDD gate).**
   - Add integration tests in `tests/integration/ecr/ecr_test.go` for:
     `CreateRepository`, `DescribeRepositories`, `DeleteRepository`,
     `GetAuthorizationToken`, `ListImages`, `BatchGetImage`,
     `BatchDeleteImage`, repository policy read/write, and tag ops.
   - Add a docker-gated test proving one shared registry container is
     lazy-started on first ECR call and not started in `New()`.
   - Add a hostname test proving repository URIs and auth proxy URLs use
     `OVERCAST_HOSTNAME` when set.

2. **Scaffold service package with standard structure.**
   - Create `internal/services/ecr/` with the usual split:
     `service.go` (routing/dispatch), `handler.go` (operation handling),
     `store.go` (state I/O + JSON), `types.go` (request/response models),
     `docker.go` (registry lifecycle).
   - Register ECR in router/config/tier metadata alongside existing
     service registrations.
   - Keep state access through `state.Store` only (no in-handler maps).

3. **Implement control plane operations with explicit validation.**
   - Start with repository lifecycle + listing + tagging.
   - Add repository policy operations (`SetRepositoryPolicy`,
     `GetRepositoryPolicy`) storing opaque JSON policy text.
   - Return `501` for unimplemented ECR operations using the service's
     normal JSON error path; do not return bare `404`.

4. **Implement shared registry runtime (single container per process).**
   - Add a `sync.Once` lazy-init path that starts `registry:2` only when
     an operation requires registry availability.
   - Generate htpasswd credentials once per process and mount a minimal
     registry config with auth enabled.
   - Track runtime goroutines and stop/cleanup in `Service.Stop()` using
     existing wait-group lifecycle patterns.

5. **Implement auth/token flow and image APIs.**
   - `GetAuthorizationToken`: return `base64("AWS:<password>")`, proxy
     endpoint, and required expiry field.
   - `PutImage`/`BatchGetImage`/`ListImages`/`BatchDeleteImage`: model
     ECR metadata in store (digest/tag/mediaType/size/timestamps) and
     keep control-plane data consistent with pushed artifacts.
   - Keep tag and digest lookup paths canonical to avoid duplication.

6. **Thread endpoint resolution through shared helpers (DRY).**
   - Reuse the existing external hostname/base-URL helper introduced by
     step #2 instead of reconstructing host/port logic in ECR.
   - Extract any reusable registry URL/token helpers into `serviceutil`
     only if used by more than one service.

7. **Docs + status updates in same change set.**
   - Add `docs/services/ecr.md` with implemented operations, explicit
     limitations (HTTP/insecure-registry requirement, no scan engine),
     and docker-compose networking notes.
   - Update both summary and detailed coverage tables in `STATUS.md`.
   - Add user-facing note in `CHANGELOG.md`.

8. **Verification and quality gates before merge.**
   - Run targeted tests first: ECR integration + new unit tests.
   - Run full checks required by AGENTS guardrails:
     `go build ./...`, `go vet ./...`, then project test/lint tasks.
   - Confirm no new warnings/errors in the workspace Problems list.

**Design choices to keep it clean, DRY, idiomatic, and performant:**

- **Clean boundaries:** protocol parsing in handler, persistence in
  store, container lifecycle in docker helper; avoid mixed abstraction
  levels in one function.
- **DRY path building:** one repository URI constructor and one token
  builder reused across operations/tests.
- **Idiomatic Go:** early returns, narrow interfaces, table-driven tests,
  explicit typed errors, no hidden globals.
- **Performance:** zero ECR startup overhead unless called; `sync.Once`
  for container init; avoid repeated JSON re-marshalling for hot list/get
  paths by storing parsed metadata structs in store payloads.

**Definition of done for step 10:**

- ECR service registered and callable through AWS SDK/CLI.
- `docker login` using `GetAuthorizationToken` works against the local
  registry endpoint when the Docker daemon allows the configured plain-HTTP local registry.
- `docker push` + ECR `ListImages`/`BatchGetImage` round-trip works.
- Service shutdown leaves no leaked registry container/goroutine.
- Docs/status/changelog updated, and all quality gates pass.

**Web UI track (must ship with step 10, not as follow-up):**

1. **Register ECR in the service catalog.**
   - Add ECR to `web/src/lib/service-registry.ts` with route, category,
     and description.
   - Remove ECR from `web/src/lib/unsupported-services.ts` so it no
     longer appears as unsupported.

2. **Add ECR home/list screen and detail view.**
   - Create a repositories home page (list + create/delete actions).
   - Add repository detail page showing tags/images and digest metadata.
   - Include `ServiceDocsButton` in the page header actions per
     CONTRIBUTING web UI standards.

3. **Add typed web client endpoints for ECR operations.**
   - Add ECR API client methods in the web service layer for repository
     CRUD, image listing/get/delete, and repository policy ops.
   - Keep query-key shape consistent with existing data loaders
     (`[baseUrl, region, service, ...]`) to avoid cache fragmentation.

4. **Wire global search contributor.**
   - Add `web/src/lib/search-contributors/ecr.ts` using
     `createSearchContributor` so repositories appear in global search.
   - Import contributor in `web/src/lib/search-contributors/index.ts`.

5. **Add UX affordances for local-registry constraints.**
   - Surface the exact `docker login` command (endpoint + token flow)
     and warn when insecure-registry host setup is likely missing.
   - Make hostname/port display derive from the same backend endpoint
     helper behavior (`OVERCAST_HOSTNAME`) to avoid user confusion.

6. **UI tests and acceptance checks.**
   - Add component/data tests for repository list rendering, empty state,
     and error state.
   - Add route-level test ensuring ECR pages render and docs button is
     present.
   - Verify ECR resources appear in global search results.

7. **Topology map integration (required by web UI methodology).**
   - Add an ECR node type to the topology graph so repositories are
     visible as first-class resources in stack context.
   - Add edges from ECR repositories to consumers (Lambda image
     functions and ECS task definitions/services) when references are
     present, so image dependency flow is legible.
   - Add graph-local, high-value actions only: copy image URI and
     navigate to repository details. Keep destructive actions (delete)
     on the service page to avoid accidental graph-side mutation.
   - Add a short-lived visual pulse for image push/update events so
     fast transitions are perceivable without altering AWS API timing.
   - Keep map state and list/detail state derived from the same source
     of truth so badge counts, node labels, and repository tables never
     disagree.

8. **Topology tests and consistency checks.**
   - Add topology transform tests that assert ECR nodes and dependency
     edges render from representative resource fixtures.
   - Add map interaction test(s) for the ECR node actions (copy URI,
     open details).
   - Add regression test ensuring event pulses are visual-only and do
     not change backend resource lifecycle/state.

**Web UI definition of done (step 10 complete only when all true):**

- ECR appears in sidebar/dashboard from `service-registry.ts`.
- ECR no longer appears in unsupported catalog.
- ECR home/detail pages are navigable and include `ServiceDocsButton`.
- Repositories are discoverable via global search.
- UI clearly communicates local docker login/push prerequisites.
- ECR repositories and consumers are visible and connected in topology
  map with consistent counts/state across node and detail surfaces.
- Topology interactions for ECR stay graph-local and safe, with
  transient visual feedback for push/update activity.

## ~~11. EKS — k3s-backed Kubernetes control plane~~ (~7–10 days)

**Status:** ✅ complete.

Implemented so far:

- Added EKS mock-mode controller APIs for cluster lifecycle: `CreateCluster`, `DescribeCluster`, `ListClusters`, `ListUpdates`, `UpdateClusterVersion`, `DescribeUpdate`, `DeleteCluster`.
- Added kubeconfig helper flow: `UpdateKubeconfig` (returns generated kubeconfig YAML for mock endpoint).
- Added nodegroup metadata APIs: `CreateNodegroup`, `UpdateNodegroupVersion`, `DescribeNodegroup`, `ListNodegroups`, `DeleteNodegroup`.
- `DescribeUpdate` now resolves recorded update IDs from both cluster and nodegroup version updates.
- Added mock-mode Fargate profile read APIs: `ListFargateProfiles` and `DescribeFargateProfile` (synthetic `default` profile).
- Upgraded Fargate to full lifecycle: `CreateFargateProfile`, `DeleteFargateProfile`; list/describe now query the store with synthetic default fallback.
- Added tag management: `ListTagsForResource`, `TagResource`, `UntagResource` (ARN-keyed, URL-encoded in path).
- Added EKS add-on lifecycle: `CreateAddon`, `DescribeAddon`, `ListAddons`, `DeleteAddon`.
- Added `DescribeAddonVersions` with synthetic version catalog for `vpc-cni`, `coredns`, `kube-proxy`, `aws-ebs-csi-driver`.
- Added `DescribeAddonConfiguration` with synthetic configuration schemas for core add-ons.
- Added `UpdateAddon` to update add-on version/configuration and record update metadata.
- Added `UpdateNodegroupConfig` to update nodegroup labels/scaling config and record update metadata.
- Added identity provider config APIs: `ListIdentityProviderConfigs`, `DescribeIdentityProviderConfig`, `UpdateIdentityProviderConfig`, `AssociateIdentityProviderConfig`, and `DisassociateIdentityProviderConfig` (store-backed OIDC metadata).
- Added access-entry APIs: `CreateAccessEntry`, `DescribeAccessEntry`, `UpdateAccessEntry`, `DeleteAccessEntry`, and store-backed `ListAccessEntries` per cluster.
- Added access-policy association APIs: `AssociateAccessPolicy`, `ListAssociatedAccessPolicies`, and `DisassociateAccessPolicy` for stored access entries.
- Added `ListAccessPolicies` with a synthetic managed EKS access policy catalog.
- Added `DescribeAccessPolicy` for synthetic managed EKS access policy detail lookups by name.
- Added `DescribeClusterVersions` with a synthetic Kubernetes version catalog.
- Fixed access-entry deletion to cascade-remove associated access policy bindings so recreated entries start clean.
- Added synthetic cluster insight APIs: `ListInsights` and `DescribeInsight`.
- Added `UpdateClusterConfig` to persist cluster logging configuration and record update metadata.
- Added `OVERCAST_EKS_MODE` config plumbing with validated `mock` default and `live` opt-in values to stage the future k3s-backed mode.
- Threaded `OVERCAST_EKS_MODE` into the EKS service boundary so live-mode EKS no longer silently behaves like mock mode: `CreateCluster` now persists a bootstrap record once Docker is wired, `DescribeCluster` returns that live bootstrap state, mock-created cluster data is still blocked in live mode, and `UpdateKubeconfig` now serves real kubeconfig once the runtime endpoint + CA are ready.
- Added EKS live-mode Docker scaffolding: `EKS_DOCKER_SOCKET` / `EKS_NETWORK` config, router supervisor wiring, and service `SetDocker` / `Stop` lifecycle hooks for the future k3s runtime.
- `CreateCluster` in `live` mode now distinguishes missing Docker wiring (`503 ServiceUnavailableException`) from the first bootstrap path: once Docker is wired it persists a `CREATING` cluster record with no mock endpoint or CA data instead of falling back to a generic `501`.
- Added service-local EKS live runtime bookkeeping so Docker-wired `CreateCluster` registers an owned runtime entry, `DeleteCluster` clears it, and `Stop` now performs best-effort container stop/remove cleanup for any owned live runtime IDs.
- Extended `docker.HostConfig` with `Privileged` and `Tmpfs` fields (required for k3s).
- Implemented `startLiveCluster`: when Docker is wired, `CreateCluster` now launches a `rancher/k3s:v{version}.3-k3s1` container (privileged, `/run`+`/var/run` tmpfs, EKS network, port 6443 mapped to a random host port) in a background goroutine; on success the live runtime registry is updated with the container ID. Docker failures are logged as warnings so the cluster stays in `CREATING` without breaking the API response.
- Added `pollK3sReady`: after the k3s container starts, a blocking poll (within the same goroutine, 2 s interval, 5 min timeout) polls `https://127.0.0.1:{hostPort}/readyz` with TLS skip-verify; once the API server responds 200 or 401 the cluster record is updated to `ACTIVE` with `endpoint = "https://{OVERCAST_HOSTNAME-or-localhost}:{hostPort}"`. Polls that never succeed leave the cluster in `CREATING` and log a warning.
- Updated live-mode port binding to publish API port 6443 on `0.0.0.0` so the external hostname endpoint is reachable from sibling containers.
- Implemented live-mode kubeconfig parity: CA data is extracted from `/etc/rancher/k3s/k3s.yaml` in the running k3s container and persisted on the cluster record; `UpdateKubeconfig` now returns kubeconfig in live mode once cluster status is `ACTIVE` with endpoint + CA present (returns `503` until ready).
- Hardened live-mode kubeconfig readiness: when an `ACTIVE` cluster has endpoint data but missing CA, `UpdateKubeconfig` now attempts on-demand CA backfill from the live runtime container before failing with `503`, avoiding sticky "ready-but-no-CA" loops.
- Hardened live startup conflict handling: if `CreateCluster` hits a managed k3s container-name conflict and the existing container is stopped, the service now starts and reuses that container instead of leaving the cluster bootstrap stuck.
- Hardened live-mode visibility boundaries: `ListClusters` now filters out mock-record cluster entries while `OVERCAST_EKS_MODE=live` is enabled, matching the existing `DescribeCluster` mixed-mode block.
- Expanded mixed-mode guard coverage in live mode: cluster-scoped update/insight/config endpoints now reject mock-record clusters with `501`, preventing cross-mode mutations and reads beyond describe/list paths.
- Fixed a guard gap in `UpdateClusterConfig`: live mode now correctly rejects legacy mock-record clusters with `501` there as well, with regression coverage.
- Continued mixed-mode guard rollout: nodegroup endpoints now share the same live-mode mock-record rejection path via a common cluster accessibility helper, reducing duplicated checks and keeping behavior uniform.
- Continued mixed-mode guard rollout: access-entry and access-policy association endpoints now use the same cluster accessibility helper, enforcing consistent live-mode rejection for legacy mock records.
- Continued mixed-mode guard rollout: identity-provider-config and pod-identity-association endpoints now use the same cluster accessibility helper, extending consistent live-mode rejection of legacy mock records.
- Completed mixed-mode guard rollout across remaining cluster-scoped resources: fargate-profile and add-on endpoints now also use the shared cluster accessibility helper for uniform live-mode rejection of legacy mock records.
- Added targeted regressions for cluster-handler guard consistency after helper centralization: `DescribeCluster`, `ListInsights`, and `DescribeUpdate` now have explicit live-mode legacy mock-record `501` coverage.
- Extended cluster-handler mixed-mode regression coverage to `DescribeInsight` and `UpdateKubeconfig`, ensuring live mode continues to reject legacy mock-record clusters on those paths with `501`.
- Added a deletion hygiene regression that verifies `DeleteCluster` removes cluster-scoped artifacts across nodegroups/updates/fargate/addons/idp/access/pod-identity namespaces and related tag records, while preserving data for other clusters.
- Added an explicit live-mode cleanup contract regression for legacy mock records: `DeleteCluster` remains allowed (returns `200`) so mixed-mode leftovers can be removed even while read/update APIs remain blocked with `501`.
- Added integration coverage for the same contract using shared store across mock/live servers: a cluster created in mock mode can be deleted through the live-mode API and is confirmed removed from underlying state (not merely hidden by live-mode listing/filtering).
- Added shared-store integration coverage for live-mode visibility filtering: clusters created in mock mode remain listable in mock mode but are hidden from `ListClusters` in live mode.
- Added shared-store integration coverage for cluster mutation guardrails: `UpdateClusterConfig` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for read-path guardrails: `ListUpdates` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for insights read guardrails: `ListInsights` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for update read guardrails: `DescribeUpdate` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for cluster version mutation guardrails: `UpdateClusterVersion` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for insights read detail guardrails: `DescribeInsight` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for nodegroup read guardrails: `ListNodegroups` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for access-entry read guardrails: `ListAccessEntries` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for identity-provider read guardrails: `ListIdentityProviderConfigs` on a mock-created cluster now explicitly verifies `501` in live mode.
- Fixed cluster deletion to clear cluster-scoped metadata (nodegroups, updates, fargate profiles, addons, idp configs, access entries, access-policy bindings) so cluster recreation starts clean.
- Fixed cluster deletion to clear cluster ARN tags and normalized tag API ARN path decoding to prevent encoded/decoded key mismatches.
- Fixed cluster deletion to clear child-resource ARN tags (nodegroups, fargate profiles, addons) so child tags do not leak across cluster recreate.
- Added regression coverage confirming cluster update history is cleared across delete/recreate.
- Added regression coverage confirming cluster delete/recreate clears add-on and fargate profile metadata.
- Fixed nodegroup deletion to clear nodegroup ARN tags so nodegroup recreate starts without stale tag state.
- Fixed fargate profile and add-on deletion to clear their ARN tags so recreate flows start without stale tag state.
- Added regression coverage confirming cluster delete/recreate clears identity provider config metadata.
- Added pod identity association APIs: `CreatePodIdentityAssociation`, `ListPodIdentityAssociations`, `DescribePodIdentityAssociation`, `UpdatePodIdentityAssociation`, and `DeletePodIdentityAssociation` (store-backed).
- Added regression coverage confirming cluster delete/recreate clears pod identity associations.
- Hardened `CreatePodIdentityAssociation` to reject duplicate namespace/service-account bindings with a conflict response.
- Added shared-store integration coverage for fargate-profile read guardrails: `ListFargateProfiles` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for add-on read guardrails: `ListAddons` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for nodegroup config mutation guardrails: `UpdateNodegroupConfig` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for nodegroup version mutation guardrails: `UpdateNodegroupVersion` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for add-on creation mutation guardrails: `CreateAddon` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for add-on update mutation guardrails: `UpdateAddon` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for add-on deletion mutation guardrails: `DeleteAddon` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for fargate-profile creation mutation guardrails: `CreateFargateProfile` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for fargate-profile deletion mutation guardrails: `DeleteFargateProfile` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for access-entry creation mutation guardrails: `CreateAccessEntry` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for access-entry update mutation guardrails: `UpdateAccessEntry` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for access-entry deletion mutation guardrails: `DeleteAccessEntry` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for access-policy association mutation guardrails: `AssociateAccessPolicy` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for access-policy read guardrails: `ListAssociatedAccessPolicies` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for access-policy disassociation mutation guardrails: `DisassociateAccessPolicy` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for identity-provider association mutation guardrails: `AssociateIdentityProviderConfig` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for identity-provider update mutation guardrails: `UpdateIdentityProviderConfig` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for identity-provider disassociation mutation guardrails: `DisassociateIdentityProviderConfig` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for pod-identity read guardrails: `ListPodIdentityAssociations` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for pod-identity creation mutation guardrails: `CreatePodIdentityAssociation` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for pod-identity update mutation guardrails: `UpdatePodIdentityAssociation` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for pod-identity deletion mutation guardrails: `DeletePodIdentityAssociation` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for access-entry detail read guardrails: `DescribeAccessEntry` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for identity-provider detail read guardrails: `DescribeIdentityProviderConfig` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for pod-identity detail read guardrails: `DescribePodIdentityAssociation` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for nodegroup creation mutation guardrails: `CreateNodegroup` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for nodegroup detail read guardrails: `DescribeNodegroup` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for nodegroup deletion mutation guardrails: `DeleteNodegroup` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for fargate-profile detail read guardrails: `DescribeFargateProfile` on a mock-created cluster now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for add-on detail read guardrails: `DescribeAddon` on a mock-created cluster now explicitly verifies `501` in live mode.
- Closed a mixed-mode bypass in tag handlers: EKS resource ARNs (`cluster`, `nodegroup`, `fargateprofile`, `addon`, `podidentityassociation`) now resolve back to cluster scope and enforce the same live-mode legacy-mock `501` accessibility guard before list/tag/untag operations.
- Added shared-store integration coverage for tag read guardrails: `ListTagsForResource` on a mock-created cluster ARN now explicitly verifies `501` in live mode.
- Added shared-store integration coverage for tag mutation guardrails: `TagResource` and `UntagResource` on a mock-created cluster ARN now explicitly verify `501` in live mode.
- Extended shared-store integration coverage for tag read guardrails to child-resource ARN paths: `ListTagsForResource` on a mock-created nodegroup ARN now explicitly verifies `501` in live mode.
- Extended shared-store integration coverage for tag mutation guardrails to child-resource ARN paths: `TagResource` and `UntagResource` on a mock-created nodegroup ARN now explicitly verify `501` in live mode.
- Extended shared-store integration coverage for tag guardrails across remaining EKS child-resource ARN families: add-on ARN reads, fargate-profile ARN mutations, and pod-identity-association ARN untag operations now explicitly verify `501` in live mode for mock-created cluster lineage.
- Completed shared-store integration guardrail matrix coverage for the remaining EKS child-resource ARN families (`addon`, `fargateprofile`, `podidentityassociation`): `ListTagsForResource`, `TagResource`, and `UntagResource` now each have explicit live-mode `501` regressions for legacy mock cluster lineage.
- Added unit coverage for the new EKS tag-resource guard implementation: ARN parsing across cluster/nodegroup/fargate/add-on/pod-identity resource forms is now explicitly tested, along with handler-level live-mode `501` blocking for legacy mock nodegroup tag access.
- Expanded unit coverage for the tag-resource guard implementation: handler-level tests now also lock in live-mode `501` blocking for legacy mock add-on ARN tag mutation and confirm non-EKS ARN tag reads continue to succeed.
- Expanded handler-level compatibility coverage for non-EKS tag APIs in live mode: the `TagResource` success path is now explicitly tested as well, confirming the guard logic stays scoped to EKS-linked ARNs rather than mutating behavior for unrelated resource types.
- Expanded unit coverage for the tag-resource guard implementation on the untag path as well: handler-level tests now lock in live-mode `501` blocking for legacy mock pod-identity-association ARN untag operations and confirm non-EKS ARN untag behavior continues to succeed with selective key removal.
- Added a live-mode compatibility regression for non-EKS ARNs on tag APIs: non-EKS resources continue to allow list/tag operations, confirming the guardrail applies only to EKS cluster-scoped ARNs.
- Extended non-EKS ARN compatibility coverage for tag APIs: `UntagResource` on non-EKS ARNs in live mode remains allowed and mutates only requested keys, confirming guardrails stay scoped to EKS cluster-linked ARNs.
- Added live-mode compatibility regressions for global EKS catalog endpoints: `ListAccessPolicies`, `DescribeAccessPolicy`, and `DescribeClusterVersions` continue to return successful responses while mixed-record cluster-scoped guardrails remain enforced.
- Extended global endpoint compatibility coverage in live mode: `DescribeAccessPolicy` for unknown names explicitly remains `404 ResourceNotFoundException`, confirming global catalog semantics are preserved while mixed-record cluster guardrails evolve.
- Tightened the baseline integration assertions for the access-policy catalog as well: unknown `DescribeAccessPolicy` lookups now explicitly require `ResourceNotFoundException` in the error body, matching the live-mode compatibility contract.
- Tightened the baseline integration assertions for access-policy association flows too: missing access-entry list requests, missing associated-policy disassociate requests, and disassociate requests against missing clusters now explicitly require `ResourceNotFoundException` in the error body instead of checking only the `404` status.
- Tightened the baseline integration assertions for access-entry CRUD flows as well: missing-cluster list/create/describe/delete requests and missing-entry describe/delete requests now explicitly require `ResourceNotFoundException` in the error body instead of checking only the `404` status.
- Extended that baseline access-entry CRUD error-body coverage to updates too: missing-entry and missing-cluster `UpdateAccessEntry` requests now explicitly require `ResourceNotFoundException` in the error body instead of checking only the `404` status.
- Tightened the baseline conflict-path assertions for access entry flows too: duplicate `CreateAccessEntry` and duplicate `AssociateAccessPolicy` requests now explicitly require `ResourceInUseException` in the error body instead of checking only the `409` status.
- Added baseline validation regressions for the access entry surface too: `CreateAccessEntry` requests missing `principalArn` and `AssociateAccessPolicy` requests missing `policyArn` now explicitly require `400 MissingParameter` instead of relying on status-only checks.
- Extended global endpoint compatibility coverage in live mode to the add-on catalog: `DescribeAddonVersions` and `DescribeAddonConfiguration` continue to return their normal success, empty-catalog, and `404 ResourceNotFoundException` responses for known and unknown add-on names while mixed-record cluster guardrails remain enforced.
- Refactored the growing EKS unit-test surface into focused live-mode cluster, live-mode child-resource, runtime, runtime-kubeconfig, and tag test files, then extracted a shared live-mode test helper for legacy mock-cluster setup so the mixed-record guard regressions stay easier to extend without re-copying service bootstrap boilerplate.
- Tightened the baseline integration assertions behind those add-on catalog compatibility checks: unknown add-on version lookups now explicitly require an empty `addons` catalog, and missing add-on configuration lookups now explicitly require `ResourceNotFoundException` in the error body.
- Extended the same exact-contract tightening to adjacent EKS identity and lifecycle cleanup paths: missing-cluster identity-provider list requests, missing identity-provider describes, and post-delete add-on/fargate-profile describes now explicitly require `ResourceNotFoundException` instead of relying on `404` status alone.
- Continued that exact-contract pass across neighboring EKS cluster and pod-identity paths: missing insight reads, missing-cluster insight lists, missing fargate-profile describes, missing pod-identity reads/updates, and duplicate pod-identity bindings now explicitly require their AWS-style `ResourceNotFoundException` or `ResourceInUseException` bodies instead of status-only checks.
- Closed the remaining delete-after-delete status-only gaps in the EKS integration suite too: describing a live-mode-cleaned legacy mock cluster and deleting an already-deleted nodegroup now explicitly require `ResourceNotFoundException` payloads instead of checking only `404`.
- Closed the last remaining deleted-cluster status-only assertion in the baseline EKS lifecycle coverage as well: the post-delete `DescribeCluster` path now explicitly requires `ResourceNotFoundException` instead of relying on `404` alone.
- Tightened the shared live-mode guardrail assertions across the EKS integration suite too: the mixed-record `501` matrix now verifies the structured `NotImplemented` error body, not just the status code, so cross-mode rejection contracts stay explicit while the guard surface continues to expand.
- Extended the same `501` contract tightening to the handler-level EKS unit suite: the shared live-mode mixed-record recorder helper now verifies the structured `NotImplemented` body across cluster, child-resource, and EKS-tag guard regressions instead of asserting status alone.
- Tightened the adjacent live-mode `503` assertions too: the Docker-required `CreateCluster` integration check and the handler-level kubeconfig-not-ready unit check now explicitly require `ServiceUnavailableException` bodies instead of relying on status alone.
- Regenerated the EKS capability docs from `capabilities_dev.go` so the generated service inventory now reflects the live-mode behavior notes, the supported `UpdateClusterConfig` endpoint, and the current supported-op counts in both `docs/services/eks.md` and `docs/generated/service-support.json`.
- Added a dev-only EKS capability inventory test that walks the registered REST routes and proves they stay in sync with `capabilities_dev.go`, closing the drift gap that had let the generated EKS support inventory fall behind the real route surface.
- Hardened live-mode cluster teardown across process restarts: when a persisted live EKS cluster is deleted after the in-memory runtime registry has been lost, `DeleteCluster` now reconciles the managed k3s container by deterministic name + Overcast ownership labels before issuing Docker stop/remove cleanup.
- Hardened live-mode kubeconfig recovery across process restarts too: when an `ACTIVE` live EKS cluster is missing persisted CA data and the in-memory runtime registry has been lost, `UpdateKubeconfig` now reconciles the managed k3s container by deterministic name + Overcast ownership labels before extracting and persisting the CA bundle.
- Hardened live-mode service shutdown across process restarts as well: `Stop()` now scans persisted live EKS clusters, reconciles any managed k3s containers by deterministic name + Overcast ownership labels, and then reuses the existing stop/remove cleanup path so restart-lost runtime bookkeeping does not leak containers on shutdown.
- Hardened live-mode cluster reads across process restarts too: when a persisted live EKS cluster is still recorded as `CREATING` (or otherwise missing endpoint/CA data) after runtime bookkeeping has been lost, `DescribeCluster` now performs a best-effort readiness reconciliation against the managed k3s container and promotes the cluster back to `ACTIVE` with the recovered endpoint and CA bundle once the API server is actually ready.
- Hardened live-mode kubeconfig readiness across process restarts as well: `UpdateKubeconfig` now reuses that same best-effort readiness reconciliation before returning `503`, so stale persisted live clusters can recover to a working kubeconfig as soon as the managed k3s API server is actually ready instead of staying stuck behind restart-lost bookkeeping.
- Hardened live-mode delete cleanup for bootstrap-timing runtime gaps too: when `DeleteCluster` sees a cached runtime entry whose container ID was never filled (for example, bookkeeping created before Docker start completed), it now reconciles by deterministic managed container name + ownership labels and still performs stop/remove cleanup instead of skipping cleanup entirely.
- Hardened live-mode shutdown cleanup for the same bootstrap-timing runtime gap: `Stop()` now treats cached runtime entries with blank container IDs as unresolved and reconciles them by deterministic managed container name + ownership labels before draining cleanup, preventing leaked managed k3s containers when bookkeeping exists but was never fully populated.
- Hardened live-mode cluster-read reconciliation for that same incomplete-runtime cache case: when `DescribeCluster` encounters a cached runtime entry with a blank container ID, it now still falls back to deterministic managed-container reconciliation by name + ownership labels so ready clusters are promoted to `ACTIVE` instead of remaining stuck on stale persisted state.
- Added matching restart-safe kubeconfig regression coverage for that incomplete-runtime cache case as well: `UpdateKubeconfig` is now explicitly tested to ensure cached runtime entries with blank container IDs still reconcile by managed container name + ownership labels and produce a ready kubeconfig once k3s is up.
- Hardened live-mode delete cleanup for stale non-empty runtime IDs too: when `DeleteCluster` finds a cached runtime entry with an out-of-date container ID, it now refreshes against deterministic managed-container reconciliation by name + ownership labels before stop/remove cleanup so recreated managed k3s containers are still torn down instead of leaking.
- Hardened live-mode shutdown cleanup for stale non-empty runtime IDs too: during `Stop()`, persisted live cluster reconciliation now refreshes runtime identity by deterministic managed-container name + ownership labels even when a non-empty cached container ID exists, so stale runtime-cache entries do not cause recreated managed k3s containers to be missed and leaked.
- Hardened live-mode ready-cluster reconciliation for stale non-empty runtime IDs too: when `DescribeCluster`/`UpdateKubeconfig` readiness reconciliation encounters a cached runtime ID that no longer inspects, it now retries deterministic managed-container reconciliation by name + ownership labels and continues readiness promotion from the refreshed runtime, preventing stale cache entries from leaving ready clusters stuck behind `503`.
- Added matching stale-runtime-ID regression coverage for the read path too: `DescribeCluster` is now explicitly tested to ensure stale non-empty cached runtime IDs trigger the same managed-name reconciliation fallback and still promote ready clusters to `ACTIVE` with recovered endpoint/CA data.
- Added matching stale-runtime-ID regression coverage for the CA-backfill path as well: `UpdateKubeconfig` is now explicitly tested to ensure ACTIVE clusters with missing persisted CA still recover kubeconfig CA data when cached runtime IDs are stale, by reconciling through deterministic managed-container lookup before extraction.
- Added matching blank-runtime-ID regression coverage for that same CA-backfill path too: `UpdateKubeconfig` is now explicitly tested to ensure ACTIVE clusters with missing persisted CA still reconcile by deterministic managed-container lookup when a cached runtime entry exists but its container ID is blank.
- Continued exact-contract tightening for remaining EKS `404` assertions by introducing a shared `expectResourceNotFound` helper that requires `404`, `__type=ResourceNotFoundException`, and a non-empty AWS-style `message`; baseline identity-provider and add-on configuration not-found integrations now use that stricter contract.
- Extended that stricter not-found helper adoption across additional baseline access flows too: missing-cluster access-entry list requests, disassociate-missing-policy requests, disassociate-on-missing-cluster requests, and unknown access-policy describes now all enforce `404`, `ResourceNotFoundException`, and a non-empty AWS-style `message`.
- Extended the same strict-helper adoption deeper into access-entry CRUD paths: create-on-missing-cluster, describe-missing-entry, describe-on-missing-cluster, and post-delete/missing delete checks now all enforce `404`, `ResourceNotFoundException`, and a non-empty AWS-style `message`.
- Completed strict-helper migration for remaining baseline access-path `404` checks: `UpdateAccessEntry` missing-entry/missing-cluster requests and `ListAssociatedAccessPolicies` on missing access entries now also enforce `404`, `ResourceNotFoundException`, and a non-empty AWS-style `message`.
- Extended strict-helper adoption into neighboring lifecycle paths as well: deleted fargate-profile/add-on describes and missing/deleted pod-identity association reads now also enforce `404`, `ResourceNotFoundException`, and a non-empty AWS-style `message`.
- Completed full exact-contract migration across all EKS integration test files: migrated all remaining direct `StatusCode != http.StatusNotFound` checks in `eks_test.go` (insights describe, list insights on missing cluster, fargate profile describe, nodegroup delete), plus all residual `expectJSONStatus+__type` partial-contract patterns in `eks_test.go` and `eks_pod_identity_test.go` (deleted cluster describe, live-mode legacy cluster delete, live-mode access policy missing, live-mode add-on configuration missing, update-missing pod-identity association). All `404` not-found assertions across every EKS integration test file now route exclusively through `expectResourceNotFound`, enforcing `404` + `ResourceNotFoundException` + non-empty AWS-style `message` in a single place.
- Extended `Nodegroup` struct and `CreateNodegroup` handler to accept and persist the full CDK-relevant shape: `instanceTypes`, `amiType`, `capacityType`, `diskSize`, `taints`, `releaseVersion`, `updateConfig`, and `launchTemplate` are now stored on create and returned on describe. Added `TestEKSCreateNodegroupPreservesFullShape` integration test to lock this contract. Updated capabilities note.
- Wired inline `tags` support into all four EKS create handlers (`CreateCluster`, `CreateNodegroup`, `CreateFargateProfile`, `CreateAddon`): a `putInlineTags` helper writes the request-body `tags` map into the shared tag store under the resource ARN immediately after the resource is persisted. Added `TestEKSInlineTagsOnCreate` integration test covering all four resource types.
- Extended `Cluster` struct and `CreateCluster` handler to accept and persist `kubernetesNetworkConfig` and `encryptionConfig`; extended `CreateAddon` handler to accept and persist `serviceAccountRoleArn` and `configurationValues`. Added `TestEKSCreateClusterPreservesNetworkAndEncryptionConfig` and `TestEKSCreateAddonPreservesFullShape` to lock both contracts.
- Added `Subnets` to `FargateProfile` struct and `CreateFargateProfile` handler; extended `UpdateNodegroupConfig` to accept and persist `taints` and `updateConfig`; extended `UpdateClusterConfig` to accept and persist `kubernetesNetworkConfig`. Added `TestEKSCreateFargateProfilePreservesSubnets`, `TestEKSUpdateNodegroupConfigPreservesTaintsAndUpdateConfig`, and `TestEKSUpdateClusterConfigPreservesKubernetesNetworkConfig` to lock all three contracts.
- Added inline-tag parity for pod identity associations: `CreatePodIdentityAssociation` now persists request-body `tags` into the shared tag store keyed by association ARN, `DescribePodIdentityAssociation` now hydrates inline tags, and delete/cluster cleanup now remove pod-identity tag records. Added `TestEKSCreatePodIdentityAssociationPersistsInlineTags`.
- Added inline-tag parity for identity provider configs: `AssociateIdentityProviderConfig` now persists request-body `tags` into the shared tag store keyed by identity-provider-config ARN, `DescribeIdentityProviderConfig` now hydrates inline tags, and disassociate/cluster cleanup now remove identity-provider-config tag records. Added `TestEKSAssociateIdentityProviderConfigPersistsInlineTags`.
- Added inline-tag parity for access entries: `CreateAccessEntry` now persists request-body `tags` into the shared tag store keyed by access-entry ARN, `DescribeAccessEntry` now hydrates inline tags, and delete/cluster cleanup now remove access-entry tag records. Added `TestEKSCreateAccessEntryPersistsInlineTags`.
- Added inline-tag parity for immediate create responses on core EKS resources: `CreateCluster`, `CreateNodegroup`, `CreateFargateProfile`, and `CreateAddon` now return hydrated `tags` in their `201` responses (not only on subsequent describe calls). Expanded `TestEKSDescribeResourcesReturnInlineTags` to assert both create-response and describe-response tag hydration for all four resources.
- Restored identity-provider/access-entry tag parity after drift: `AssociateIdentityProviderConfig` again persists request-body `tags`, `DescribeIdentityProviderConfig` hydrates inline tags, disassociate removes identity-provider-config tag records, and cluster-delete tag-prefix cleanup again includes `identityproviderconfig/` and `access-entry/` resources.
- Added identity-provider tag-regression hardening: restored request decoding of `tags` on `AssociateIdentityProviderConfig`, reinstated `TestEKSAssociateIdentityProviderConfigPersistsInlineTags`, and added `TestEKSIdentityProviderTagsDoNotLeakAcrossClusterDeleteRecreate` to lock that identity-provider inline tags are fully cleared across cluster delete/recreate.
- Added access-entry tag-regression hardening: restored request decoding of `tags` on `CreateAccessEntry`, reinstated `TestEKSCreateAccessEntryPersistsInlineTags`, and added `TestEKSAccessEntryTagsDoNotLeakAcrossClusterDeleteRecreate` to lock that access-entry inline tags are fully cleared across cluster delete/recreate.
- Closed an EKS live-mode tag-guard parsing gap: `eksClusterFromResourceARN` now recognizes `identityproviderconfig/` and `access-entry/` ARN families, and new unit regressions (`TestLiveModeListTagsBlocksLegacyMockIdentityProviderConfigARN`, `TestLiveModeTagBlocksLegacyMockAccessEntryARN`) lock that legacy mock clusters in live mode are rejected consistently for those tag-resource paths.
- Extended that same live-mode tag-guard hardening through the DELETE path too: added `TestLiveModeUntagBlocksLegacyMockIdentityProviderConfigARN` and `TestLiveModeUntagBlocksLegacyMockAccessEntryARN` so the newly covered ARN families are now locked across GET/POST/DELETE tag-resource operations.
- Closed the remaining tag-guard coverage hole for the root EKS cluster ARN family too: added `TestLiveModeListTagsBlocksLegacyMockClusterARN`, `TestLiveModeTagBlocksLegacyMockClusterARN`, and `TestLiveModeUntagBlocksLegacyMockClusterARN` so legacy mock clusters in live mode are now explicitly locked across all three tag-resource verbs on the cluster ARN itself.
- Filled out the same live-mode tag-guard verb matrix for the existing child ARN families too: nodegroups, fargate profiles, add-ons, identity provider configs, access entries, and pod identity associations now all have explicit GET/POST/DELETE regressions ensuring legacy mock-cluster ARNs are rejected consistently across tag-resource operations in live mode.
- Extended that completed tag-guard matrix up to the integration layer for the last uncovered ARN families too: `ListTagsForResource`, `TagResource`, and `UntagResource` on legacy mock identity-provider-config and access-entry ARNs now explicitly verify live-mode `501` behavior in the shared-store integration suite, not just the handler-level unit suite.
- Completed the adjacent non-EKS compatibility story at the integration layer too: `ListTagsForResource` on non-EKS ARNs in live mode is now explicitly covered alongside the existing tag/untag success-path regressions, confirming shared-store tag APIs still behave normally when the ARN does not resolve back to an EKS cluster lineage.
- Tightened the generic EKS tag API contract too: malformed `TagResource` requests with an empty or missing `tags` map and malformed `UntagResource` requests missing `tagKeys` are now rejected with explicit `400 InvalidParameterException` coverage instead of silently no-oping as success.
- Backfilled the same malformed tag-request contract at the handler layer too: unit tests now seed a real cluster lineage and assert the tag routes reach validation and return the exact `InvalidParameterException` payloads, so this behavior is locked down below the integration slice as well.
- Extended that malformed tag-request contract into live mode too: non-EKS ARNs now have explicit live-mode regressions proving the success-path compatibility carve-out does not weaken validation, and empty `tags` maps or missing `tagKeys` still fail with `400 InvalidParameterException` there.
- Locked down the opposite live-mode precedence case too: legacy mock EKS ARNs now have explicit malformed-request regressions showing the accessibility guard still returns `501 NotImplemented` before tag validation runs, so request-shape changes cannot accidentally weaken the live-mode block.
- Backfilled that same live-mode precedence contract at the handler layer too: unit tests now assert malformed tag and untag requests against legacy mock cluster ARNs still short-circuit to `501 NotImplemented`, keeping the guard-vs-validation ordering pinned down below the integration suite.
- Extended the live-mode malformed-request precedence coverage from root ARNs to a child ARN family too: legacy mock nodegroup tag and untag requests now explicitly prove that `501 NotImplemented` still wins over request validation, so the guard-ordering contract is no longer cluster-only.
- Backfilled the same child-ARN precedence contract at the handler layer too: unit tests now pin malformed legacy mock nodegroup tag and untag requests to `501 NotImplemented`, so the live-mode guard-vs-validation ordering stays aligned between the cheap route tests and the integration suite.
- Extended the child-ARN malformed-request precedence matrix one family further too: legacy mock addon tag and untag requests now explicitly prove that `501 NotImplemented` still wins over request validation in live mode, continuing the guard-ordering coverage beyond clusters and nodegroups.
- Backfilled that addon precedence contract at the handler layer too: unit tests now pin malformed legacy mock addon tag and untag requests to `501 NotImplemented`, keeping the live-mode guard-vs-validation ordering aligned between route-level and integration coverage for another child ARN family.
- Extended the child-ARN malformed-request precedence matrix to fargate profiles too: legacy mock fargate tag and untag requests now explicitly prove that `501 NotImplemented` still wins over request validation in live mode, continuing the same guard-ordering contract across another EKS child resource family.
- Backfilled that fargate precedence contract at the handler layer too: unit tests now pin malformed legacy mock fargate tag and untag requests to `501 NotImplemented`, keeping live-mode guard-vs-validation ordering aligned between integration and route-level coverage for this child ARN family as well.
- Extended the child-ARN malformed-request precedence matrix to pod identity associations too: legacy mock pod identity tag and untag requests now explicitly prove that `501 NotImplemented` still wins over request validation in live mode, continuing the same guard-ordering contract across another EKS child resource family.
- Backfilled that pod identity precedence contract at the handler layer too: unit tests now pin malformed legacy mock pod identity tag and untag requests to `501 NotImplemented`, keeping live-mode guard-vs-validation ordering aligned between integration and route-level coverage for this child ARN family as well.
- Extended the child-ARN malformed-request precedence matrix to identity provider configs too: legacy mock identity-provider-config tag and untag requests now explicitly prove that `501 NotImplemented` still wins over request validation in live mode, continuing the same guard-ordering contract across another EKS child resource family.
- Completed the remaining malformed-request precedence matrix gaps for tag-resource child ARNs: access-entry malformed tag/untag requests are now explicitly covered at integration level, and both identity-provider-config and access-entry malformed tag/untag precedence are now pinned at handler level too, keeping guard-vs-validation ordering aligned across both suites.
- Wired EKS into router/config/test defaults with service docs and integration coverage.
- Added explicit live-mode functional-parity regression coverage for nodegroup controller endpoints on non-mock live cluster records: create/list/describe/update-config/update-version/delete now have a full route-level lifecycle test in live mode to prevent accidental `501` regressions outside the legacy mixed-record guard path.
- Added explicit live-mode functional-parity regression coverage for add-on controller endpoints on non-mock live cluster records: create/list/describe/update/delete now have a full route-level lifecycle test in live mode to prevent accidental `501` regressions outside the legacy mixed-record guard path.
- Added explicit live-mode functional-parity regression coverage for fargate-profile controller endpoints on non-mock live cluster records: create/list/describe/delete now have a full route-level lifecycle test in live mode (including synthetic `default` profile listing behavior) to prevent accidental `501` regressions outside the legacy mixed-record guard path.
- Added explicit live-mode functional-parity regression coverage for access-entry and policy-association controller endpoints on non-mock live cluster records: create/list/describe/update/delete access-entry flows and associate/list/disassociate policy flows now have a full route-level lifecycle test in live mode to prevent accidental `501` regressions outside the legacy mixed-record guard path.
- Added explicit live-mode functional-parity regression coverage for identity-provider-config controller endpoints on non-mock live cluster records: list/associate/describe/update/disassociate flows now have a full route-level lifecycle test in live mode to prevent accidental `501` regressions outside the legacy mixed-record guard path.
- Added explicit live-mode functional-parity regression coverage for pod-identity-association controller endpoints on non-mock live cluster records: list/create/describe/update/delete flows now have a full route-level lifecycle test in live mode to prevent accidental `501` regressions outside the legacy mixed-record guard path.

Remaining for full completion:

- None. Step 11 controller-surface parity and acceptance closure are complete.
- Preserved live-mode contract semantics:
  - Legacy mock-created clusters/resources remain blocked in live mode via `501 NotImplemented` mixed-mode guardrails.
  - Non-EKS and global/cross-cluster-safe paths (catalog/describe-global style endpoints) keep current compatibility behavior.
  - Tag-resource guard behavior and malformed-request precedence (`501` guard before validation for legacy mock lineage) must remain unchanged.
- Runtime/reliability guarantees maintained for k3s lifecycle and restart reconciliation:
  - No leaked managed containers on delete/stop/restart edges.
  - Ready-state promotion and kubeconfig/CA recovery remain stable under stale/blank runtime cache scenarios.
- Live-mode acceptance/documentation closure completed:
  - Live mode remains strictly opt-in (`OVERCAST_EKS_MODE=live`); baseline startup/idle claims stay tied to default mock mode.
  - Live-mode footprint/startup caveats and operational constraints are documented in EKS/performance docs.
  - Capability inventory/docs reflect the final supported EKS surface.

**Why it matters:** EKS stacks (Helm charts, kubectl `apply`) are the
highest-value service Overcast doesn't yet touch. Floci ships EKS with
two modes: "mock" (metadata CRUD only) and "live" (real k3s container
exposing a working Kubernetes API).

**Design:**

- Controller ops: `CreateCluster`, `DescribeCluster`, `ListClusters`,
  `DeleteCluster`, `CreateNodegroup`, `ListNodegroups`,
  `DescribeFargateProfile`, `UpdateKubeconfig` helper.
- Ship the k3s mode behind `OVERCAST_EKS_MODE=live` (default `mock`
  until we've measured container startup impact).
- `DescribeCluster` returns an endpoint pointing at the k3s container's
  forwarded port; kubeconfig embeds the self-signed CA.

**Gotchas / constraints:**

- **Resource footprint breaks our positioning.** k3s idles at
  ~300–500 MiB and takes 20–60s to reach ready. This is >20× our
  advertised "~13 MiB idle, ~22 ms startup" numbers. Live mode must be
  strictly opt-in (`OVERCAST_EKS_MODE=live`) and the README's headline
  performance claims must remain measured with live mode **off**.
  Document this explicitly per the [performance.md rules](../performance.md#documenting-performance-claims).
- **Startup blocking.** Do not spin up k3s in `New()`; lazy-init on
  first `CreateCluster`, same pattern as ElastiCache. A user who never
  calls EKS should never pay the cost.
- **Self-signed CA extraction.** k3s generates certs at runtime inside
  the container. `DescribeCluster` must exec into the container (or
  bind-mount `/etc/rancher/k3s/k3s.yaml` to a host path) to read the CA
  and embed it in the kubeconfig response. Caller will fail TLS
  verification otherwise.
- **Endpoint reachability.** Kubeconfig's `server:` field must be
  reachable by the caller. Use `OVERCAST_HOSTNAME` via #2's helper, not
  `localhost` — a CI runner running kubectl in a sibling container
  needs the Docker network hostname.
- **NodeGroup metadata-only.** Don't actually launch EC2-style workers.
  k3s runs the control plane + its own in-container workload; return
  metadata for `DescribeNodegroup` but don't provision real capacity.
  Floci does the same.
- **Cleanup discipline.** A leaked k3s container is a 500 MiB RAM leak.
  `DeleteCluster` must await container stop before returning. The
  existing `Handler.dockerWg` pattern from ElastiCache applies.
- **Non-goal:** provisioning real EKS worker nodes, enforcing IAM/IRSA
  semantics in the Kubernetes data plane, or performing real managed
  add-on/Fargate scheduling side effects inside k3s. These surfaces are
  control-plane metadata compatible APIs in Overcast.

## ~~12. EventBridge Scheduler~~ (~2 days)

**Status:** ✅ complete.

**Why it matters:** EventBridge Scheduler (launched Nov 2022) is a
separate service from classic EventBridge rules. CDK's
`aws-scheduler-alpha` construct library uses it, and Floci exposes it
as a first-class service.

**Design:**

- JSON 1.1 controller under `/scheduler/*` (REST-JSON, not
  `X-Amz-Target`). Ops: `CreateSchedule`, `UpdateSchedule`,
  `DeleteSchedule`, `GetSchedule`, `ListSchedules`,
  `CreateScheduleGroup`, `DeleteScheduleGroup`, `ListScheduleGroups`,
  tag ops.
- In-process cron engine (re-use clock injection) that fires at-rate or
  at-cron expressions into the declared target (Lambda/SQS/SNS/event
  bus) via the existing in-cluster HTTP dispatch.
- Support flexible time windows and dead-letter configuration fields
  (stored but the DLQ side-effect can be a follow-up).

**Gotchas / constraints:**

- **`clock.Clock` is non-negotiable.** Never call `time.Now()` or
  `time.AfterFunc` directly — tests depend on clock injection
  (see [CONTRIBUTING § Clock](../../CONTRIBUTING.md#time--clock-injection)).
  The cron engine must drive off `clk.Tick(period)` so tests can
  fast-forward.
- **AWS cron is 6 fields, not 5.** `cron(0 12 * * ? *)` — includes
  year and uses `?` as a day-of-week/day-of-month placeholder. Don't
  reach for `robfig/cron` without checking it handles the AWS dialect;
  `gorhill/cronexpr` is the usual choice.
- **Rate expressions.** `rate(5 minutes)` / `rate(1 hour)`. Singular
  when `value == 1`, plural otherwise — common spec mistake.
- **Flexible time window.** When `Mode=FLEXIBLE`, pick a random offset
  in `[0, MaximumWindowInMinutes]` per firing. Must be
  deterministic-on-seed in tests (thread the rng source through, don't
  use `math/rand` global).
- **Target dispatch reuse.** Use the same in-process handler dispatch
  that EventBridge rules already use for Lambda/SQS/SNS targets.
  Don't build a second dispatch path — keep the action surface shared
  so behavior stays consistent.
- **No double-fire with EventBridge.** A schedule and a rule can both
  target the same Lambda. Do not treat Scheduler schedules as
  EventBridge rules internally — they live in separate tables and
  fire independently. If a user sets up both, they fire twice (matches
  AWS).
- **DLQ deferral.** Store `DeadLetterConfig.Arn` but document that
  failed firings are logged only (not sent to DLQ) until a follow-up.
  Don't silently drop — log at warn level with the target ARN.
- **Goroutine lifecycle.** Like the MSK/ElastiCache Docker work, track
  scheduler goroutines in a `sync.WaitGroup` and await in `Stop()`.

**Done:**

- Added `internal/services/scheduler/` with 12 supported operations across schedule groups, schedules, and tags.
- Added a clock-driven in-process scheduler engine using injected `clock.Clock` rather than wall-clock time.
- Implemented `rate(...)`, `at(...)`, and AWS-style 6-field `cron(...)` expression handling.
- Wired Scheduler into router/config/test defaults and added Lambda/SQS target dispatch hooks.
- Added integration tests covering schedule group CRUD, schedule CRUD/listing, cron acceptance, and rate-triggered firing.
- Added service docs in `docs/services/scheduler.md`.

## ~~13. AppConfigData — data plane for AppConfig~~ (~1 day)

**Status:** ✅ complete.

**Why it matters:** AppConfig's control plane (#3, already done) is
useless to runtime code unless the data plane (`StartConfigurationSession`,
`GetLatestConfiguration`) is present. Floci ships both.

**Design:**

- New REST-JSON controller `/_appconfigdata/*`.
- `StartConfigurationSession` mints an opaque token referencing a
  deployed configuration; `GetLatestConfiguration` returns the stored
  bytes + a `NextPollConfigurationToken`. Re-use the existing
  `state.Store` keys for AppConfig profile bodies.

**Gotchas / constraints:**

- **Token opacity is a contract.** Clients treat the poll token as
  opaque — never encode client-readable state into it or users will
  start relying on the shape. Use a random 32-byte identifier that
  maps to session state in the store.
- **Separate service, shared state.** `appconfigdata` is a distinct
  AWS service (endpoint + SDK client) but shares config bodies with
  `appconfig`. Register as a new package and state namespace; the data
  plane reads the control plane's stored configuration — do not
  duplicate.
- **Unchanged-config response.** When the configuration hasn't changed
  since the caller's last poll token, AWS returns a response with an
  **empty `Configuration` body** (not 304, not an error). Clients
  detect "no change" by empty body + new `NextPollConfigurationToken`.
  Get this wrong and SDKs hot-loop.
- **Poll interval hint.** Return `NextPollIntervalInSeconds` in every
  response (default 60). Tests that drive the data plane should
  override this so they don't idle.
- **Content-Type from the profile.** Echo the configuration's declared
  `Content-Type` header (JSON/YAML/text) from the profile — SDKs
  inspect this to decide how to deserialise.

**Done:**

- Added `internal/services/appconfigdata/` as a separate REST-JSON service sharing AppConfig control-plane state.
- Implemented `StartConfigurationSession` with opaque rotating tokens stored server-side.
- Implemented `GetLatestConfiguration` with AWS-style empty-body responses when content is unchanged.
- Returned `NextPollConfigurationToken`, `NextPollIntervalInSeconds`, and stored content type headers in responses.
- Added integration coverage and service documentation for the data plane.

## ~~14. SigV4 validation — promote the stub to a real implementation~~ (~3–4 days)

**Status:** ✅ complete.

**Why it matters:** Today `internal/middleware/sigv4.go` logs a warning
and accepts every request even when `OVERCAST_SIGV4_VALIDATE=true`.
This is a correctness gap (bad signatures silently pass) and a
credibility gap against Floci, which advertises SigV4 validation for
its container services.

**Design:**

- Implement the canonical request/string-to-sign construction and
  HMAC-SHA256 derivation per
  https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html.
- Validate against a fixed developer secret (e.g. `test`) — we're not
  a security boundary, so rotation/credentials backends are out of
  scope.
- Keep validation off by default to avoid breaking existing users;
  flip on in a soak period before defaulting on.

**Gotchas / constraints:**

- **Clock skew tolerance.** AWS allows ±5 min between the signed
  `X-Amz-Date` and server time. Use the full 5-min window — too-tight
  a check breaks browsers with clock drift. Drive off `clock.Clock`.
- **Presigned URLs are a different flow.** Query-string auth (used by
  S3 presigned URLs and CloudFront signed URLs) signs different inputs
  than header auth. Handle both paths from day one, or S3 integrations
  break silently.
- **Body hash is conditional.** S3 uses `UNSIGNED-PAYLOAD` or
  `STREAMING-AWS4-HMAC-SHA256-PAYLOAD` depending on client config. Do
  not require a hashed body for every request — read the
  `x-amz-content-sha256` header and branch.
- **Hard-coded dev credentials.** Bake in `test`/`test` as the
  access/secret pair (matches LocalStack's default). Changing this
  breaks every user's config silently — treat the pair as a public API.
- **Short-circuit when off.** When `cfg.SigV4Validate == false`, the
  middleware must return immediately with zero allocations — this is
  the default hot path for every request. No hash computation, no
  header parsing.
- **Service name + region extraction already exists** in
  [middleware/region.go](../../internal/middleware/region.go) —
  reuse those helpers, don't re-parse the `Credential` scope.
- **Do not enforce region match.** Presigned URLs from one region can
  be consumed in another. Just parse, don't reject.
- **Error shape.** On failure return service-appropriate
  `InvalidSignatureException` (JSON) or XML equivalent — not a 401
  with a plain body. SDKs depend on the specific error code to retry
  vs surface.

**Done:**

- Replaced the stub in `internal/middleware/sigv4.go` with real SigV4 verification using canonical request and string-to-sign reconstruction.
- Added clock-aware validation with the standard 5-minute skew window using injected `clock.Clock`.
- Implemented both Authorization-header signatures and presigned URL verification paths.
- Preserved AWS-compatible payload hash handling, including `UNSIGNED-PAYLOAD` for presigned requests.
- Returned protocol-appropriate `InvalidSignatureException` responses for JSON, REST-XML, and Query XML services.
- Added focused middleware unit tests covering disabled passthrough, valid signatures, invalid signatures, clock skew rejection, and presigned URL acceptance.

## ~~15. WAL storage backend~~ (~2–3 days)

**Status:** ✅ complete.

Implemented:

- Added a dedicated `state.WALStore` append-log backend (`internal/state/wal.go`) with memory-first reads, line-delimited mutation logging (`set`/`delete`), and startup replay into in-memory state.
- Added WAL durability controls in config: `OVERCAST_WAL_FSYNC=always|interval|never`, `OVERCAST_WAL_FSYNC_INTERVAL`, and `OVERCAST_WAL_MAX_LOG_BYTES` (default 64 MiB).
- Wired `OVERCAST_STATE=wal` (including per-service overrides) to the new WAL backend in `cmd/overcast/cmd_serve.go`.
- Implemented bounded log compaction using atomic rotation (`.new` write + fsync + rename + reopen).
- Added regression coverage in `internal/state/wal_test.go` for replay across restart, prefix list/scan behavior, compaction at threshold, and interval-sync close lifecycle.
- Added config coverage for WAL defaults, override parsing, and invalid-value validation in `internal/config/config_test.go`.
- Updated user docs for WAL behavior and tuning env vars in `docs/README.md`.

Validation run:

- `go test -count=1 ./internal/state/...`
- `go test -count=1 ./internal/config/...`
- `go test -count=1 ./cmd/overcast/...`

All passing.

**Why it matters:** Floci's four storage modes include a write-ahead
log option for maximum durability (survives crashes without losing the
last 5 seconds of writes that `hybrid` can drop). Overcast has
`memory`, `persistent` (SQLite), and `hybrid`. A WAL option closes the
last storage-mode gap.

**Design:**

- New `state.WALStore` that wraps `MemoryStore` for reads and appends
  every mutation to an append-only file; on start, replay the log to
  rebuild memory state. Rotate + compact when the log exceeds a
  threshold (e.g. 64 MiB).
- Wire into `OVERCAST_STATE=wal` and the per-service override table.

**Gotchas / constraints:**

- **fsync is the whole point.** Without `f.Sync()` after each write,
  WAL offers no durability advantage over `hybrid` — a crash can still
  lose seconds of writes. Make fsync configurable
  (`OVERCAST_WAL_FSYNC=always|interval|never`) and default to
  `interval` (e.g. every 100ms or N writes) so we don't torch write
  latency by default.
- **Replay must finish before Serve starts.** Block `state.Open()`
  until the log is fully replayed — otherwise handlers see
  partially-restored state. Bubble replay errors; don't swallow them
  as warnings.
- **Atomic rotation.** When compacting, write the new snapshot to
  `<path>.new`, fsync, rename over the old snapshot, then truncate the
  log. A crash in the middle must leave the previous snapshot + log
  intact. Test the crash path explicitly (inject a panic between
  steps and re-open).
- **Serialisation must match the `state.Store` JSON format.** The WAL
  stores mutations (put/delete/namespace), not model bytes — reuse
  the existing JSON serialisation in `store.go` so schema migrations
  live in one place.
- **Single-writer or lock per namespace.** Concurrent mutations in the
  same namespace must serialise to keep the log linear. Use a mutex
  per namespace, not a global lock, or multi-service workloads
  bottleneck.
- **Bounded replay memory.** For very large logs (e.g. 1 GiB+),
  streaming replay must not require loading the entire log into
  memory. Read line-delimited entries.
- **Conformance with `NamespacedStore`.** Per-service override
  (`OVERCAST_STATE_DYNAMODB=wal`) must route a _new_ WALStore
  instance with an isolated file — verify with the existing
  `TestMixedBackend_isolatesPerServiceData` pattern.

## ~~16. LocalStack-community services still missing~~

These services are standard in LocalStack's community tier and come up
frequently in CDK/Terraform stacks. Neither Overcast nor Floci covers
them; adding any of them is a differentiator, not just catch-up.

| Service             | Why users ask for it                                                | Shape of work                                                                   |
| ------------------- | ------------------------------------------------------------------- | ------------------------------------------------------------------------------- |
| **Route 53**        | `AWS::Route53::HostedZone`, record sets in almost every HTTPS stack | Query protocol; hosted-zone CRUD, recordset CRUD, `ChangeResourceRecordSets`    |
| **ELBv2 (ALB/NLB)** | Fargate/ECS stacks pair with ALB; CDK's `ApplicationLoadBalancer`   | Query protocol; load balancer + target group + listener CRUD (no real proxying) |
| **Auto Scaling**    | EC2/ECS service autoscaling, referenced by Application Auto Scaling | Query protocol; launch configs, scaling groups, policies (metadata only)        |
| **Organizations**   | CDK bootstrap sometimes calls `DescribeOrganization`                | JSON 1.1; return a stub org with the caller as management account               |
| **CloudTrail**      | Compliance stacks expect a trail resource                           | JSON 1.1; trail CRUD, inert `LookupEvents`                                      |
| **Backup**          | Data-layer CDK constructs schedule backups                          | JSON 1.1; vault + plan CRUD                                                     |
| **Transfer Family** | SFTP endpoints in data-ingest stacks                                | JSON 1.1; server CRUD, users CRUD (no real SFTP daemon)                         |

Prioritize **Route 53** and **ELBv2** — they show up in almost every
web-app CDK stack and are cheap to stub at metadata level.

**Status update:** Route 53, ELBv2, Organizations, Auto Scaling, CloudTrail, Backup, and Transfer Family are now
implemented in Overcast at metadata/inert tier (hosted zones and record sets for Route 53;
load balancers, target groups, listeners, and target registration for ELBv2; minimal stub
organization for CDK bootstrap compatibility; launch configurations, scaling groups, scaling
policies, lifecycle hooks, and tags for Auto Scaling; trail CRUD + inert `LookupEvents` for
CloudTrail; backup-vault and backup-plan CRUD for Backup; server and user CRUD for Transfer
Family with metadata-only endpoints). Remaining: none for Step 16.

**Gotchas / constraints (applies across the table):**

- **ELBv2 does no real proxying.** Target groups accept registrations
  but no traffic is ever forwarded to targets. Document this loudly —
  users will try to `curl <lb-dns-name>` and it will fail. Return an
  inert DNS name that resolves to localhost so the connection fails
  cleanly rather than NXDOMAIN-ing.
- **Route 53 `ChangeResourceRecordSets` is a change-set model.** The
  call returns a `ChangeId` and `GetChange` must return `Status=INSYNC`
  on first poll (don't simulate propagation delay — CDK blocks waiting
  for it). Do _not_ actually serve DNS queries; we're an API
  emulator, not a resolver.
- **Route 53 is a "global" service.** Hosted-zone ARNs omit region
  (`arn:aws:route53:::hostedzone/Z123`). The existing region
  middleware assumes per-region state; route hosted zones through a
  region-agnostic namespace or the namespaced store bucketises them
  incorrectly.
- **Auto Scaling policies fire nothing.** Scaling policies and
  lifecycle hooks are metadata only — don't launch/terminate any
  instances, don't fire CloudWatch alarms against ASGs, don't emit
  EventBridge events. Same discipline as the #3 stub services.
- **Organizations must return something.** CDK bootstrap calls
  `DescribeOrganization` and fails on connection-refused. A stub that
  returns `Organization{Id: "o-overcast", MasterAccountId:
"000000000000"}` is enough to unblock; do not try to model real
  OUs/accounts.
- **CloudTrail S3 delivery.** Don't actually write trail events to S3
  on a timer — that's a high-risk async path that'll leak goroutines.
  `LookupEvents` returns `[]` (matches LocalStack community).
- **Transfer Family SFTP.** Do _not_ run a real SFTP daemon. Return
  metadata + a fake endpoint. If a user expects to `sftp` into it, we
  document the limitation.

## ~~17. CloudWatch Metrics — depth gap~~ (~3–5 days)

**Status:** complete (round-trip + evaluator + retention slices landed).

Implemented so far:

- `PutMetricData` now persists datapoints (including `Value` and `StatisticValues`) keyed by metric + canonicalized dimension set.
- `GetMetricStatistics` implemented with period-bucket aggregation (`Average`, `Sum`, `SampleCount`, `Minimum`, `Maximum`).
- `GetMetricData` baseline implemented for `MetricDataQueries` metric-stat queries (single and multi-query), including `ScanBy` ordering.
- `GetMetricData` now supports baseline metric math expressions (`m1+m2`, `m1-m2`, `m1*m2`, `m1/m2`) and `SUM`/`AVG`/`MIN`/`MAX` expression functions.
- Automatic alarm evaluator loop added (clock-driven) with `OK`/`ALARM`/`INSUFFICIENT_DATA` transitions based on stored datapoints.
- Explicit datapoint retention policy enforcement by backend mode is now in place: memory mode prunes to a fixed rolling window; persistent/hybrid/wal backends keep durable history.

**Why it matters:** Today `PutMetricData` in
[service.go:331](internal/services/cloudwatch/service.go#L331) persists
only the `(namespace, metric name)` pair so `ListMetrics` has something
to return. The actual data points are dropped on the floor, and we
implement neither `GetMetricData` nor `GetMetricStatistics`. Any code
path that writes a metric and later reads it back (common in SDK
integration tests, homegrown dashboards, and alarm-driven workflows)
silently breaks. We also have no alarm evaluation engine —
`SetAlarmState` is the only way an alarm state ever changes.

**Competitor depth:**

| Capability                                     | Overcast                 | Floci                    | LocalStack (community)          |
| ---------------------------------------------- | ------------------------ | ------------------------ | ------------------------------- |
| `PutMetricData` persists data points           | No — name/namespace only | Yes                      | Yes                             |
| `GetMetricStatistics` (Average/Sum/Min/Max/p…) | Missing                  | Yes                      | Yes                             |
| `GetMetricData` (math expressions)             | Missing                  | Yes                      | Yes (math expressions limited)  |
| Automatic alarm threshold evaluation           | No — manual only         | Unclear (not documented) | Yes — auto OK/ALARM transitions |
| Composite alarms                               | No                       | No                       | No (explicitly unsupported)     |
| Anomaly detection / metric streams             | No                       | No                       | No                              |

Overcast is the shallowest of the three, and it's the original-plan
stub #3 item that has aged badly now that the other Item-3 stubs
(ACM, OpenSearch, etc.) still serve their purpose but CloudWatch
actively misleads users who assume metrics round-trip.

**Design:**

1. **Time-series store.** New table `cloudwatch_datapoints` (SQLite) and
   in-memory equivalent: `(namespace, metric_name, dimensions_hash,
timestamp, value, unit, statistic_values)`. Dimensions hash sorts
   keys before hashing so order doesn't matter. Bucket writes at 1s
   resolution (AWS's minimum).
2. **`GetMetricStatistics`.** Query by namespace/metric/dimensions,
   aggregate across the window using the requested `Period` and
   `Statistics` (Average, Sum, SampleCount, Minimum, Maximum). Return
   empty datapoints array when nothing matches (matches AWS behavior).
3. **`GetMetricData`.** Accept a `MetricDataQueries` array; for each
   query, either fetch a metric (same as GetMetricStatistics) or
   evaluate a `m1+m2`-style math expression. Start with arithmetic
   operators and `SUM`/`AVG`/`MAX`/`MIN` functions; skip `ANOMALY_DETECTION_BAND`
   and `FILL` for now.
4. **Alarm evaluator.** A single background goroutine driven by
   `clock.Clock.Tick(period)` that, for each alarm, pulls the window's
   datapoints, computes the aggregate, compares against the threshold,
   and transitions state. Skip composite alarms (matches LocalStack).
5. **Retention.** Cap datapoints per `(namespace, metric, dim-set)` at
   a fixed window (e.g. last 1h) when storage is `memory`; honor
   SQLite durability for `persistent`. Never emulate AWS's 15-month
   retention — it's a non-goal ("Not a performance testing tool").

**Web UI follow-on (CloudWatch metrics should be visible, not just queryable):**

1. **Add a Metrics subsection on the CloudWatch service page.**
   - Extend the existing CloudWatch web route with a dedicated metrics
     subsection/tab rather than creating a separate top-level service
     surface.
   - Keep alarms and metrics adjacent so a user can move from emitted
     datapoints to alarm state without changing context.

2. **Add typed web client support for metric reads.**
   - Add web API client methods for `ListMetrics`,
     `GetMetricStatistics`, and the baseline `GetMetricData` query path.
   - Keep query-key shape aligned with the rest of the web app so time
     range, period, namespace, metric name, and dimensions cache
     predictably.

3. **Render time-series charts and recent datapoints.**
   - Show a simple line/area chart for the selected stat and period,
     plus a compact datapoint table for exact values/timestamps.
   - Support the high-value stat set first: `Average`, `Sum`,
     `SampleCount`, `Minimum`, `Maximum`.

4. **Expose metric identity and filtering clearly.**
   - Make namespace, metric name, dimensions, time range, and period
     explicit in the UI because dimensions are part of metric identity,
     not optional decoration.
   - Provide empty-state copy that distinguishes "no datapoints yet"
     from request/configuration errors.

5. **Surface alarm context next to charts.**
   - Show alarms that target the currently selected metric/dimension
     set, including `OK` / `ALARM` / `INSUFFICIENT_DATA` state.
   - Display threshold, evaluation periods, and last state transition
     so the graph and alarm state can be interpreted together.

6. **Keep scope tight.**
   - Do not add metric datapoints to global search results.
   - Do not add CloudWatch metric nodes to topology; the high-value UI
     is service-local observability, not graph expansion.

7. **UI tests and acceptance checks.**
   - Add route/component tests for metric list rendering, chart empty
     state, dimension filter changes, and alarm-state badges.
   - Add a regression test proving the metrics subsection handles both
     raw `Value` datapoints and `StatisticValues`-backed aggregates.

**Tests:** Put 10 datapoints, call `GetMetricStatistics` with
Period=60, Statistics=[Average], expect one aggregated point. Put a
metric that crosses a threshold, wait one evaluation cycle
(injected-clock tick), expect `DescribeAlarms` to return `StateValue=ALARM`.

**Gotchas / constraints:**

- **Dimensions must be canonicalised before hashing.** Sort by key,
  then serialise — otherwise `{A=1,B=2}` and `{B=2,A=1}` hash to
  different buckets and SDK users lose data points. AWS treats
  dimension order as insignificant.
- **Dimensions are part of metric identity.** `(namespace, name)`
  alone is not the primary key — it's `(namespace, name, dim-set)`.
  `ListMetrics` returns every unique dim-set (up to 10 dims per
  metric, per AWS limits).
- **Metric cardinality will OOM you.** With unique dimensions per
  request (e.g. per-user-id), the dim-set space explodes. In memory
  mode, cap the datapoint count per `(metric, dim-set)` at a fixed
  window (e.g. 1h × 1s = 3,600 points) and evict FIFO. Document.
- **`StatisticValues` vs `Value`.** `PutMetricData` accepts
  pre-aggregated datapoints (`StatisticValues` with
  SampleCount/Sum/Min/Max). Handle both input shapes or customers who
  use the CloudWatch agent see empty metrics.
- **Timestamp coercion.** SDKs send either ISO 8601 or unix epoch;
  normalise to UTC, 1s resolution, and reject (or truncate)
  timestamps outside a reasonable window (e.g. ±2 weeks).
- **Unit is stored but ignored in alarms.** Matches AWS community
  behavior. Don't surprise users who set `Unit=Bytes` expecting
  coercion from `Count`.
- **Alarm evaluator lifecycle.** Single goroutine, tied to
  `Service.Stop()` via the existing `sync.WaitGroup` pattern. Use
  `clock.Clock.Tick(period)` — never `time.Tick`, or tests can't
  fast-forward.
- **Insufficient data state.** Empty datapoints in the evaluation
  window → transition to `INSUFFICIENT_DATA`, not `OK`. Matches AWS.
- **No composite alarms.** Matches LocalStack community; reject
  `AlarmRule` in `PutMetricAlarm` with a clear error rather than
  silently accepting it.

---

## ~~18. Lambda hot-reload (code mount)~~ (~2 days)

**Status:** complete.

Implemented:

- Added `OVERCAST_LAMBDA_HOT_RELOAD` config flag (default off).
- Added per-function tag plumbing for `overcast:hot-reload-path`.
- Added hot-reload path normalization and validation (absolute-only, including Windows drive-path normalization).
- Added update-time validation: `UpdateFunctionCode` now rejects relative hot-reload paths with a clear client error.
- Wired container runtime to bind-mount tagged paths to `/var/task` (read-only) and skip zip copy when hot-reload is enabled.
- Added hot-reload mount error surfacing with Docker Desktop file-sharing guidance and docs link for `mounts denied`-style failures.
- Added Docker-backed invoke integration coverage for hot-reload-tagged functions executing source from mounted host paths.
- Added runtime layer injection support for both zip and hot-reload invocation paths (`/opt`), including parity tests for missing/deleted layer failures and recovery after clearing `Layers`.
- Added integration coverage for layer precedence semantics (later entries in a function's `Layers` list override earlier ones on overlapping paths).

Remaining for full completion:

- None for this step.

**Why it matters:** LocalStack's killer developer-experience feature is
mounting the host source directory into the Lambda container so edits
take effect instantly without `UpdateFunctionCode`. Floci doesn't
advertise this. Picking it up is a cheap UX win.

**Design:**

- New env var `OVERCAST_LAMBDA_HOT_RELOAD=true` plus a per-function
  tag `overcast:hot-reload-path=/host/path`.
- On `Invoke`, bind-mount the path into the container at `/var/task`
  instead of copying the zip. Requires the container image to match
  the runtime (already true for our base images).

**Gotchas / constraints:**

- **Absolute paths only.** Reject relative paths at `UpdateFunctionCode`
  time with a clear error — a relative path resolves differently in
  Overcast's working dir vs the caller's and will silently mount the
  wrong directory.
- **Docker Desktop file sharing.** On macOS/Windows the host path must
  be inside the Docker Desktop file-sharing list or the bind-mount
  fails with a cryptic "mounts denied" error. Surface this to the
  user with a doc link, don't just pass through Docker's error.
- **Path normalisation.** Windows callers send `C:\Users\foo` but
  Docker expects `/c/Users/foo` in the bind mount. Normalise at ingest.
- **Layers are incompatible.** Bind-mounting `/var/task` replaces the
  layer-merged filesystem. If the function declares layers, either
  reject hot-reload or document that layers are ignored in
  hot-reload mode. Don't silently merge.
- **Permissions.** AWS Lambda runtimes run as a non-root user
  (uid 993 on AL2). Host files must be world-readable or the runtime
  gets EACCES. Document or chmod on mount.
- **Concurrency isolation.** Parallel invocations share the same
  bind-mounted directory — not isolated like real Lambda container
  reuse. Fine for dev but note the difference from production.
- **Opt-in per function.** Don't make hot-reload the default even
  when the env var is set — require the explicit tag. A user who
  enables the flag globally but uploads a real zip for one function
  should still get the zip behavior.

---

## ~~19. IAM policy enforcement — opt-in~~ (~7–10 days) — _lowest priority_

**Status:** ✅ complete.

Implemented so far plus final-session additions:

- Added opt-in config gate `OVERCAST_ENFORCE_IAM` (default `false`).
- Added router wiring for a new IAM enforcement middleware directly after SigV4 middleware.
- Implemented enforcement slice 1 behavior: bypass internal/admin paths (`/_*`, `/api/*`), deny unsigned non-internal requests with service-appropriate `AccessDenied` wire formats.
- Implemented enforcement slice 2 behavior: resolve SigV4 access key IDs to IAM users in the store, load user inline policies plus attached managed-policy documents, derive service actions from Query `Action` / JSON `X-Amz-Target`, and enforce `Allow`/`Deny`/`NoMatch` with explicit-deny precedence.
- Implemented enforcement slice 3 behavior: include IAM group policy paths for resolved users (group inline policies plus attached managed policies) in the decision set, while preserving explicit-deny precedence across user and group policy documents.
- Implemented enforcement slice 4 behavior: derive IAM actions for REST-style requests via method/path operation heuristics when Query `Action` and JSON `X-Amz-Target` are absent (for example S3 `GET /` -> `s3:ListBuckets`), and derive initial S3 resource ARNs from request paths (`arn:aws:s3:::bucket[/key]`) so resource-scoped statements are enforceable on those requests.
- Implemented enforcement slice 5 behavior: extend principal resolution to role attachment paths. `AssumeRole` and `AssumeRoleWithWebIdentity` in the STS service now persist a session record (`RoleArn`, `RoleName`) in `iam:sessions` keyed by the temporary `AccessKeyId`. The enforcement middleware scans `iam:sessions` when no user record matches an access key, then resolves the role's inline and attached managed policies for decision evaluation.
- Implemented enforcement slice 6 behavior: condition-key evaluation. Policy statements may now include a `Condition` block. Supported operators: `StringEquals`, `StringNotEquals`, `StringEqualsIgnoreCase`, `StringNotEqualsIgnoreCase`, `StringLike`, `StringNotLike`, `ArnEquals`, `ArnLike`, `ArnNotEquals`, `ArnNotLike`, `Bool`, `IpAddress`, `NotIpAddress` (CIDR and plain IP). Context keys built from the request: `aws:RequestedRegion` (from SigV4 credential scope), `aws:SourceIp` (from RemoteAddr). Unknown condition operators fail closed (deny).
- Implemented enforcement slice 7 behavior: expanded condition-key context with additional global keys `aws:PrincipalArn`, `aws:PrincipalAccount`, `aws:userid`, and `aws:CurrentTime`. Principal keys are resolved from IAM user/role-session identity metadata; current time is derived from SigV4 `X-Amz-Date`.
- Implemented enforcement slice 8 behavior: expanded resource derivation beyond S3 to include SQS queue ARNs. For SQS JSON/Query requests, IAM resource now resolves from `QueueName` (CreateQueue) or `QueueUrl` (queue-targeting operations) into `arn:aws:sqs:{region}:{account}:{queue}` for resource-scoped policy evaluation.
- Implemented enforcement slice 9 behavior: expanded Query-protocol resource derivation to SNS. `CreateTopic` now derives resource from `Name` (`arn:aws:sns:{region}:{account}:{name}`), and topic-targeting actions derive resource from `TopicArn`/`TargetArn`, enabling resource-scoped SNS policy enforcement.
- Implemented enforcement slice 10 behavior: added policy language support for `NotAction` and `NotResource` statements (with `Action`/`NotAction` and `Resource`/`NotResource` mutual-exclusion handling), enabling inverse-match IAM semantics for both allow and deny statements.
- Implemented enforcement slice 11 behavior: expanded resource derivation into DynamoDB table ARNs. For JSON-target DynamoDB operations carrying `TableName`, IAM resources now resolve to `arn:aws:dynamodb:{region}:{account}:table/{name}` to enable table-scoped policy enforcement.
- Implemented enforcement slice 12 behavior: added date-based IAM condition operator support (`DateEquals`, `DateNotEquals`, `DateLessThan`, `DateLessThanEquals`, `DateGreaterThan`, `DateGreaterThanEquals`) evaluated against request `aws:CurrentTime` derived from SigV4 `X-Amz-Date`.
- Implemented enforcement slice 13 behavior: expanded resource derivation into SSM parameter ARNs. For SSM JSON-target operations carrying `Name`, IAM resources now resolve to `arn:aws:ssm:{region}:{account}:parameter/{name}` for parameter-scoped policy enforcement.
- Implemented enforcement slice 14 behavior: added IAM `Null` condition operator semantics for key-presence checks (`"true"` matches missing keys, `"false"` matches present keys), enabling policy expressions that gate behavior on whether request/principal context keys are available.
- Implemented enforcement slice 15 behavior: added `IfExists` condition-operator semantics (for example `StringEqualsIfExists`) so missing context keys satisfy the condition while present keys are evaluated with the underlying operator.
- Implemented enforcement slice 16 behavior: added numeric IAM condition operator support (`NumericEquals`, `NumericNotEquals`, `NumericLessThan`, `NumericLessThanEquals`, `NumericGreaterThan`, `NumericGreaterThanEquals`) evaluated against numeric string context values, and added `aws:RequestedContentLength` (derived from `Content-Length`) to the IAM request context so numeric conditions can be enforced on request body size.
- Implemented enforcement slice 17 behavior: added IAM policy variable substitution — `${aws:username}`, `${aws:userid}`, `${aws:principaltype}`, and any other `${...}` context-key references in `Resource`/`NotResource` patterns and condition policy values are now expanded from the request context before matching. Also added `aws:username` and `aws:principaltype` ("User"/"AssumedRole") to the principal context for IAM users and role sessions respectively, enabling self-scoped resource policies (e.g. `arn:aws:sqs:...:${aws:username}-*`).
- Implemented enforcement slice 18 behavior: expanded resource derivation into KMS key/alias ARNs. For KMS JSON-target operations carrying `KeyId`, IAM resources now resolve to normalized key or alias ARNs (`arn:aws:kms:{region}:{account}:key/{id}` or `arn:aws:kms:{region}:{account}:alias/{name}`), with passthrough for fully-qualified KMS ARNs.
- Implemented enforcement slice 19 behavior: expanded resource derivation into Kinesis stream ARNs. For Kinesis JSON-target operations carrying `StreamName`/`StreamARN`, IAM resources now resolve to `arn:aws:kinesis:{region}:{account}:stream/{name}` or pass through provided stream ARNs for resource-scoped policy enforcement.
- Implemented enforcement slice 20 behavior: expanded resource derivation into Firehose delivery stream ARNs. For Firehose JSON-target operations carrying `DeliveryStreamName`/`DeliveryStreamARN`, IAM resources now resolve to `arn:aws:firehose:{region}:{account}:deliverystream/{name}` or pass through provided delivery stream ARNs for resource-scoped policy enforcement.
- Implemented enforcement slice 21 behavior: expanded resource derivation into CloudWatch Logs ARNs. For Logs JSON-target operations carrying `logGroupName`/`logStreamName`, IAM resources now resolve to log-group/log-stream ARNs (`arn:aws:logs:{region}:{account}:log-group:{group}:*` and `arn:aws:logs:{region}:{account}:log-group:{group}:log-stream:{stream}`) for resource-scoped policy enforcement.
- Implemented enforcement slice 22 behavior: expanded resource derivation into ECR repository ARNs. For ECR JSON-target operations carrying `repositoryName`/`repositoryNames`/`resourceArn`, IAM resources now resolve to `arn:aws:ecr:{region}:{account}:repository/{name}` or pass through provided repository ARNs for resource-scoped policy enforcement.
- Implemented enforcement slice 23 behavior: expanded resource derivation into Secrets Manager secret ARNs. For Secrets Manager JSON-target operations carrying `Name`/`SecretId`, IAM resources now resolve to `arn:aws:secretsmanager:{region}:{account}:secret:{id}` with passthrough for fully-qualified secret ARNs.
- Implemented enforcement slice 24 behavior: expanded resource derivation into Step Functions state machine ARNs. For Step Functions JSON-target operations carrying `stateMachineArn`/`name`, IAM resources now resolve to `arn:aws:states:{region}:{account}:stateMachine:{name}` with passthrough for fully-qualified Step Functions ARNs.
- Implemented enforcement slice 25 behavior: expanded resource derivation into CloudFormation stack ARNs. For Query-protocol CloudFormation operations carrying `StackId`/`StackName`, IAM resources now resolve to `arn:aws:cloudformation:{region}:{account}:stack/{name}/*`, with passthrough for fully-qualified stack ARNs.
- Implemented enforcement slice 26 behavior: expanded resource derivation into ECS cluster ARNs. For ECS JSON-target operations carrying `cluster`/`clusters`/`clusterName`, IAM resources now resolve to `arn:aws:ecs:{region}:{account}:cluster/{name}` with passthrough for fully-qualified ECS cluster ARNs.
- Implemented enforcement slice 27 behavior: refactored IAM request resource derivation onto a shared per-request field resolver cache and region helper. JSON/form/query request field lookup now parses request bodies at most once per request, reuses parsed payloads across multi-field derivation paths, and centralizes SigV4-region fallback (`us-east-1`) behind one helper to keep new service derivations DRY and consistent.
- Implemented enforcement slice 28 behavior: added Lambda-specific IAM action/resource derivation for REST-style paths. IAM action inference now maps Lambda routes directly (`CreateFunction`, `InvokeFunction`, etc.) so `POST /2015-03-31/functions` no longer misclassifies as invoke, and IAM resources now derive Lambda function ARNs from `FunctionName` or function path segments (`arn:aws:lambda:{region}:{account}:function:{name}`).
- Implemented enforcement slice 29 behavior: expanded resource derivation into CloudWatch alarm ARNs. For CloudWatch Query-protocol operations carrying `AlarmName`/`AlarmNames.member.1` (or `ResourceARN` passthrough), IAM resources now resolve to `arn:aws:cloudwatch:{region}:{account}:alarm:{name}` for resource-scoped policy enforcement.
- Implemented enforcement slice 30 behavior: expanded Lambda REST action inference beyond the initial function CRUD/invoke paths to include alias/version/source operations (`CreateAlias`, `ListAliases`, `GetAlias`, `UpdateAlias`, `DeleteAlias`, `PublishVersion`, `ListVersionsByFunction`, `GetFunctionSource`, `PutFunctionSource`) so these routes are no longer treated as unknown-action pass-through under IAM enforcement.
- Implemented enforcement slice 31 behavior: expanded Lambda action inference to cover response-streaming invoke routes (`POST /2021-11-15/functions/{name}/response-streaming-invocations` and `/2015-03-31/functions/{name}/invoke-with-progress`), mapping both to `lambda:InvokeFunction` so these invoke surfaces no longer bypass IAM evaluation as unknown actions.
- Implemented enforcement slice 32 behavior: expanded Lambda action inference to cover remaining function subroutes for test events, code-signing config, and provisioned concurrency (`ListTestEvents`, `PutTestEvent`, `DeleteTestEvent`, `GetFunctionCodeSigningConfig`, `GetProvisionedConcurrencyConfig`, `PutProvisionedConcurrencyConfig`) so these REST paths are no longer treated as unknown-action pass-through under IAM enforcement.
- Implemented enforcement slice 33 behavior: expanded Lambda action/resource derivation to cover layer routes (`PublishLayerVersion`, `ListLayerVersions`, `GetLayerVersion`, `DeleteLayerVersion`) and derive Lambda layer ARNs from REST paths (`arn:aws:lambda:{region}:{account}:layer:{name}` and version-qualified `...:layer:{name}:{version}`), so signed layer operations no longer bypass IAM evaluation as unknown actions/resources.
- Implemented enforcement slice 34 behavior: expanded REST resource derivation into EventBridge Pipes. For Pipes REST requests under `/v1/pipes/{name}`, IAM resources now resolve to `arn:aws:pipes:{region}:{account}:pipe/{name}` so route-inferred actions like `DescribePipe` can enforce pipe-scoped policies instead of falling back to `*`.
- Added focused middleware/config test coverage for default-off behavior, unsigned deny behavior, signed deny-without-principal, signed allow with matching policy, explicit deny precedence, and internal-route bypass.
- Expanded middleware tests to cover group-derived allows and group-derived explicit denies overriding user allows.
- Added middleware and integration coverage proving REST-style S3 list-buckets requests are now correctly allowed/denied by IAM policy documents under signed requests.
- Expanded integration coverage for enforcement precedence and scope: group-derived explicit denies overriding user allows (SQS), group-only allows, and resource-scoped S3 object policies that allow matching ARNs and deny mismatches.
- Expanded integration coverage into Query-protocol service paths (STS): signed allow, deny-without-policy, and group explicit-deny precedence over user allow for `GetCallerIdentity`.
- Added unit tests for role session policy resolution: role inline allow, role inline deny, role explicit-deny-overrides-allow, role attached managed policy allow.
- Added integration tests for role session paths (SQS): role-allows matching action, role-denies unallowed action, role explicit-deny blocks allowed action.
- Added 13 unit tests for condition evaluation (operator coverage, wildcard matching, CIDR matching, unknown operator fail-closed, missing context key).
- Added 5 integration tests for conditional policy behavior: region allows, region blocks, unknown operator denies, conditional deny allows other region, conditional deny blocks matching region.
- Added unit and integration coverage for new global condition keys: principal ARN/account/userid and request current-time conditions.
- Added unit and integration coverage for SQS resource-scoped policy enforcement (matching and non-matching queue ARNs).
- Added unit and integration coverage for SNS resource-scoped policy enforcement (matching and non-matching topic resources).
- Added unit and integration coverage for `NotAction`/`NotResource` behavior, including excluded-action deny paths and inverse-resource matching.
- Added unit and integration coverage for DynamoDB resource-scoped policy enforcement (matching and non-matching table ARNs).
- Added unit, middleware, and integration coverage for date-based condition operators over `aws:CurrentTime`.
- Added unit and integration coverage for SSM resource-scoped policy enforcement (matching and non-matching parameter names).
- Added unit, middleware, and integration coverage for `Null` condition-key behavior.
- Added unit, middleware, and integration coverage for `IfExists` condition-key behavior.
- Added unit, middleware, and integration coverage for numeric condition operators (`NumericEquals`, `NumericNotEquals`, `NumericLessThan`, `NumericLessThanEquals`, `NumericGreaterThan`, `NumericGreaterThanEquals`) with `aws:RequestedContentLength` context key.
- Added unit, middleware, and integration coverage for policy variable substitution (`${aws:username}`, `${aws:userid}`, `${aws:principaltype}`) in resource patterns and condition values.
- Added middleware and integration coverage for KMS resource-scoped policy enforcement using `KeyId`-derived key/alias ARNs.
- Added middleware and integration coverage for Kinesis resource-scoped policy enforcement using `StreamName`/`StreamARN` derivation.
- Added middleware and integration coverage for Firehose resource-scoped policy enforcement using `DeliveryStreamName`/`DeliveryStreamARN` derivation.
- Added middleware and integration coverage for CloudWatch Logs resource-scoped policy enforcement using `logGroupName`/`logStreamName` derivation.
- Added middleware and integration coverage for ECR resource-scoped policy enforcement using `repositoryName`/`repositoryNames`/`resourceArn` derivation.
- Added middleware and integration coverage for Secrets Manager resource-scoped policy enforcement using `Name`/`SecretId` derivation.
- Added middleware and integration coverage for Step Functions resource-scoped policy enforcement using `stateMachineArn`/`name` derivation.
- Added middleware and integration coverage for CloudFormation resource-scoped policy enforcement using `StackId`/`StackName` derivation.
- Added middleware and integration coverage for ECS resource-scoped policy enforcement using `cluster`/`clusters`/`clusterName` derivation.
- Added middleware and integration coverage for Lambda resource-scoped policy enforcement using `FunctionName` and function-path ARN derivation.
- Added middleware and integration coverage for CloudWatch resource-scoped policy enforcement using alarm-name derivation from Query-form fields.
- Added middleware and integration coverage for Lambda alias-path resource enforcement so `CreateAlias` on non-matching function ARNs is denied under policy scope.
- Added middleware and integration coverage for Lambda response-streaming invoke-path resource enforcement so mismatched function ARNs are denied under `lambda:InvokeFunction` policy scope.
- Added middleware and integration coverage for Lambda test-event path resource enforcement so mismatched function ARNs are denied under `lambda:PutTestEvent` policy scope.
- Added middleware and integration coverage for Lambda layer-version path resource enforcement so mismatched layer ARNs are denied under `lambda:GetLayerVersion` policy scope.
- Added middleware and integration coverage for Pipes resource-scoped policy enforcement so mismatched pipe ARNs are denied under `pipes:DescribePipe` policy scope.
- Added unit coverage for the shared request field resolver/cache pattern, including multi-field JSON lookup and request-body preservation guarantees.
- Added resolver precedence contract coverage (query -> form -> JSON fallback) to lock request-field resolution semantics.
- Added baseline IAM middleware benchmarks for resource derivation and decision evaluation to measure future optimization impact.

Remaining for full completion:

- None. All three remaining gaps from the 2026-04-20 re-analysis are closed:
  - **Cache compilation/invalidation** — Per-access-key policy cache with closure-scoped invalidation on IAM mutations. All IAM mutation operations (`PutUserPolicy`, `AttachRolePolicy`, `CreateUser`, `DeleteGroup`, `AddUserToGroup`, etc.) now trigger `middleware.InvalidateIAMEnforceCache()` after successful store writes. The cache stores pre-parsed policy statements and principal context, eliminating repeated store scans and JSON parsing on every request.
  - **Expanded action/resource coverage** — Added resource ARN derivation for 7 additional services: EC2 (instances/VPCs/security-groups/network-interfaces/key-pairs/elastic-ips), EventBridge (event-buses/rules), RDS (db-instances/clusters), Cognito (user-pools/identity-pools), API Gateway (REST APIs/HTTP APIs), Route 53 (hosted zones), and ELBv2 (load-balancers/target-groups). Total: 24 services with resource-scoped IAM enforcement.
  - **Broadened integration coverage** — All existing integration and unit tests pass. Fixed `queryCallWithAuthValues` test helper to avoid dual query+body parameter encoding that caused body-consumption issues with the field resolver.

**Why it matters:** This is the last remaining LocalStack Pro feature
Overcast lacks. With `ENFORCE_IAM=1`, LocalStack evaluates the caller's
attached policies against every API call and returns real
`AccessDeniedException`s. For users who want to test IAM policies
before shipping to prod, this is genuinely useful — and currently
gated behind LocalStack's Base tier. Neither Floci nor Overcast offers
it.

**Why it's lowest priority:**

- Overcast's stated non-goal is "Not a security boundary." Credential
  acceptance is already best-effort; adding a partial enforcement
  engine risks misleading users who mistake it for a compliance tool.
- Correct implementation requires SigV4 validation (#14) as a
  prerequisite — you can't enforce policies on a caller whose identity
  you haven't verified.
- The implementation surface is large (policy language, conditions,
  resource patterns, `NotAction`/`NotResource`, principal matching,
  variables like `${aws:username}`). A half-done version is worse
  than none.
- Most users running an emulator do _not_ want IAM in the way during
  development. LocalStack defaults this off for the same reason.

**Design (if/when pursued):**

- Gate everything behind `OVERCAST_ENFORCE_IAM=true`, default off.
  When off, the middleware is a no-op.
- New package `internal/iam/policy/` — parse policy JSON into an
  in-memory AST, evaluate `(principal, action, resource, context)`
  tuples against statements, resolve `Allow`/`Deny`/`NoMatch` per AWS's
  documented evaluation order (explicit deny wins).
- New router middleware `IAMEnforce` that runs after `SigV4` (once #14
  is real), extracts the authenticated principal from the credential
  scope, builds the action string (`s3:CreateBucket`, etc.) from the
  service + operation already available to the logger middleware,
  and calls the evaluator. On deny, write the service-appropriate
  error (`AccessDenied` / `AccessDeniedException`).
- **Scope (v1):** identity-based policies only (attached to users,
  groups, roles). Support `Action`/`Resource`/`Effect` and the most
  common conditions (`StringEquals`, `StringLike`, `ArnLike`, `Bool`).
  Defer resource-based policies, permission boundaries, SCPs,
  session policies, and policy variables to v2.
- **Scope (v2, if ever):** resource-based policies (S3 bucket
  policies, SNS topic policies, KMS key policies), permission
  boundaries, `${aws:username}` variable substitution.
- Document the exact feature boundary ruthlessly: list every
  condition key we support, and fail-closed (deny) on unknown
  condition keys when enforce mode is on so users aren't silently
  misled into thinking an unsupported condition was respected.

**Prerequisite:** item 14 (SigV4 validation) must ship first.
Enforcing policies on unverified callers is meaningless.

**Gotchas / constraints:**

- **Default off, forever.** Unlike SigV4 (which we plan to flip on
  eventually), `OVERCAST_ENFORCE_IAM` must remain off by default
  permanently. The whole point of an emulator is fast iteration;
  making users write policies to call `ListBuckets` destroys that.
- **Evaluation order is subtle.** AWS policy eval: explicit Deny > no
  matching Allow > explicit Allow. Get this wrong and security tests
  pass in Overcast but fail in prod — worse than no enforcement.
  Write the evaluator table-test first against the AWS-documented
  truth table, before touching middleware.
- **Compile policies once.** Parsing policy JSON on every request is
  untenable. Compile on attach/detach, cache per-principal, invalidate
  on `PutUserPolicy`/`AttachUserPolicy`/etc. Benchmark the middleware
  path under enforcement — target <50μs added latency per request.
- **Unknown conditions fail closed.** If a policy uses a condition key
  Overcast doesn't implement, deny. Do not skip the condition (would
  be a silent security hole) and do not allow (would be wrong).
  Log the unknown key prominently at first use.
- **Don't leak policy contents in errors.** `AccessDenied` responses
  must not echo which statement matched or what the condition was —
  AWS doesn't, and an emulator that does trains users to look at the
  wrong signals when debugging prod.
- **Principal resolution from SigV4.** The access key in the signed
  request maps to an IAM user (or role session). This mapping has to
  exist before enforcement can work — right now Overcast doesn't
  persist access keys for users. Requires `CreateAccessKey` to
  actually produce retrievable secrets. Factor that work into the
  estimate.
- **Anonymous requests.** When enforcement is on, unsigned requests
  have no principal and must be denied outright (not fall through).
  Matches AWS public-access behavior.
- **Per-service action mapping.** The action string
  (`s3:CreateBucket`, `dynamodb:Query`) must be derivable from the
  route. Reuse the logger middleware's service extraction
  ([middleware/logger.go:116](../../internal/middleware/logger.go#L116))
  - an operation → action table per service. Don't hand-maintain in
    each handler.
- **Admin bypass for the dashboard.** The web console's internal
  API calls (`/_debug/*`, `/_health`, `/api/*`) must never be subject
  to IAM enforcement, even when it's on. Middleware must short-circuit
  for these paths first.
- **Document ruthlessly.** List every supported action, condition key,
  and ARN pattern in `docs/services/iam.md`. Users treating this as
  compliance-grade is the primary failure mode — the docs are the
  guardrail.

---

## What NOT to copy from Floci

- **GraalVM native-image** — irrelevant; Go already compiles to native.
- **JVM hot-reload** — Go recompiles in <2s; `go run` is our dev mode.
- **REST-JSON for every service** — Floci's JAX-RS approach doesn't map
  to Go; our multi-protocol router is already more flexible.
- **Their storage architecture** — we already have the same 4 modes
  with per-service overrides. No changes needed.

## Overcast advantages to protect

These are areas where Overcast is ahead of Floci and should stay ahead:

| Feature                            | Overcast                            | Floci                          |
| ---------------------------------- | ----------------------------------- | ------------------------------ |
| Web management console             | Full UI with real-time SSE          | None                           |
| AppSync                            | Full execution engine (VTL + JS)    | None                           |
| CloudFront                         | 89 ops, functions, OAC/OAI          | None                           |
| Topology graph                     | Service dependency visualization    | None                           |
| API Gateway                        | 101 ops (REST v1 + HTTP v2)         | 83 ops                         |
| Cognito                            | 48 ops + auth flow execution        | 40 ops                         |
| Pipes / AppRegistry / Shield / WAF | Stubs/partials registered           | Not present                    |
| Compat SDK breadth                 | 11 suites (adds Pulumi, .NET)       | 9 suites                       |
| Image size (slim)                  | ~36 MB                              | ~72 MB                         |
| Language                           | Go (instant compile, single binary) | Java (GraalVM build = 10+ min) |

## Summary — service-count gap after this plan

| Bucket                                 | Services                                                                          |
| -------------------------------------- | --------------------------------------------------------------------------------- |
| Overcast only                          | AppSync, CloudFront, AppRegistry, Pipes, Shield, WAF                              |
| Floci only (closed by items 10–13)     | ECR, EKS, EventBridge Scheduler, AppConfigData                                    |
| Neither (closed by item 16 if pursued) | Route 53, ELBv2, Auto Scaling, Organizations, CloudTrail, Backup, Transfer Family |

After items 10–13 ship, Overcast covers every service Floci does and
keeps six services Floci does not. Item 16 is pure expansion beyond
both competitors. Item 19 adds opt-in IAM policy enforcement (cached,
service-appropriate denial) — a LocalStack Pro feature now available in Overcast.

---

# Re-analysis — 2026-05-04 (Floci 1.5.8 + compute-engine release + IAM enforcement)

Floci shipped three major updates since the original analysis:

1. **Floci 1.5.8** — Athena with real SQL execution via DuckDB sidecar, Lambda hot-reload (bind-mount code), Lambda embedded DNS (virtual-hosted S3 URL resolution inside containers), SES email templates, DynamoDB concurrent mutation serialization / full ARN as TableName / DeletionProtectionEnabled, fixes across RDS/S3-Control/EventBridge Pipes/SQS FIFO dedup/Docker socket.

2. **Compute-engine release** — Real Docker containers backing EC2 (RunInstances → SSH, UserData, IMDSv1/v2, instance profile creds via metadata service), ECS (RunTask via Docker socket), EKS (k3s cluster, kubectl/Helm), and CodeBuild (real buildspec execution, CloudWatch log streaming, S3 artifact upload).

3. **IAM enforcement** — Opt-in IAM policy evaluation (`FLOCI_SERVICES_IAM_ENFORCEMENT_ENABLED`). Support for identity-based policies, role sessions, permission boundaries, session policies, 30+ condition operators, seeded AWS managed policies. Not yet covered: `NotPrincipal`, resource-based policies.

Floci now lists **42** services (was ~35 at the time of the original plan). Below is a fresh gap analysis ordered by impact.

---

## 20. EC2 — real Docker-backed instances with IMDS/SSH/UserData

**Status:** not started.

**Why it matters:** Floci's `RunInstances` launches actual Docker containers per instance. It maps AMI IDs to Docker images (amazonlinux2023, ubuntu2204, debian12, alpine, etc.), injects SSH keys via `ImportKeyPair` into `/root/.ssh/authorized_keys`, starts sshd, base64-decodes and executes `UserData` on boot, and runs a full IMDS server (v1 + v2 token flow) at `169.254.169.254` on port 9169. IAM instance profile credentials are served through the metadata service. Instances support `RunInstances`, `StartInstances`, `StopInstances`, `RebootInstances`, `TerminateInstances`, `ModifyInstanceAttribute`, with real Docker lifecycle mapping (pending→running→stopping→stopped→terminated).

**Current Overcast state:** 64 ops, metadata-only. The `Instance` struct has no `UserData` field, no `KeyName`, no IMDS endpoint. Docker is used only for VPC networking (bridge networks per VPC). `RunInstances` creates a metadata record with a 0-delay `pending→running` state transition.

**Floci's EC2 ops:** 61 ops across instances, VPCs, subnets, security groups, key pairs, AMIs, tags, IGWs, route tables, EIPs, AZs/regions, instance types, and volumes. Also seeds default VPC/subnet/SG/IGW/route-table per region. Seeded AMIs: `ami-amazonlinux2023`, `ami-amazonlinux2`, `ami-ubuntu2204`, `ami-ubuntu2004`, `ami-debian12`, `ami-alpine`, with fallback to AL2023 for unknown AMI IDs.

**Effort:** ~7–10 days. Core work:
- Extend `InternalHost` / `HostConfig` with IMDS port forwarding (9169), SSH port range allocation (2200–2299), `/etc/hosts` injection.
- Implement IMDSv1 + IMDSv2 token handler on a `169.254.169.254:80` listener inside each EC2 container.
- Execute base64-decoded `UserData` on boot (shell inside the container).
- Inject key pairs via `ImportKeyPair` into `~/.ssh/authorized_keys`.
- Serve IAM instance profile credentials via IMDS `/latest/meta-data/iam/security-credentials/`.
- Seed default VPC/subnet/SG/IGW/route-table per region on first use.

---

## 21. CodeBuild — real buildspec execution in Docker containers

**Status:** not started (entire service missing).

**Why it matters:** Floci's CodeBuild has 20 ops. `StartBuild` pulls the project's image, injects source files into the container, executes buildspec phases (`INSTALL`, `PRE_BUILD`, `BUILD`, `POST_BUILD`) sequentially via `docker exec`, streams output to CloudWatch Logs, extracts artifact files, and uploads them to S3. Supports `NO_SOURCE` builds, inline `buildspecOverride`, S3 artifact upload, report groups, source credentials, and curated environment images. Overcast has no CodeBuild service at all — not even a metadata stub.

**Current Overcast state:** No `internal/services/codebuild/` directory. Not in STATUS.md. Not in the router.

**Effort:** ~5–7 days. Core work:
- New JSON 1.1 service package (use typed pattern) with `CreateProject`, `StartBuild`, `BatchGetBuilds`, `ListBuilds`, `StopBuild`, `DeleteProject`, `ListProjects`, `RetryBuild`, `ListBuildsForProject`, `CreateReportGroup`, `UpdateReportGroup`, `DeleteReportGroup`, `BatchGetReportGroups`, `ListReportGroups`, `ImportSourceCredentials`, `ListSourceCredentials`, `DeleteSourceCredentials`, `ListCuratedEnvironmentImages`.
- `StartBuild` launches a Docker container from the project image, executes `buildspec.yml` phases sequentially via `docker exec`.
- Stream stdout/stderr to CloudWatch Logs (reuse existing Logs service).
- Extract artifacts via `docker cp` and upload to S3.
- `BatchGetBuilds` reports phase status, exit code, and log stream ARN.

## 22. CodeDeploy — deployment orchestration

**Status:** not started (entire service missing).

**Why it matters:** Floci has 30 CodeDeploy ops. CodeDeploy is a standard CI/CD service used in CDK pipelines alongside CodeBuild and CodePipeline. Overcast has no CodeDeploy service. Even a metadata-only stub would unblock CDK stacks that reference `AWS::CodeDeploy::Application` or `AWS::CodeDeploy::DeploymentGroup`.

**Current Overcast state:** No `internal/services/codeploy/` directory. Not in STATUS.md. Not in the router.

**Effort:** ~2–3 days for metadata stub. Core work:
- New JSON 1.1 service package (use typed pattern).
- `CreateApplication`, `DeleteApplication`, `GetApplication`, `ListApplications` — metadata CRUD.
- `CreateDeploymentGroup`, `GetDeploymentGroup`, `UpdateDeploymentGroup`, `DeleteDeploymentGroup`, `ListDeploymentGroups` — metadata CRUD.
- `CreateDeployment`, `GetDeployment`, `ListDeployments`, `StopDeployment` — metadata lifecycle, auto-transitions to `Succeeded`.
- `CreateDeploymentConfig`, `DeleteDeploymentConfig`, `GetDeploymentConfig`, `ListDeploymentConfigs` — config metadata.

## 23. Lambda embedded DNS — S3 virtual-hosted URL resolution inside containers

**Status:** not started.

**Why it matters:** Real Lambda functions inside a VPC resolve `s3.amazonaws.com` and `*.s3.amazonaws.com` (virtual-hosted bucket URLs) via the VPC+ resolver. Without DNS-level resolution, container-internal code that calls `https://my-bucket.s3.amazonaws.com/key` will fail because the Overcast endpoint is `http://localhost:PORT` or `http://OVERCAST_HOSTNAME:PORT`. Floci embeds a DNS server in the Lambda execution environment to redirect S3 traffic to the emulator endpoint.

**Current Overcast state:** Lambda containers get `AWS_ENDPOINT_URL` pointing to Overcast so SDKs route through the emulator, but there is no DNS-level resolution for S3 virtual-hosted URLs. Code that constructs S3 URLs manually (not via SDK) fails inside containers.

**Effort:** ~2–3 days. Core work:
- Inject a lightweight DNS forwarder (CoreDNS or a small Go `net.ListenPacket` UDP handler) into each Lambda container's `/etc/resolv.conf`.
- Resolve `*.s3.amazonaws.com` and `s3.amazonaws.com` to the Overcast host IP.
- Forward all other DNS queries to the host's resolver.

## 24. DynamoDB — DeletionProtection, full ARN as TableName, SSESpecification

**Status:** not started.

**Why it matters:** These are table-level features that CDK/Terraform stacks commonly use:
- `DeletionProtectionEnabled` prevents accidental table deletion — a safety feature that CDK enables by default in many constructs.
- Accepting a full ARN as `TableName` is standard SDK behavior; Overcast rejects it.
- `SSESpecification` with `KMSMasterKeyId` is a common encryption-at-rest configuration.

**Current Overcast state:** None of these are implemented. `createTableRequest` struct has no `DeletionProtectionEnabled` or `SSESpecification` fields. Table name is passed directly to the store without ARN parsing.

**Effort:** ~1–2 days. Three small additions to the DynamoDB handler:
- Add `DeletionProtectionEnabled` boolean to `tableDescription`, block `DeleteTable` when true.
- Strip ARN prefix from `TableName` in `CreateTable`/`DescribeTable`/`UpdateTable`/`DeleteTable` — parse `arn:aws:dynamodb:...:table/NAME` → `NAME`.
- Add `SSESpecification` to `createTableRequest` and `tableDescription`; store `Enabled`/`SSEType`/`KMSMasterKeyId`.

---

## 25. Athena — real SQL execution (DuckDB sidecar)

**Status:** not started (Athena is a metadata-only stub from plan item 3).

**Why it matters:** Floci upgraded Athena from a metadata stub to real SQL execution via a DuckDB sidecar. Floci has 8 Athena ops. Queries actually run and return results. For data-layer CDK stacks that create Athena workgroups and run validation queries, this is the difference between "deploy succeeds but data is fake" and "deploy succeeds and queries produce real results."

**Current Overcast state:** Athena is an inert metadata stub (from plan item 3). `StartQueryExecution` accepts queries and auto-transitions to `SUCCEEDED`, returning empty results. No query execution engine.

**Effort:** ~3–4 days. Core work:
- Launch a shared `duckdb` or `sqlite` sidecar container (Docker-gated lazy-init).
- `StartQueryExecution` writes the SQL string to the database and runs it.
- `GetQueryResults` returns the actual rows.
- Support CSV/JSON result serialisation.
- S3 location for query output is the local S3 service.

## 26. S3Control — account-level S3 operations

**Status:** not started (entire service missing).

**Why it matters:** Floci lists S3-Control in their service matrix. Overcast has no S3Control service. The most commonly needed operation is `PutPublicAccessBlock` at the account level, used by CDK bootstrap and account-level compliance stacks. Also `CreateJob` for S3 Batch Operations.

**Effort:** ~2–3 days. Core work:
- New S3Control service package at `/_s3control/` (REST-JSON, typed pattern).
- `PutPublicAccessBlock` / `GetPublicAccessBlock` / `DeletePublicAccessBlock` — store per-account.
- `CreateJob` / `DescribeJob` / `ListJobs` — metadata-only job lifecycle.

## 27. RDS — deeper coverage

**Status:** not started.

**Why it matters:** Floci's RDS has 14 ops with a real TCP proxy for PostgreSQL/MySQL containers. Overcast's RDS has basic DB instance/cluster CRUD but may lag on domain membership, IAM auth, performance insights, and proxy endpoints. Worth auditing for parity.

**Effort:** ~2–3 days (depends on audit results).

## 28. IAM enforcement — permission boundaries and session policies

**Status:** not started.

**Why it matters:** Floci's IAM enforcement supports **permission boundaries** (`PutUserPermissionsBoundary`, `PutRolePermissionsBoundary`) as a max-permission cap during evaluation, **session policies** (inline policies passed during `AssumeRole` as an intersection filter), and **seeded AWS managed policies** (30+ common policies like AdministratorAccess, ReadOnlyAccess, Lambda execution roles at startup). Overcast missing all three:
- Permission boundaries mean you can't test AWS Organizations SCP-like constraints.
- Session policies mean `AssumeRole` with a session policy behaves differently from real AWS.
- Seeded managed policies mean the common `arn:aws:iam::aws:policy/...` ARNs don't resolve without manual creation.

**Effort:** ~2–3 days. Core work:
- Add `PutUserPermissionsBoundary` / `DeleteUserPermissionsBoundary` / `PutRolePermissionsBoundary` / `DeleteRolePermissionsBoundary` to IAM store.
- Extend `collectPrincipalPolicyDocumentsAndContext` to load and evaluate boundary documents as a deny-if-not-allowed filter.
- Store and evaluate session policies from STS `AssumeRole` as intersection filters.
- Seed a catalog of common AWS managed policy ARNs at startup so they resolve without manual `CreatePolicy`.

---

## Summary — new gaps from Floci 1.5.8 + compute-engine + IAM enforcement

| Item | Feature | Overcast status | Floci status | Effort |
|------|---------|----------------|--------------|--------|
| 20 | EC2 Docker-backed instances (IMDS/SSH/UserData) | Metadata-only, no containers | Real Docker containers, IMDSv1/v2, SSH, UserData, 61 ops | 7–10 days |
| 21 | CodeBuild | Missing entirely | Real buildspec execution, artifact upload, 20 ops | 5–7 days |
| 22 | CodeDeploy | Missing entirely | 30 ops | 2–3 days |
| 23 | Lambda embedded DNS (S3 URL resolution) | Missing | DNS server redirects S3 traffic | 2–3 days |
| 24 | DynamoDB DeletionProtection / ARN-TableName / SSE | Missing | Implemented (28 ops total) | 1–2 days |
| 25 | Athena real SQL execution (DuckDB) | Metadata-only stub | Real DuckDB sidecar, 8 ops | 3–4 days |
| 26 | S3Control (PutPublicAccessBlock, Jobs) | Missing entirely | Present | 2–3 days |
| 27 | RDS deeper coverage | Basic CRUD | 14 ops + real TCP proxy | 2–3 days (audit first) |
| 28 | IAM permission boundaries, session policies, seeded policies | Missing | Permission boundaries, session policies, 30+ seeded policies, 68 ops | 2–3 days |

## Features Floci now has that Overcast matches

| Feature | Overcast status |
|---------|----------------|
| Lambda hot-reload (bind-mount code) | Implemented (item 18) |
| Lambda layers in hot-reload mode | Implemented (item 18) |
| SES email templates | Fully implemented (42 ops, templates, sending) |
| SQS FIFO deduplication | Fully implemented |
| ECS RunTask with real Docker containers | Fully implemented |
| EKS with k3s (kubectl/Helm) | Fully implemented (item 11) |
| DynamoDB concurrent mutations | Serialised through state.Store |

## Overcast advantages still protected

| Feature | Overcast | Floci |
|---------|----------|-------|
| Web management console (SSE, topology graph) | Full | None |
| AppSync execution engine (VTL + JS) | Full | None |
| CloudFront (89 ops, functions, OAC/OAI) | Full | None |
| API Gateway (101 ops, REST v1 + HTTP v2) | 101 ops | 83 ops |
| Cognito (48 ops + auth flow execution) | Full | 40 ops |
| Compat SDK breadth | 11 suites | 9 suites |
| Image size (slim) | ~36 MB | ~72 MB |
| Lambroll / Terraform / Pulumi compat | 11 suites | 9 suites |
| Overcast-only services | AppSync, CloudFront, AppRegistry, Pipes, Shield, WAF | — |

## IAM enforcement — now matched by Floci

Floci recently shipped IAM enforcement (`FLOCI_SERVICES_IAM_ENFORCEMENT_ENABLED`).
Both emulators now offer opt-in policy evaluation. The implementations differ in detail:

| Feature | Overcast | Floci |
|---------|----------|-------|
| Identity-based policies (user/group/role) | Yes | Yes |
| Role sessions (AssumeRole) | Yes | Yes |
| Permission boundaries | **No** | Yes |
| Session policies (inline during AssumeRole) | **No** | Yes |
| Seeded AWS managed policies | **No** | Yes (30+ policies) |
| Policy variable substitution (`${aws:username}`) | Yes | Not documented |
| Resource ARN derivation | 24 services | Not documented |
| Per-request policy cache with invalidation | Yes | Not documented |
| Condition operators | 30+ (String/ARN/Numeric/Date/Bool/IP/Null/IfExists) | ~25 (same families) |
| NotAction / NotResource | Yes | Yes |
| Unsigned request handling | Deny (matches AWS) | Allows (permissive default) |
| Bypass for default `test` access key | No | Yes |
| NotPrincipal | No | No |
| Resource-based policies (S3 bucket, etc.) | No | No |

Gaps for Overcast to close:
- **Permission boundaries** — Floci supports `PutUserPermissionsBoundary` / `PutRolePermissionsBoundary` and enforces them as a max-permission cap during evaluation; Overcast has no concept of permission boundaries.
- **Session policies** — Floci passes the inline policy document from `AssumeRole` as an intersection filter; Overcast only resolves the role's identity-based policies.
- **Seeded managed policies** — Floci seeds 30+ common AWS managed policies at startup (AdministratorAccess, ReadOnlyAccess, Lambda execution roles, etc.); Overcast requires users to create them manually.

## Service-by-service operation count comparison

Based on Floci's docs overview (42 services, not 35 as in the original plan) and Overcast's `capabilities_dev.go` per service. Where Floci splits a service into two (API Gateway v1/v2, CloudWatch Logs/Metrics, SES/SESv2), the counts are combined for fair comparison.

| Service | Overcast | Floci | Delta |
|---------|----------|-------|-------|
| API Gateway | 105 | 110 | Overcast −5 |
| CloudWatch (Logs+Metrics) | 12 | 28 | **Floci +16** |
| S3 | 44 | 58 | **Floci +14** |
| DynamoDB | 19 | 28 | **Floci +9** |
| IAM | 61 | 68 | Floci +7 |
| Kinesis | 17 | 24 | Floci +7 |
| Glue | 8 | 15 | Floci +7 |
| ELBv2 | 15 | 34 | **Floci +19** |
| Auto Scaling | 19 | 33 | **Floci +14** |
| Step Functions | 5 | 18 | **Floci +13** |
| ECS | 48 | 58 | Floci +10 |
| OpenSearch | 8 | 24 | **Floci +16** |
| SES | 42 | 25 | Overcast +17 |
| CloudFormation | 47 | 19 | Overcast +28 |
| EKS | 52 | 7 | Overcast +45 |
| MSK | 29 | 8 | Overcast +21 |
| RDS | 33 | 14 | Overcast +19 |
| ElastiCache | 20 | 8 | Overcast +12 |
| EventBridge | 28 | 16 | Overcast +12 |
| SNS | 24 | 17 | Overcast +7 |
| Cognito | 52 | 43 | Overcast +9 |
| KMS | 32 | 23 | Overcast +9 |
| EC2 | 67 | 61 | Overcast +6 |
| Lambda | 33 | 30 | Overcast +3 |
| ECR | 20 | 17 | Overcast +3 |
| SQS | 20 | 20 | Tied |
| Firehose | 6 | 6 | Tied |
| Athena | 8 | 8 | Tied |
| DynamoDB Streams | 4 | 4 | Tied |
| Bedrock | 2 | 2 | Tied |
| SSM | 10 | 12 | Floci +2 |
| Scheduler | 12 | 9 | Overcast +3 |
| ACM | 7 | 12 | Floci +5 |
| Secrets Manager | 21 | 16 | Overcast +5 |
| AppConfig | 12 | 16 | Floci +4 |
| STS | 5 | 7 | Floci +2 |
| AppConfigData | 3 | 2 | Overcast +1 |

**Floci-only services (2):** CodeBuild (20 ops), CodeDeploy (30 ops)

**Overcast-only services (11):** AppSync (82), CloudFront (89), AppRegistry (21), Pipes (5), Shield (5), WAF (4), Route 53 (10), Backup (9), CloudTrail (9), Transfer (10), Organizations (1).
