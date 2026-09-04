# Handover: the VPC network sweep domain, and a full review of network lifecycle

**Status:** implemented on the branch — the identity is anchored to the data directory (`serviceutil.DataDirAnchor`), the review in §5 is answered in the PR body, and both tests pass.
**Branch:** `fix/vpc-network-sweep-domain-survives-a-wiped-store`
**Date:** 2026-09-04

This document is self-contained. You do not need the conversation that produced
it. Read it end to end before touching code — the second half asks for a review
whose scope is wider than the fix.

---

## 1. The incident

A user cleared their Overcast state directory and redeployed a CDK stack. The
deploy failed at an RDS instance:

```
CreateDBInstance: HTTP 400
InvalidVPCNetworkStateFault: VPC 'vpc-b7732331' is not launchable for
DB instances (network status=unbacked).
```

RDS is the symptom, not the cause. The cause is three minutes earlier:

```
16:38:56  hybrid seed complete  loaded=0                      ← state wiped
16:38:56  docker reconcile: syncing network state  networks=1 ← old network survives
16:38:59  vpc network: create failed  vpc-b7732331  cidr=10.42.0.0/16
          403: invalid pool request: Pool overlaps with other one on this address space
16:38:59  CreateVpc → 200                                     ← failure swallowed
16:41:21  CreateDBInstance → 400  network status=unbacked
```

An earlier run of the same stack had also failed, with ECS and Lambda unable to
place anything:

```
ResourceInitializationError: ecs: unable to provision the task's network
namespace: connect to VPC network 6b73d961…: 404: network … not found
```

Both are the same underlying fault seen from two sides — a Docker network that
Overcast can neither use nor reclaim.

## 2. Root cause

`removeOrphanedNetworks` may remove a VPC network only when it carries this
instance's `overcast.instance` label. The value of that label is
`serviceutil.InstanceIdentity` — **a UUID stored in a row inside the very state
store it identifies**:

```go
// internal/serviceutil/instance.go
id, found, err := st.Get(ctx, namespace, InstanceKey)
if found && id != "" { return id }
minted := uuid.NewString()
st.Set(ctx, namespace, InstanceKey, minted)
```

Wipe the data directory and the next start mints a new UUID. Every network the
previous incarnation created now carries a label matching nothing, and
`networksInScope` does exactly what its comment promises:

> A network labelled with another instance's identity is invisible: **neither
> adopted nor removed.**

So the reclaim that should free `10.42.0.0/16` is not permitted to see it. The
pool stays allocated in Docker forever. Every wipe strands another `/16`.

`overcast network reset` does not help either — it skips foreign-owned networks
too (`networkSpecTarget.foreignTo`), so today there is **no supported remedy**;
the operator must `docker network rm` by hand.

### It is a regression, two days old

