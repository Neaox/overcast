# Deploy failure diagnosis — telling the truth, and telling it unprompted

> **Status:** proposed; prior art all merged (as of 2026-08-22). Everything marked ✅
> is now in `main` — see [Prior art](#prior-art) — and W4's first instance, the
> region preflight check, shipped in [#1004](https://github.com/Neaox/overcast/pull/1004)
> (`internal/router/preflight_region.go`). W4's four remaining named instances
> (Docker, ports, endpoint, ephemeral state) shipped in
> [#1193](https://github.com/Neaox/overcast/issues/1193) — see
> [W4](#w4--environment-preflight) for what each one covers and doesn't; the
> disk-full instance remains open (reactive-only via the existing storage
> health advisories, no proactive free-space probe). W1's eight-service audit
> is done ✅ ([#1107](https://github.com/Neaox/overcast/issues/1107)) — see
> [W1's audit table](#w1-audit-table). Still open: W2's verdict + log line +
> `overcast explain`, and W3 (the correlation key).
> **Scope:** `internal/services/*` (the create/update success paths),
> `internal/trace/`, `internal/events/`, `internal/bff/`, `cmd/overcast`,
> `web/src/features/`.
> **Audience:** any contributor or agent. Read [CONTRIBUTING.md](../../CONTRIBUTING.md)
> and [AGENTS.md](../../AGENTS.md) first; all their rules apply.

---

## The promise this is trying to keep

> Your `cdk deploy` failed. Overcast told you it failed — it did not report
> success for a resource that is not running. It told you in the terminal you
> were already watching, without your having to know that a console tab exists.
> It led with what broke *first* rather than what happened last. And it told you
> which of three things you are looking at: your template, something Overcast
> does not emulate, or a bug in Overcast.

Every rule below exists to keep that sentence true. This document is deliberately
not organised around a screen, because the most valuable parts of it are not on
one.

## Why it is not true today

Four defects, in the order a developer meets them.

### 1. Overcast reports success for things that are not running

The worst of the four, because it poisons every other result. Observed live: a
stack whose ECS container exited 1 on startup reported **`CREATE_COMPLETE`**,
while twenty seconds later `DescribeServices` reported `rolloutState: FAILED`,
`failedTasks: 3`, `running: 0`.

An emulator that is slow is an inconvenience. An emulator that says a broken
deploy worked costs its user the next hour, and — worse — makes every future
green result something they have to double-check by hand, which is most of the
value of having the tool at all.

Fixed for `AWS::ECS::Service` ✅. **The class is wider than the instance**: every
service that creates a container-backed resource and returns a terminal success
status has the same shape of exposure, and none of them has been audited for it.

### 2. The answer does not find you

The developer is in a terminal watching `cdk deploy` scroll. Everything Overcast
knows about the failure is in a console tab they have to know exists, navigate
to, and choose the right pane of.

The asymmetry is the point: Overcast already has the answer at the moment of
failure and says nothing, then waits to be asked.

### 3. Surfaces lead with the newest line, not the first failure

Failures cascade. Rollback is chatty, and it happens *after* the thing that
caused it, so the most recent line is reliably not the interesting one.

This is a law of this codebase, not an observation about one bug.
[trace-retention.md](./trace-retention.md) states it — "the failure is early and
the noise is late" — and derives its whole eviction policy from it.
[#993](https://github.com/Neaox/overcast/pull/993) is the same law in the ECS
scheduler: a replacement task's cheerful "has started 1 tasks." was hiding the
placement failure underneath it.

### 4. "Is this me or the emulator?" has no answer at the point of failure

This question has no analogue in real AWS. Nobody debugging against AWS wonders
whether CloudFormation is broken. Against an emulator you wonder every time, and
guessing wrong is expensive in both directions: hours spent debugging correct
code, or a bug filed against your own template when the emulator was at fault.

Overcast already knows most of the answer. `x-emulator-unsupported` marks a 501,
`x-overcast-emulation-limitation` marks a resource something will not act on
(see [`internal/protocol/limitation.go`](../../internal/protocol/limitation.go)),
and the capability tiers record, per operation, what is emulated and how well.
**None of it is visible at the moment it matters.** It is in a response header
the developer would have to have already been looking for.

### And underneath all four: nothing joins the evidence

The unit of debugging is the *operation*, not the resource. A failed
`cdk deploy` is one event to the person who ran it, and five places to the
person diagnosing it: CloudFormation events, ECS service events, the task's stop
reason, the container's output, and the request trace.

Knowing to visit those five, and in what order, is expert knowledge. There is no
key that spans them:

- `trace.Entry` carries `RequestID`, and hops link child requests to their
  parent — but `Entry.Stack` is a **goroutine stack**, not a CloudFormation
  stack, so traces are not deploy-scoped.
- `StackEvent` carries `ClientRequestToken` back to the originating call.
- `events.History` can be searched by request ID and **only** by request ID —
  `Event.ResourceARN` exists and is populated, but nothing indexes it.
- Container records, where they exist at all, are per-service and join to
  nothing.

Each surface holds a true fragment. None of them can be asked the question the
developer actually has.

---

## The four workstreams

Ordered by value, not by effort. W3 is the spine: W1 and W2 are worth building
without it, and every *renderer* anyone might want later is worth much more with
it.

### W1 — Never report success for something that is not running

**The rule.** A create or update that returns a terminal success status must
first have observed the backing resource actually running, for long enough that
"running" is evidence rather than a coincidence of timing.

The ECS fix establishes the shape and the vocabulary ✅: a settle window,
measured on the resource's own clock, calibrated to outlast the failure mode it
is guarding against, and honest in its comment that it *buys evidence, not
certainty* — a container that survives the window and then dies is still
reported complete.

**The audit.** Every service that creates a container-backed resource:
`rds`, `elasticache`, `msk`, `eks`, `ec2`, `efs`, `ecr`, and `lambda`. For each,
answer in writing: what does its create return, what does it observe before
returning it, and what happens if the container dies one second later. Anything
that cannot answer the third question gets a failing test first, then a window.

**AWS fidelity is the constraint, not the obstacle.** The ECS work found that
AWS's own definitions supported the stricter behaviour — a deployment reaches
`COMPLETED` "when the service reaches a steady state", and steady state means
"healthy and at the desired number of tasks". Expect the same to be true
elsewhere; check before inventing a rule, and record what you checked.

**Cost.** A window is latency on every deploy of that resource type. ECS's is
3 s. Measure per service and keep each no larger than its job needs.

#### W1 audit table

Closed by [#1107](https://github.com/Neaox/overcast/issues/1107). One row per
create path; "settles on" names the mechanism that gates the terminal success
status, cited to the code; "CFN waits" says whether the CloudFormation resource
handler stabilizes (polls the service's own status) before `CREATE_COMPLETE`.

| service | create op | settles on | on container death | CFN waits | verdict |
| --- | --- | --- | --- | --- | --- |
| rds (instance) | `CreateDBInstance`/`StartDBInstance` | `readiness.Watch`: container inspect → TCP dial → credential-bootstrap exec probe (`internal/services/rds/health.go` `scheduleHealthCheck`, `markAvailable`) | `DBInstanceStatus="failed"` + `StatusReason` (`health.go` `failInstance`) | yes, `rdsDBInstanceHandler.Stabilize` polls to `available`/failed (`provisioner_newservices.go`) | already correct |
| rds (cluster) | `CreateDBCluster` | fixed 500 ms timer (`typed_logic.go`) | none — a bare cluster owns no `DockerContainerID`; Docker starts on the first member instance, which gets the full instance-row treatment | yes, but the cluster's own status can only reach `available` | correct by design: nothing container-backed to fail on the cluster record itself |
| elasticache (cache cluster) | `CreateCacheCluster` | `readiness.Watch` probing the real engine protocol (Redis/Memcached PING) (`elasticache/handler.go` `scheduleHealthCheck`) | `incompatible-network` + `StatusReason` (`failCacheCluster`) | yes, `Stabilize` polls `elastiCacheClusterStatuses` | already correct |
| elasticache (replication group) | `CreateReplicationGroup` | same probe shape via `scheduleReplicationGroupHealthCheck` (`handler_replication.go`) | `create-failed` + reason (`failReplicationGroup`) | yes | already correct |
| msk (cluster) | `CreateCluster` | `readiness.Watch` probing a real Kafka `ApiVersions` request (`msk/handler_docker.go` `scheduleHealthCheck`) | `State="FAILED"` + `StateInfo{Code:"BROKER_UNREACHABLE"}` | yes, `Stabilize` polls `mskClusterStatuses` | already correct |
| eks (cluster) | `CreateCluster` | polls the k3s `/readyz` endpoint (`eks/live_runtime.go` `pollK3sReady`); a missing-but-recorded-`ACTIVE` control plane is self-healed by `container_reconcile.go` | `Status="FAILED"` + `health.issues` reason (`failLiveCluster`) | yes, `eksClusterHandler.Stabilize` polls to `ACTIVE`/`FAILED` | already correct |
| eks (nodegroup) | `CreateNodegroup` | synchronous `ACTIVE` (`eks/handler_nodegroup.go`) | n/a | no `Stabilize` (consistent) | out of scope — no container backs a nodegroup at all (kubelet/worker containers are not emulated) |
| efs (file system) | `CreateFileSystem` | zero-delay timer (`efs/typed_logic.go`) | n/a | yes | correct by design: backed by a Docker *volume*, not a container |
| efs (mount target) | `CreateMountTarget` | probes a real NFSv4 NULL RPC against the Ganesha export (`efs/live_nfs.go` `scheduleExportReadiness`), only under `OVERCAST_EFS_NFS=true` | `LifeCycleState="error"` (`markExportFailed`) | yes, `efsMountTargetHandler.Stabilize` | already correct |
| ec2 (instance) | `RunInstances` | scheduler-clock timer, `pending`→`running` (`ec2/handler_instances.go`) | n/a | no `AWS::EC2::Instance` CFN handler exists | out of scope — instances are not container-backed here at all (Docker in `internal/services/ec2` backs VPC networks, not instances) |
| ec2 (VPC Docker network) | `CreateVpc` | `EnsureNetwork` (`ec2/vpc_strategy.go`) | swallowed: `NetworkStatus="unbacked"`, `CreateVpc` still returns success (documented trade-off, "strategy is best-effort") | n/a (no async `Vpc` status AWS itself would poll) | flagged, not changed here — pre-existing, documented, and outside a `CreateVpc`/container-race shape since AWS's own `Vpc` has no readiness status to misreport |
| ecr (repository) | `CreateRepository` | registry container start already blocks `applyCurrentRepoURI` on `waitRegistryReady` (`ecr/service.go`) | **was**: `Warn` log only, response still 200 with a broken/fallback `repositoryUri` | no `Stabilize` (and none is AWS-faithful — real `CreateRepository` is synchronous with no status to poll) | **fixed here**: `CreateRepository` (both wire paths) now marks `x-overcast-emulation-limitation` when the URI it hands out does not name a proven-reachable registry, so the failure reaches `ResourceStatusReason` at create time instead of surfacing later as an opaque `docker push` `405` |
| lambda (function) | `CreateFunction` | container is lazy-per-invoke by design (matches real Lambda: the execution environment starts at first `Invoke`, not at `CreateFunction`); create itself gates on a background image-pull prewarm (`lambda/handler_functions.go`) | `State="Failed"` + `StateReasonCode="ImagePullError"` (`completeFunctionPrewarm`) | yes, `lambdaFunctionHandler.Stabilize` polls to `Active`/`Failed` (`provisioner_lambda_stabilize_test.go`) | already correct |

**Result:** seven of eight services already implemented the ECS pattern (several — RDS, ElastiCache, MSK, EKS, EFS — going further, with a real protocol-level readiness probe rather than a fixed clock). One real gap found and fixed: ECR's `CreateRepository` returned success while silently naming a registry that might not exist or might not be reachable, with the only trace a server log line. EC2 instances and EKS nodegroups are not container-backed at all, so the failure class does not apply to them; the EC2 VPC-network swallow is a separate, pre-existing, documented trade-off, noted for completeness rather than changed.

### W2 — A verdict, delivered where the developer already is

**The verdict** is a four-way classification, and the fourth value is not
optional:

| verdict | meaning |
| --- | --- |
| `your-template` | The request was wrong, and Overcast rejected it the way AWS would. |
| `not-emulated` | Overcast does not implement this, or implements it inertly. Real AWS would have behaved differently. |
| `overcast-bug` | Overcast failed in a way it cannot justify — an internal error, a panic, a violated invariant. |
| `undetermined` | Overcast cannot tell. |

**`undetermined` is what keeps this honest.** A confident wrong verdict is worse
than no verdict: it sends the developer down a path with false authority behind
it. Where the classification is a guess, say so.

The inputs mostly exist: the 501 channel, the limitation header, the capability
tier for the operation, and whether the failure came from a handler's own error
path or from an unexpected one.

**Delivery, in descending order of value:**

1. **The server log line at the moment of failure.** Cheapest and highest value.
   It is already Overcast's own voice — no fidelity argument is needed for it at
   all — and it is already on the screen the developer is watching.
2. **`overcast explain <stack|resource>`.** For when the log line is not enough
   and they are still in the terminal.
3. **The console.** Already built for CloudFormation ✅.

**The standing rule applies without exception:** none of this may enter an
AWS-shaped field. Not `ResourceStatusReason`, not `StackStatusReason`, not any
SDK-visible response. The verdict is Overcast talking about itself, and it
belongs on Overcast's own channels.

### W3 — The correlation key

**One key, minted at the outermost request, propagated through everything that
request causes.**

Most of the mechanism exists and is unjoined rather than absent:

- `internal/trace` already links child requests to their parent
  (`linkChildRequest`), so the propagation path through internal dispatch is
  built.
- `events.Bus.FindEventsByRequestID` already searches history by request ID.
- CloudFormation already stamps `ClientRequestToken` onto every `StackEvent`.

**The work is to stamp the same key onto the surfaces that lack it** — bus
events raised during an operation, container records, the CloudFormation
diagnostics journal — and to index by it. Then "everything about this deploy" is
one query rather than five pages and the knowledge of which order to read them
in.

Two design questions to settle before building:

- **What is the key for work that is not CloudFormation-driven?** A bare
  `aws ecs create-service` has no stack and no client request token, but it still
  has an outermost request. The key should be the operation, not the stack, with
  the stack as an attribute of it.
- **Retention.** The key is only useful while the things it joins still exist,
  and those have three different lifetimes today (trace ring, event ring,
  per-resource records). Reuse [trace-retention.md](./trace-retention.md)'s
  policy rather than inventing a fourth: pin failures, evict noise first, bound
  by bytes, compile-time constants over env knobs.

### W4 — Environment preflight

The failure class with no AWS analogue at all, and the most infuriating to debug
as though it were a code problem.

Known instances, each of which has cost real time on this project: Docker not
running or its socket not permitted; a port already bound; **the client's region
not matching where the data is**; the endpoint pointed somewhere other than the
developer thinks; the state tier being in-memory so a restart silently discarded
everything; the disk being full.

**These are detectable, and the region one shows why it is worth doing.** A
developer whose `AWS_REGION` is `ap-southeast-2` while the console defaults to
`us-east-1` sees an empty stack list and a working emulator, and has no reason to
suspect the region — it looks exactly like a bug in Overcast. Overcast can see
both sides of that and say so:

> No stacks in `us-east-1`. There are 3 in `ap-southeast-2`.

The check should fire **when a symptom matches**, not as a wall of startup
output nobody reads.

The region check shipped ✅ ([#1004](https://github.com/Neaox/overcast/pull/1004),
`internal/router/preflight_region.go` + the console's empty-list advisory).

Four more instances shipped in [#1193](https://github.com/Neaox/overcast/issues/1193) ✅:

- **Docker not running / socket not permitted.** `internal/router.dockerUnavailableWarning`
  (`internal/router/preflight_docker.go`) diffs the configs the Docker Supervisor was asked
  to probe against what actually connected, and logs one aggregated `WARN` naming every
  affected service and socket — no second probe, no per-service repeat of the same fact
  (that per-service detail moved to Debug in `internal/docker/supervisor.go`).
- **A port already bound.** Already fully handled before this issue: `listenAll`
  (`cmd/overcast/cmd_serve.go`) fails startup immediately with the OS's own `bind: address
  already in use`, covered by `TestListenAllReleasesOpenedListenersOnFailure`. Verified, not
  changed.
- **The endpoint pointed somewhere other than the developer thinks.** Distinct from the
  region check as the issue asked: `middleware.WarnRealAWSHost`
  (`internal/middleware/endpointpreflight.go`) fires once per process the first time a
  request's Host targets real AWS's own domain space — the one shape of "wrong endpoint"
  Overcast can actually observe, since a request signed for the *right* endpoint just never
  arrives here at all. States the fact and both fixes; never enforces.
- **The state tier being in-memory so a restart silently discards everything.** The common
  case (auto-detected memory, nothing persisted yet) was already fully covered — the
  "storage mode auto-detected" startup log and `checkMemoryMode`'s three advisory variants
  predate this issue. The narrow, previously-uncovered gap: memory mode chosen *while an
  existing database already sits in the data directory* — `warnIfExistingDatabaseIgnored`
  (`cmd/overcast/preflight_ephemeral.go`) plus `checkMemoryModeIgnoresExisting`
  (`internal/router/advisories.go`) close it, backed by the newly-exported
  `config.HasExistingDatabase`.
- **Ports: published vs. listen mismatch (`-p 4580:4566`).** Already mostly handled before
  this issue — `containerendpoint`'s URL rewriting fixes the common case, and
  `resolvePublishedPort` already detects the remap. Upgraded from Info to an actionable
  Warn naming the one thing rewriting cannot fix (a value compared rather than dialed, e.g.
  a Cognito token's `iss`) and the fix (publish 1:1).

**Still open: the disk being full.** Not proactively probed — a statfs-based free-space
check would need its own platform-specific implementation (mirroring
`internal/config/mountpoint_windows.go` / `mountpoint_unix.go`) and a threshold that risks
false positives on legitimately small volumes, which is more than this issue's scope
justified on its own. It is reactively covered today: a disk-full write failure surfaces
through the SQLite driver's own error text in `checkStoreUnhealthy`/`checkStoreDegraded`
(`internal/router/advisories.go`) the moment it happens, just not before it happens.

---

## What this makes possible

Deliberately downstream of the four above, because each is a *renderer* of
evidence that W3 makes joinable, and each is worth much less on its own:

- **A container lifecycle ledger.** One record per container Overcast created,
  outliving the container, joined to the AWS resource that owned it via the
  `overcast.service` / `overcast.resource-id` labels
  ([`docker.ManagedLabels`](../../internal/docker/client.go)). Its strongest
  argument is consolidation: RDS, ECS and Lambda have each separately invented
  "keep the dead container's last words", with three retention policies and
  three sets of bugs. This would own that once. **No AWS service owns the
  container substrate**, so unlike the item below it has no AWS twin to drift
  against.
- **CloudTrail on top of traces.** Every CloudTrail operation is currently
  `StatusInert` while `trace.Entry` already holds nearly everything
  `LookupEvents` returns. The discipline is the one
  [architecture.md § "Two things called a bus"](../dev/architecture.md) already
  documents: not a merge, but **one occurrence, two audiences** — the trace UI
  gets everything, CloudTrail gets a deliberately lossy AWS-shaped projection.
  The trap is retention: CloudTrail documents 90 days, the trace ring overruns
  during a single CDK deploy, and a `LookupEvents` that silently under-reports is
  worse than the honest `StatusInert` it replaces. Ship it with
  `x-overcast-emulation-limitation` saying so.

---

## Prior art

Work already done that this generalises. All of it merged to `main` on 2026-08-15.

- **[#993](https://github.com/Neaox/overcast/pull/993)** — ECS scheduler failure
  reasons surviving newer progress events; `Retain` / `RetainExceptOnCreate`
  rollback semantics.
- **[#997](https://github.com/Neaox/overcast/pull/997)** (was
  `claude/ecs-log-retention-hardening`) — the capture/removal race, the
  unbounded `ecs:task-container-logs` namespace, and a TTY log-corruption bug in
  a duplicated Docker demultiplexer.
- **The ECS deployment settle window** (was `claude/ecs-deployment-settle`,
  merged as part of [#1005](https://github.com/Neaox/overcast/pull/1005)) — W1's
  first instance, and the source of its vocabulary.
- **[#1005](https://github.com/Neaox/overcast/pull/1005)** (was
  `claude/cfn-deploy-diagnostics`) — the capture-before-rollback journal, the
  provenance tiers (`aws-api` / `overcast-capture` / `overcast-inference`) and
  the counterfactual sentence. W2's console delivery, already built for one
  service. See [cfn-deploy-diagnostics.md](./cfn-deploy-diagnostics.md).

---

## Risks / open questions

- **W1's windows are latency on every deploy.** Eight services × a few seconds
  is a real cost to the tool's feel. Each window must be justified and measured
  separately; a shared constant would be the wrong kind of DRY.
- **A wrong verdict is worse than none.** W2 must be conservative, and
  `undetermined` must be a respectable answer rather than an embarrassment to be
  engineered away.
- **W3 could become a second tracing system.** It is a key and an index, not a
  new store. If it starts wanting its own retention policy independent of the
  buffers it joins, that is the signal it has been over-built.
- **Preflight can cry wolf.** A check that fires on a healthy setup teaches
  people to ignore all of them. Prefer firing on a matched symptom over firing on
  a heuristic.
