# Deploy diagnostics — keeping the answer after rollback deletes the evidence

> **Status:** in progress. Phase 0 (contract + failing tests), phase 1 (journal + capture),
> phase 2 (ECS collector) and phase 3 (endpoints) complete on `claude/cfn-diag-backend`; the
> console tab is on `claude/cfn-diag-web`.
> Stacked on [#993](https://github.com/Neaox/overcast/pull/993), which this depends on.
> **Scope:** `internal/services/cloudformation/`, `internal/services/ecs/`, `internal/bff/`,
> `web/src/features/cloudformation/`.
> **Audience:** any contributor or agent. Read [CONTRIBUTING.md](../../CONTRIBUTING.md) and
> [AGENTS.md](../../AGENTS.md) first; all their rules apply.

---

## The promise this is trying to keep

> Your `cdk deploy` failed. Overcast rolled the stack back exactly as AWS would, so the ECS service,
> its tasks and their containers are gone. You open the stack in the console and the reason the
> container refused to start is still there — and you can tell at a glance which parts of that
> answer you would also have had in real AWS.

Both halves matter. The first is why this exists. The second is why it is allowed to exist.

## Why it is not true today

When an ECS service cannot keep its tasks alive, the best sentence CloudFormation can carry is the
one ECS gives it:

> `(service Foo) is unable to consistently start tasks successfully. For more information, see the
> Troubleshooting section.`

That is byte-correct AWS behaviour — [handler_services.go:977](../../internal/services/ecs/handler_services.go)
emits it, and [#993](https://github.com/Neaox/overcast/pull/993) made sure it is the sentence that
survives rather than a newer, less useful one. It is also almost useless for debugging, because the
actual answer — container `app` exited 1, stderr `Error: DATABASE_URL is not set` — is not expressible
in CloudFormation's vocabulary at all.

**The ceiling here is AWS's own vocabulary, so no amount of further fidelity work can lift it.**
That is what makes this a separate mechanism rather than a better `ResourceStatusReason`.

### What actually survives a rollback today

An audit of the teardown path was done before designing this, and it changed the plan. The evidence
is **not** simply destroyed — most of it survives, and the reasons it is nevertheless unavailable are
more specific and more fixable than "rollback deleted it".

`rollbackCreate` deletes the ECS service, which drains its tasks
([`drainServiceRecord`](../../internal/services/ecs/handler_services.go)), which stops each task and
then calls `RemoveContainerForce`. What that leaves behind:

| At capture time | Gone | Survives |
| --- | --- | --- |
| immediately | Docker container and its live logs; the task's ECS tags | STOPPED task record with `StoppedReason` / `StopCode`; DRAINING service record with its full `Events` list; retained container logs |
| after 1 hour | the task record, lazily reaped on the next list/describe | the service record; the retained logs, now **unreachable** |
| on `DeleteCluster` | the cluster record only | everything else, orphaned in the store |

So there are three distinct defects, not one:

1. **ECS already retains container logs** — `retainContainerLogs` writes them to a dedicated
   `ecs:task-container-logs` namespace, deliberately outside `Task` so they "can never leak into
   DescribeTasks' AWS response shape". But the write is triggered by the `DockerContainerDied` bus
   event and runs asynchronously on the worker pool, while `stopServiceTasks` calls
   `RemoveContainerForce` immediately — **the capture races the removal and loses exactly when a
   rollback tears a service down**, which is the case this feature exists for. The miss is only a
   `Debug` log.
2. **The retained logs become unreachable long before they are gone.** `GetTaskContainerLogs`
   resolves the task first and requires a live `DockerID`, so once the task record is reaped at its
   one-hour TTL the endpoint 404s on logs that are still sitting in the store.
3. **That namespace has no `Delete`, no TTL and no reaper anywhere in the repo**, and each entry is
   bounded only by the 64 KiB read cap — it is write-only-and-grow. Building on it without bounding
   it would turn a latent leak into a load-bearing one.

**This reframes the work.** The dominant fix is not a parallel store of things Overcast is failing to
keep; it is to make the existing capture deterministic, reachable and bounded. The journal below is
correspondingly smaller than first drafted: it binds a stack's failed deploy to the ECS artefacts and
snapshots the genuinely volatile parts (the task records, which die at one hour), rather than
re-capturing what is already retained.

These ECS-side defects are separable from the diagnostics feature and independently worth fixing, so
they ship as their own change on `claude/ecs-log-retention-hardening` and this plan depends on it.

## This is not a new fidelity exception

Overcast already has a documented, load-bearing split between what it must say as AWS and what it
may say as itself — [architecture.md § "Two things called a bus"](../dev/architecture.md) argues it
directly: the internal bus "answers to nobody… precisely because it is not pretending to be an AWS
service." Three existing surfaces sit on the Overcast side of that line:

- **`/_overcast/*` emulator endpoints**, e.g. [`internal/services/ecs/handler_emulator.go`](../../internal/services/ecs/handler_emulator.go),
  whose header comment is exactly "These are NOT part of the AWS API surface."
- **`x-overcast-*` response headers**, whose naming problem was worked through at length in
  [`internal/protocol/limitation.go`](../../internal/protocol/limitation.go).
- **The RDS retained-logs endpoint**, which is this feature in miniature and is the precedent this
  plan generalises rather than duplicates.

### RDS already solved the hard half

[`internal/services/rds/handler_emulator.go`](../../internal/services/rds/handler_emulator.go) exists
because "a DB instance that failed to start usually has no container left to read". It copies a
bounded tail of the dying container's output onto the instance record (`captureContainerLogs`), and
its response carries `logSource: "container" | "retained"` plus `capturedAt` so — in its own words —
"the UI never implies a container that is not there."

That is the whole idea: **capture the evidence before the teardown, and tag it with where it came
from.** This plan applies it to a CloudFormation deploy, which spans many resources rather than one,
and makes the provenance tag a first-class part of the payload instead of a single string field.

## The two hard rules

Everything below is constrained by these. A change that violates either is wrong even if it is
useful.

1. **Nothing Overcast authors may enter an AWS-shaped field.** Not `ResourceStatusReason`, not
   `StackStatusReason`, not `DescribeStackEvents`, not any SDK-visible response. The console's
   Events tab is a 1:1 mirror of `DescribeStackEvents` and stays one. It is tempting to append
   "— see the Diagnostics tab" to the CFN reason, since CloudFormation can carry one line; do not.
   `x-overcast-emulation-limitation` earned its exception by making a narrow claim about what
   Overcast *will not do*, which is a different kind of statement from a pointer to a UI.
2. **No resource outlives its rollback.** Retaining a container so its logs stay readable would break
   the semantics this feature exists to preserve. Capture only, then delete exactly what AWS deletes.

The pointer that rule 1 forbids in the API is fine in Overcast's own voice: the server log line
emitted at failure may name the console tab, because that log is already Overcast talking, and it is
on screen in the terminal the developer is watching.

## Provenance is the design, not a disclaimer

A badge reading "Overcast-only" is weak — people stop seeing badges. Instead **every section of the
payload carries where it came from**, and the tiers are chosen so that the tag tells a developer
something they need in order to act:

| `provenance` | Shown as | What it tells the reader |
| --- | --- | --- |
| `aws-api` | *From the AWS API* | Real AWS returns this too. `aws ecs describe-services` would have shown it. |
| `overcast-capture` | *Captured by Overcast* | Overcast saved this before rollback deleted it. **Real AWS discards it too** — you would need `awslogs` configured on the task definition beforehand. |
| `overcast-inference` | *Overcast's reading* | Overcast's interpretation of the evidence. Not an AWS concept. |

The third tier is the one that keeps this honest, and it matches the posture
[architecture.md § "The honest limit"](../dev/architecture.md) already takes.

Reinforcing it, every payload carries a **counterfactual** sentence rendered at the foot of the tab:

> In real AWS this deploy would have left you only *"(service Foo) is unable to consistently start
> tasks successfully."* The container output above exists because Overcast captured it before
> rollback — in AWS it would require `awslogs` on the task definition.

That sentence does more anti-misleading work than any badge, because it teaches the difference
instead of disclaiming it — and it is directly useful, since it tells the reader whether the fix
they are about to write depends on a signal they will actually have in production.

---

## The contract

Both the Go and the web workstreams build against this. It is the interface between them, so a
change to it needs both.

**Emulator (Overcast-only):** `GET /_overcast/cloudformation/stacks/{stackName}/diagnostics`
**BFF proxy for the console:** `GET /api/cloudformation/stacks/{stackName}/diagnostics`

`404` with `{"error": "..."}` when no journal exists for the stack — the ordinary case for a stack
that has never failed. The console hides the tab on 404 rather than showing an empty one.

**A journal exists if and only if the most recent deploy of that stack failed.** A successful
create or update deletes the stack's entry. `DeleteStack` deliberately does not: a
`ROLLBACK_COMPLETE` stack is delete-only, so the CDK CLI's response to a failed deploy is to delete
the stack and create it again, and clearing on delete would take the diagnosis away at exactly the
moment the developer went looking for it. The tombstone record `DeleteStack` leaves keeps the
stack's final state and events readable for the same reason. The console cannot gate its request on the tab, because
whether the tab exists *is* the answer to the request, so it probes unconditionally — which makes a
stale entry a failure explanation shown on a green stack, exactly the misleading surface the two
hard rules exist to prevent. It also bounds storage further: entries are bounded not by stack count
but by the number of stacks whose last deploy failed.

**The response always carries `stackName`.** The console identifies a real payload by that field's
presence, so that a `404`'s `{"error": …}` body and an empty body both read as "no diagnostics". A
payload without it is silently treated as absent and the tab never appears.

```jsonc
{
  "stackName": "MyStack",             // always present; how a client tells a payload from a 404
  "stackId": "arn:aws:cloudformation:us-east-1:000000000000:stack/MyStack/…",
  "operation": "CREATE",              // CREATE | UPDATE | DELETE
  "stackStatus": "ROLLBACK_COMPLETE", // the status the stack came to rest at
  "capturedAt": "2026-08-15T04:12:07Z",

  // The sentence CloudFormation itself recorded — repeated here so the tab can
  // show what AWS would have told you, next to what Overcast found.
  "awsReason": "(service Foo) is unable to consistently start tasks successfully. …",

  // One-sentence answer. Always provenance `overcast-inference`; omitted when
  // nothing could be inferred, in which case the UI leads with the sections.
  "headline": "Container \"app\" exited with code 1 about 6s after starting, 3 times.",

  // Optional. Omitted — not softened — when every section is `aws-api`, since
  // there is then nothing Overcast preserved that AWS would have discarded and
  // any sentence would be false. The UI drops its footer when it is absent.
  "counterfactual": "In real AWS this deploy would have left you only …",

  "resources": [{
    "logicalId": "WebService",
    "physicalId": "arn:aws:ecs:us-east-1:000000000000:service/MyStack-Cluster/…",
    "type": "AWS::ECS::Service",
    "statusReason": "…",              // what CloudFormation recorded (AWS surface)

    "sections": [{
      "id": "ecs-service-events",     // stable, unique within the resource
      "title": "ECS service events",
      "provenance": "aws-api",
      "note": "…",                    // optional one-line gloss, house voice
      "kind": "events",
      // Source order, newest first — see "Ordering" below. Never re-sorted.
      "events": [{ "at": "2026-08-15T04:11:58Z", "message": "(service Foo) …" }]
    }, {
      "id": "ecs-stopped-tasks",
      "title": "Stopped tasks",
      "provenance": "overcast-capture",
      "kind": "facts",
      // `label` need not be unique within a section; `hint` is what
      // disambiguates two exit codes from two different containers.
      "facts": [{ "label": "Exit code", "value": "1", "hint": "container app" }]
    }, {
      "id": "ecs-container-output",
      "title": "Container output",
      "provenance": "overcast-capture",
      "kind": "log",
      "log": {
        "label": "task 8f3a… · container app",   // optional
        "text": "Error: DATABASE_URL is not set\n    at …",
        "truncated": true,
        "capturedAt": "2026-08-15T04:12:03Z"
      }
    }]
  }]
}
```

### Why three section kinds and no more

`facts`, `events` and `log` are enough for ECS today and for the two collectors most likely to come
next — a Lambda init failure is `facts` (error type, init duration) plus `log` (init output); an RDS
start failure is `facts` (status, reason) plus `log` (engine output). Adding a fourth kind
speculatively would give the web side branches nothing renders. The union is discriminated on `kind`
so one component per kind renders every collector, present and future.

### Ordering, and what `provenance` is not

**Event ordering is the source service's, preserved verbatim.** ECS lists a service's events newest
first, and which of two failure events survives to be read is the entire subject of
[#993](https://github.com/Neaox/overcast/pull/993) — a collector that quietly reversed them would
undo that work. The renderer displays the slice exactly as sent.

**`provenance` is orthogonal to `kind`.** Today the `headline` is the only `overcast-inference`
thing in a payload and every section is `aws-api` or `overcast-capture` — captured container stderr
is `overcast-capture`, because Overcast preserved it rather than wrote it. That is a statement of
what the current collectors emit, not a constraint: a future collector may legitimately offer an
inferred `facts` section ("the image's architecture does not match the task's platform"), and
nothing should be written that forbids it.

### Rules the payload must obey

- **Never emit environment-variable values, secrets, or resolved parameter values.** Names only.
  A task definition's `environment` may contribute *keys*; its values may not appear. This applies
  to captured container output only insofar as we do not add to it — we do not scrub what the
  container itself printed, because that is the diagnosis, but we never introduce a value the
  container did not print itself.
- **Bounded.** Log text is tail-truncated with `truncated: true` set, reusing the existing helper
  rather than a second copy of it (see DRY below).
- **Best-effort.** A collector that fails must not fail the rollback, change stack status, or delay
  teardown. A missing section is a missing section.

---

## Where the journal lives, and how it is bounded

The first draft of this plan proposed a ring buffer in the shape of
[`internal/events/history.go`](../../internal/events/history.go). The audit made a simpler design
available, and simpler wins.

**One entry per stack, in `state.Store`, under a new `cfn:diagnostics` namespace.** CloudFormation
already stores everything it owns this way — `cfn:stacks`, `cfn:changesets`, `cfn:events` — with the
same region-scoped key convention, so the journal is storage the package already knows how to manage
rather than a second mechanism beside it. It also means a developer who restarts Overcast between
the failure and the investigation still has the answer.

Bounding falls out of the shape rather than needing eviction machinery:

- **One entry per stack, replaced on each failed deploy and deleted when a deploy succeeds.** The
  question the tab answers is why *this* deploy failed, which is the same reasoning already written
  down for `failedResource` in
  [web/src/features/cloudformation/utils.ts](../../web/src/features/cloudformation/utils.ts). Entry
  count is therefore bounded by the number of stacks whose *latest* deploy failed, which is smaller
  than stack count and already bounded by the store.
- **Captured log text is tail-trimmed** with the hoisted `tailBytes`, per container, at the RDS
  precedent's 16 KiB (`maxCapturedLogBytes`). A chatty container cannot grow the entry without limit.
- **A cap on captured tasks per resource** (`maxCapturedTasks`, three) so a service with fifty
  failing tasks contributes a readable sample rather than all of it. How many were omitted is
  recorded in the section's note and folded into the headline as "at least *n* times", so the
  sample is never presented as the whole.
- **A deadline on the whole capture** (`deployDiagnosticsBudget`, five seconds), derived from the
  operation's own context so a shutdown cancels it. Capture runs while a developer is waiting for a
  deploy to fail; a wedged collector must cost a pause, not a hang.

Because the entry is written at capture time and holds its own copy of the log tails, the ECS
hardening work is free to delete `ecs:task-container-logs` keys when a task record is reaped —
fixing that leak without taking the diagnosis away, since the diagnosis is no longer stored there.

### Resolving "should the journal be a CloudFormation concern"

It should. It is keyed by stack, written on the CloudFormation failure path, and read by a
CloudFormation endpoint; a neutral package would own a thing only CloudFormation ever touches.

The collectors are the part that is per-service, and they stay decoupled the way the provisioner
already is: **the ECS collector gathers its evidence over the internal router**
(`DescribeServices`, `ListTasks`, `DescribeTasks`, and the existing
`/_overcast/ecs/tasks/{arn}/logs/{container}` endpoint), exactly as `provisioner_ecs.go` already
calls ECS through `internalJSON`. No new Go import from `cloudformation` to `ecs`, and no new
coupling to undo later.

This works because **capture runs before rollback, while the records are still there** — which is
also the property the P0 test pins.

---

## DRY — what to reuse rather than write again

This feature sits on top of code that already exists, and the audit turned up two duplications. One
of them is not merely untidy — it is a live bug.

**Docker stream demultiplexing — the copies are not equivalent.** `internal/docker/logframe.go`
exports `DemuxStream` and says of itself that it is "the one place that does [this], for every
service". RDS and Lambda use it. ECS carries a private earlier copy,
`stripDockerLogHeaders`, and the difference is behavioural, not cosmetic:

| Input | `stripDockerLogHeaders` (ECS) | `DemuxStream` (shared) |
| --- | --- | --- |
| Non-multiplexed **TTY** stream | **Corrupts it** — no detection; eats 8 bytes of real output as a header, then reads a nonsense length from the payload | Returns it unchanged |
| Mid-stream desync | Keeps mis-parsing to the end | Re-validates each header, appends the remainder verbatim |
| Truncated final frame | Same result | Same result |

So any ECS container started with a TTY produces corrupted output through the ECS path, and only the
ECS path. Collapsing onto the shared function is a bug fix; it belongs with the other ECS hardening
rather than buried in this feature.

**Log tail helpers.** `tailBytes` in [`internal/services/rds/handler_emulator.go`](../../internal/services/rds/handler_emulator.go)
is currently the repo's *only* trailing-trim helper — everything else named `truncate*` keeps the
head, which is the wrong end for a log. The ECS retention path does no trimming at all, which is one
reason its namespace is unbounded.

Hoist `tailBytes` to `internal/serviceutil` rather than copying it a second time.
[CONTRIBUTING.md § Shared utilities](../../CONTRIBUTING.md) requires shared behaviour to live there,
the package already carries small pure helpers of exactly this shape (`ClampInt`, `DefaultInt`,
`ParseIntDefault`), and it already imports `internal/docker`, so no new dependency edge appears.
`internal/protocol` is the wrong home — its own package doc scopes it to wire-protocol concerns.

---

## Workstreams

### P0 — failing tests first (per AGENTS.md) — **done**

All in [internal/services/cloudformation/diagnostics_test.go](../../internal/services/cloudformation/diagnostics_test.go),
written before the implementation:

1. A CloudFormation test that creates a stack whose ECS service cannot keep a task alive, lets the
   rollback complete, and then asserts the diagnostics endpoint still returns the container's output
   and the stopped task's exit code. **This is the whole feature in one test** — it failed on the
   endpoint's absence before P3, and it fails again the moment capture is moved to after teardown:
   by then the resource records read `DELETE_COMPLETE` and the fake ECS router has stopped
   answering for its tasks, so the journal comes out empty. It also asserts the call order
   directly, that `DescribeTasks` precedes `DeleteService`.
2. A test asserting the AWS surface is unchanged by capture: `DescribeStackEvents`, `DescribeStacks`
   and `ListStackResources` byte-identical with the journal present and with it removed. It compares
   one stack against itself rather than two runs against each other, so nothing has to be normalised
   away but the per-call request ID — a normaliser is exactly the thing that would hide an
   Overcast-authored string appearing in a reason field. It also greps the three responses for the
   journal's own vocabulary. Guard for hard rule 1.
3. A test asserting rollback still deletes everything — capture must not retain a resource. Guard for
   hard rule 2.
4. Same-stack replacement, and clearing on a successful deploy. There is no count cap or byte cap to
   test at the journal level: one entry per stack and the per-collector caps made both unnecessary.

### P1 — journal and capture — **done**

`DeployDiagnostics` and its store methods (`cfn:diagnostics`, tiered `TierCached` in
[internal/state/tier.go](../../internal/state/tier.go)); capture hooked at every point a deploy is
decided to have failed, ahead of any teardown:

| Hook | Covers |
| --- | --- |
| `provisionStackResourcesCtx`, at the resource failure | `rollbackCreate` **and** the `DisableRollback` `CREATE_FAILED` path |
| `updateStackResourcesCtx`, at both failure sites | `rollbackUpdate` **and** the `DisableRollback` `UPDATE_FAILED` path |
| `rollbackToStable` | an explicit `RollbackStack` on a stack a failed update left standing |

Each site gathers immediately and defers the write, so the entry records the terminal status rather
than the in-progress one, and both branches of a `DisableRollback` test are covered by one pair of
lines. A capture that finds nothing never displaces one that found something — the case that
matters is a `DisableRollback` failure journalled once, then journalled again by a later
`RollbackStack` after ECS has reaped the task records.

### P2 — the ECS collector — **done**

[diagnostics_ecs.go](../../internal/services/cloudformation/diagnostics_ecs.go): `DescribeServices`,
`ListTasks(desiredStatus=STOPPED)` + `DescribeTasks`, and the existing
`/_overcast/ecs/tasks/{task}/logs/{container}` endpoint — all over the internal router, no Go import
of `internal/services/ecs`. The GET needed no new dispatch machinery: `internalCall` was already the
lower-level helper `internalJSON` is built on, so `internalGET` is a named entry point over it that
states its service (as `internalS3Request` does), and trace-hop recording stays in one place.

The service filter is applied to the described tasks rather than in the `ListTasks` request:
Overcast's `ListTasks` accepts only `cluster`, `family` and `desiredStatus`, and sending real ECS's
`serviceName` would look like a filter that works. Each task states its owner in `group`.

The collector never reads a task definition. That is the enforcement of the privacy rule, not an
oversight — `environment` and `secrets` live there.

### P3 — endpoints — **done**

`/_overcast/cloudformation/stacks/{stackName}/diagnostics` in
[handler_emulator.go](../../internal/services/cloudformation/handler_emulator.go), registered from
`Service.RegisterRoutes` (which had been a no-op), plus the BFF proxy at
`/api/cloudformation/stacks/{stackName}/diagnostics`. The proxy forwards the region and passes the
upstream status through untranslated — the 404 especially, since the console keys the tab's
existence on it.

### P4 — console

The Diagnostics tab. Appears only when the endpoint returns a journal. Order: headline, then per
resource its sections, then the counterfactual. Not in the Events tab. The existing failure banner in
[stack-detail.tsx](../../web/src/features/cloudformation/components/stack-detail.tsx) gains a second
action next to "View events" — *"Why did this fail?"* — since that banner is already the console's
best failure affordance.

### P5 — documentation and reach

`docs/services/cloudformation.md`'s notes and a changelog fragment are done. `docs/services/ecs.md`
belongs with the ECS hardening change on `claude/ecs-log-retention-hardening`, which owns that file.
The collector interface is documented at `diagnosticCollector`, with Lambda and RDS named as the
obvious next two and the note that both fit the existing three section kinds.

---

## Risks / open questions

- ~~**Capture cost on the failure path.**~~ Resolved: the whole capture runs under
  `deployDiagnosticsBudget` (five seconds), derived from the operation's context so a shutdown
  cancels it rather than waiting it out. It is deliberately sequential rather than concurrent —
  making it concurrent would mean racing the rollback it must precede, which is the one thing this
  design cannot allow.
- ~~**Where the collector hook belongs.**~~ Resolved: see the P1 table above. All five failure sites
  are covered, `DisableRollback` included.
- **The capture race is only half-fixed by ordering.** Making `stopServiceTasks` capture before
  `RemoveContainerForce` fixes the teardown path, but a container that crash-loops fast can be gone
  before anything reads it. The honest answer is that some deploys will have no container output and
  the payload must say so plainly rather than rendering an empty pane — the same judgement RDS made
  with its `Message` field.
- **`DeleteCluster` orphans records** rather than cascading, so a stack that deleted its cluster
  leaves service and task records unreachable in the store. Out of scope here, but it is the same
  class of defect as the unbounded log namespace and should get an issue.