`919d80d09` — *fix(ec2): the startup reconcile removes only the VPC networks
this instance created (#1568)*, 2026-09-02. Before it the sweep was
unconditional:

```go
// Whatever is left in byID was not adopted by any VPC — remove it.
for id, n := range byID { s.h.removeDockerVPCNetwork(ctx, id) }
```

So "wipe state, redeploy" worked, as a side effect of the sweep being too
aggressive. #1568 was fixing a genuinely worse bug — running the ECS
integration package on a developer machine deleted a **live** instance's
network — so it must not simply be reverted.

RDS did not regress: its `InvalidVPCNetworkStateFault` launchability check
predates all of this.

### Why the test suite did not catch it

`TestReconcileNetworks_removesOnlyThisInstancesOrphans` shipped with #1568 and
models four networks: mine-live, mine-orphan, another instance's, unlabelled.
On the wire, a wiped instance's own network is byte-identical to
`net-theirs` — and the test **asserts `net-theirs` must be left alone**. The
suite does not merely miss the case; it encodes the buggy outcome as correct
for anything wearing an unrecognised label.

The blind spot is structural: every test in the package builds the handler and
the networks from one store, so identity is never discontinuous. "Same daemon,
new store" was not expressible.

## 3. The agreed fix — option (B)

**Make the sweep-domain identity as durable as the thing it names.**

The label's own doc already states the intent — the value is *"the identity of
the state store… instances sharing a data directory share records, so they
share a sweep domain."* The intent is the **data directory**. The bug is that
it is implemented as a row *inside* that directory's database. Derive it from
the directory instead and:

- wipe `/data`'s contents → identity unchanged → old networks still carry *my*
  label → the existing reclaim sweeps them → CIDR freed → deploy works;
- two live instances still differ, because their data directories differ →
  #1568's protection intact;
- no liveness protocol, no stealing, no cross-instance sharing.

### Where it must be fixed, and the blast radius

Eight services derive the domain identically:

```
ec2  ecs  efs  eks  elasticache  lambda  msk  rds
    ← serviceutil.NewInstanceDomain(store, nsInstance)
```

**This is not an EC2 bug.** Every wipe orphans every Docker resource Overcast
owns. Volumes partly self-heal (`ListUnusedVolumes` is daemon-answerable, as
the `LabelInstance` comment notes); containers do not — the GC's
`ownedByThisInstance` refuses to sweep them, so they leak on every wipe.

The user chose **(B): fix it in `serviceutil.InstanceIdentity`**, accepting
that it changes the ownership domain for every sweep at once. Option (A),
EC2-only, was rejected: it guarantees revisiting this later as a container leak.

### Implementation sketch (not prescriptive — the review may change it)

Overcast already self-inspects: `containerendpoint/published.go` finds its own
container because *"Docker defaults a container's hostname to its own short
ID"*, then `InspectContainer`s it. From that you can read what backs the data
directory — the volume name or bind-mount source — which is host-unique and
survives wiping the contents.

Ordered fallbacks, each with a stated reason:

1. Containerised: the mount source backing `cfg.DataDir`.
2. Native: the absolute `cfg.DataDir` path (no `/data` collision there).
3. Neither resolvable: today's stored UUID, unchanged.

**Trap:** the path alone is *not* sufficient in containers — every
containerised instance mounts `/data`, so a path hash collides for exactly the
setup this bug was reported on. The mount *source* is the discriminator.

Whatever is chosen must be stable across restarts, distinct per data directory,
and must never silently collide.

## 4. What already exists on the branch

Committed:

| Commit | What |
| --- | --- |
| `66825f201` | `chore:` untrack `.claude/settings.local.json` |
| `913816649` | `fix(networking):` do not reconcile Docker networks against a migrating store |
| `7a939fc96` | `feat(elbv2):` ModifyLoadBalancerAttributes / DescribeLoadBalancerAttributes |
| `2ecd72af5` | `fix(web):` restore scroll position on Back |
| `bbf4333d1` | `fix(web):` diagnostics page no longer crashes on a non-ECS failure |

`913816649` is closely related and worth reading first: the **same startup
reconcile** was also running before the state store finished its schema
migration, reading zero VPCs, and sweeping every EC2 network as an orphan. That
is a second, independent way the CIDR was being stranded, already fixed.

Uncommitted / this hand-over's commit: `internal/services/ec2/vpc_network_sweep_domain_test.go`,
a discriminating pair.

```
--- FAIL: TestReconcileNetworks_reclaimsItsOwnNetworksAfterTheStoreIsWiped
    removed [], want only net-wiped-orphan
     ok: TestReconcileNetworks_stillLeavesAnotherDataDirectorysNetworks
```

The first fails today and pins the bug. The second passes today and pins
#1568's protection — it is what stops the fix regressing into deleting a live
instance's network. **Both must pass when you are done.**

> The branch is deliberately red until the fix lands. `verify-changed.sh` will
> block a push while it is. That is expected, not a problem to route around.

`sweepDomainHandler` in that file is the harness addition: it makes the data
directory and the store separately settable, so "same directory, new store" (a
wipe) and "different directory" (a second instance) are both expressible.

## 5. The review you are being asked to do

The fix above is agreed in principle, but the user asked for a **full review of
network management overall** before it lands — over Overcast start, over stop,
and over networks created and deleted while running. Treat the fix as one
finding among several rather than the whole job.

Two prior bugs in this area were each introduced by a change that was locally
correct. Assume the same is possible here.

### 5.1 Startup

- `router.reconcileDockerDaemon` → `ec2.Service.ReconcileNetworks` →
  `Handler.reconcileNetworks` (`internal/services/ec2/handler_vpc_docker.go:40`).
  Adoption, isolation repair, egress reconcile, join, then the orphan sweep.
- `Handler.ensureRegionReconciled` (`:252`) — the lazy per-region backstop for
  regions the full pass could not cover.
- The two control planes (`overcast`, `overcast_control`) are created in
  `internal/dataplane` with `LabelService = ServiceCore`, so the EC2 pass —
  which filters on `overcast.service=ec2` — never sees them. **Confirm that
  boundary still holds under any identity change**, and that the planes
  themselves are correctly reclaimed/reused across restarts.

Questions to answer:

- After the fix, what happens on first start against a data directory that has
  networks from *before* the identity scheme existed (no `overcast.instance`
  label at all)? Today they are adoptable but never removable. Is that still
  right, or does durable identity let us do better?
- Does the identity resolve *before* anything that stamps or sweeps runs? What
  happens if Docker is unavailable at that moment and the containerised branch
  cannot inspect its own container — does it fall back, or mint a UUID that
  later disagrees with the one used to stamp?

### 5.2 While running

- Create: `CreateVpc` → `vpcNetworkStrategy.EnsureNetwork` → `createDockerVPCNetwork`
  (`:497`). Note `sharedVPCStrategy.EnsureNetwork` **swallows** a create
  failure and records the VPC `unbacked` with a 200 response
  (`vpc_strategy.go:146`) — this is what made the reported failure surface
  minutes later at RDS instead of at the point of failure.
- Delete: `DeleteVpc` → `OnDelete` per strategy.
- Isolation repair recreates a network under its containers
  (`recreateDockerVPCNetwork`, `:1024`), which changes the network **ID** —
  interacting with records that name the old ID (see #1599/#1601 and the
  create-verify in #1661).
- Egress networks (`OVERCAST_VPC_EGRESS=routed`) have their own create/reserve/
  release/sweep lifecycle in `vpc_egress.go`, their own CIDR allocator, and are
  split out of the plane pass by `splitVPCNetworks`.
- The Docker event watcher re-verifies a managed network on `create` and
  forgets one on `destroy` (`internal/docker/watcher.go`, gated by
  `managesNetwork`).

Questions to answer:

- **Is the swallow at `vpc_strategy.go:146` right?** A 200 from `CreateVpc` for
  a VPC that cannot back its network defers the failure to an unrelated
  resource. Options discussed: record a `netProblems` entry so health and the
  console advisory show it immediately; fail the call; or fall back to the
  `remapped` strategy per-VPC. See 5.4.
- Are the egress CIDR allocator and the plane CIDRs subject to the same
  identity fault? `reserveEgressCIDR`/`releaseEgressCIDR` persist reservations
  — what happens to a reservation whose store was wiped but whose network
  survives?
- Does anything create a network **outside** the strategies (tests, compat,
  `dataplane`) that would now be stamped differently?

### 5.3 Shutdown

`ec2.Service.Stop` stops only the lifecycle scheduler. **Nothing removes VPC
networks on shutdown**, by design — records outlive the process and the next
start reconciles.

Questions to answer:

- Is that still correct with durable identity? A graceful shutdown now *could*
  distinguish "my networks, nothing running on them" from a crash. Should it,
  or is leaving reclaim to startup still the better contract?
- Does the container GC's shutdown drain (`DrainAndSweep`) interact with
  networks — can it leave a network with endpoints that reconcile then cannot
  remove?

### 5.4 Cross-cutting: what the design must not break

These were established during the analysis and should be treated as
constraints, not open questions, unless you have a concrete reason:

1. **Never share a Docker network across two live Overcast instances.** ENI
   addresses come from EC2's own counter, per instance, deliberately
   independent of Docker's IPAM (`attachTaskENI`'s comment). Two instances
   allocating into one subnet collide. Worse, `ConnectNetworkWithAliases`
   registers resource hostnames — two instances on one bridge both register
   `rds-aurora-…`, so one environment's app silently reaches the other's
   database. The `shared` strategy's same-CIDR sharing is fine because it is
   *within* one instance: one allocator, one alias namespace, one lifecycle
   owner.
2. **Endpoint-emptiness is not a steal licence for networks.** The
   `LabelInstance` doc gives the rule: prefer the daemon's answer *"where it
   can answer"*, and fall back to the label *"where it cannot, because being
   unreferenced is the resource's normal resting state."* An idle VPC's network
   is empty and must not be taken. (This is why `ListUnusedVolumes` suffices
   for volumes and does not for networks.)
3. **Absence of evidence is not permission.** Both bugs came from a default:
   no label → "mine, delete it"; foreign label → "not mine, ever". Absence
   should route to the daemon, or to the operator, never to an assumption.
4. **When a CIDR genuinely belongs to someone else** — a live instance, or a
   non-Overcast compose project — the answer is the existing `remapped`
   strategy (a unique Docker subnet from `100.64.0.0/10` plus address
   translation), not sharing and not stealing.

### 5.5 Deliverables

- The two tests in `vpc_network_sweep_domain_test.go` passing.
- The existing `vpc_network_ownership_test.go` passing **unchanged** — if it
  needs editing, say why in the PR; it is #1568's regression guard.
- Equivalent coverage for the identity change itself, in
  `internal/serviceutil`, since the change is there and affects eight services.
- A written answer to each question in 5.1–5.3, in the PR body. "Checked, no
  change needed" is a fine answer; silence is not.
- A changelog fragment (`.changelog/`). This ships.
- Anything you decline to fix, filed or listed with a reason.

## 6. Verification

```sh
go test -count=1 ./internal/services/ec2/... ./internal/serviceutil/...
go test -count=1 -tags slim,dev ./internal/... ./tests/integration/...
gofmt -l internal/ && go vet ./... && go vet -tags slim,dev ./...
make lint-go          # or the golangci-lint invocation from the Makefile
```

`make` is not on PATH on the reporting machine; read the target and run the
underlying command. The Docker-backed EC2 tests use a fake daemon
(`newOwnershipDaemon`), so they need no real Docker.

## 7. Open, explicitly not decided

- Whether `overcast network reset` should gain a `--reclaim` for networks whose
  owner matches no live instance — needed for orphans *already* stranded on
  users' machines, which a durable identity fixes going forward but cannot
  retroactively re-label.
- Whether the readiness marker (`Ready.`) should wait for the state store; it
  is currently emitted ~8 s before the store can serve, so init hooks and
  Testcontainers clients see 503s. Related, separately scoped.
