# Trace retention — keeping the thing that broke until someone looks at it

> **Status:** phase 1 shipped in [#942](https://github.com/Neaox/overcast/pull/942). Phases 2–4 proposed;
> no retention behaviour has changed yet, and the buffer is still one ring with a floor of 1000.
> **Scope:** `internal/trace/`, `internal/router/debug.go`, `internal/services/cloudformation/provisioner.go`, `web/src/features/debug-traces/`.
> **Audience:** any contributor or agent. Read [CONTRIBUTING.md](../../CONTRIBUTING.md) and [AGENTS.md](../../AGENTS.md) first; all their rules apply.
>
> **Reworked after phase 1**, which turned up something that removed roughly half of what this
> document originally proposed. See [What this no longer needs](#what-this-no-longer-needs).

---

## The promise this is trying to keep

> You ran something big against the emulator. Something in it failed. When you open the trace UI —
> now, or after a meeting — the request that explains the failure is still there, in full, and you
> did not have to configure anything in advance to make that true.

Every rule below exists to keep that sentence true. Where a mechanism does not serve it, it has been
cut rather than kept for tidiness.

## Why it is not true today

A CDK deploy dispatches thousands of requests through the emulator in a couple of minutes. The ring
holds a thousand. So the deploy overruns it, and what survives is decided by arrival order: the
newest thousand.

That is the wrong thousand. A deploy that fails does not stop — it rolls back, and rollback is
*chatty*: `DescribeStacks` polls, `DeleteStack`, resource teardown, each one a trace. **The failure
is early and the noise is late**, so recency eviction reliably keeps the teardown and discards the
`400` that explains why any of it happened.

`OVERCAST_DEBUG_TRACE_BUFFER` does not rescue anyone. By the time you know you needed a bigger
buffer, the trace that told you is gone. A knob you had to have already turned is not a knob a
developer has.

## Phase 1 — say what was dropped ✅ shipped

Before any of the retention work, the UI deleted context silently. `TraceHop` did not declare the
truncation flags at all and both hop-body renderers were gated on the body being present, so a hop
whose body had been dropped rendered *no section whatsoever* — indistinguishable from a hop that
never had one.

Shipped: `OmitReason` in [internal/trace/omission.go](../../internal/trace/omission.go) with its
producers, the legacy `*BodyTruncated` booleans derived from it so the two cannot drift, and one
notice component used at both the entry and hop levels — an inline chip when a size truncation left
a prefix to read, a notice in place of the body when nothing did.

This stands on its own and is independent of everything below.

---

## The finding that reshaped the rest

Verifying phase 1 against a running instance turned up something the unit tests could not.

There is exactly one `AddHop` call site in the codebase — `dispatch` in
[internal/services/cloudformation/provisioner.go](../../internal/services/cloudformation/provisioner.go).
It always dispatches through `router.ServeHTTP`, and it always sets `RequestID` from
`linkChildRequest`. **Every hop is therefore a router-dispatched request with a trace of its own**,
unconditionally. There is no such thing as a hop without one.

So `Hop.RequestBody` / `Hop.ResponseBody` on the parent are a *second copy* of bodies the ring
already holds. Measured live: a `CreateStack` declaring 12 SSM parameters of ~900 KiB spent its whole
8 MiB `MaxHopBodyBytes` budget on the first nine hops and dropped the bodies of hops 10–13 — while
each of those hops' own traces still held its 921,668-byte body **in full, untruncated**.

That reframes the entire memory problem. `MaxHopBodyBytes` is not protecting the ring from a
deploy's data; it is protecting it from a duplicate of that data which need not exist.

### Per-trace footprint, before and after de-duplication

| Component | Cap | Today | After dedup |
| --- | --- | --- | --- |
| Request body | 1 MiB | `maxTraceBody` | unchanged |
| Response body | 1 MiB | `maxTraceBody` | unchanged |
| Hop bodies | 8 MiB | `MaxHopBodyBytes` | **gone** |
| Hop metadata | — | ~200 B × hops | unchanged |
| Log entries | 500 | `AddLog` | unchanged |
| Hop stacks | 20 + 20 | `MaxHopStacks` | unchanged |
| **Worst case** | | **~10 MiB** | **~2.4 MiB** |

Typical is a thousandth of either: a `DescribeStacks` trace is about 2 KiB. But removing the 8 MiB
term is what collapses the worst case far enough for everything else here to get simpler.

---

## The policy

Three rules. A developer should be able to hold all of them at once.

**1. The newest 1,000 traces are always retained, at full fidelity.** Unchanged from today. This is
the floor, and nothing below reduces it.

**2. Beyond the floor, traces are retained for an hour, up to a ceiling.** A deploy that produces
6,000 traces keeps all 6,000 while you investigate. An hour later the overflow is culled back to the
floor. In overcast a deploy is not waiting on real CloudFront or RDS provisioning, so it completes in
minutes — the hour is not sized for the deploy, it is sized for the gap between the deploy failing
and a human opening the trace UI. It covers a coffee break and a meeting. It does not cover
overnight, which is what rule 3 is for.

**3. Traces that went wrong are exempt from both, up to their own cap.** A trace with
`StatusCode >= 400`, a non-empty `AWSErrorCode`, or any hop satisfying the existing `hopFailed` is
*pinned*: not culled by the floor, not culled by the hour. Failures are rare by construction — a
clean deploy has none and a broken one has a handful — so pinning a thousand of them costs nothing in
the common case and converts "probably still there" into "still there".

### Knobs

| Knob | Default | Meaning |
| --- | --- | --- |
| `OVERCAST_DEBUG_TRACE_BUFFER` | `1000` | Floor. Existing name, existing meaning, still an override nobody needs to set. |
| `OVERCAST_DEBUG_TRACE_CEILING` | `10000` | Ceiling on retained count. |
| `OVERCAST_DEBUG_TRACE_WINDOW` | `1h` | How long overflow above the floor survives. |
| `OVERCAST_DEBUG_TRACE_BYTES` | `512MiB` | Backstop; see below. Not the primary bound. |
| `OVERCAST_DEBUG_TRACE_PINNED` | `1000` | Cap on pinned failures. |

Deliberately **not** added: a burst-detection knob. An earlier draft opened a larger buffer on a
detected arrival-rate spike. Rejected — the detector is stateful, hard to test, and its failure mode
is losing data on exactly the unusual traffic it exists to catch. "Keep the last N, keep failures,
cull overflow after an hour" needs no detector.

---

## Phase 2 — three rings, not one ✅ shipped as two

Shipped in the `ring` type and the `live` / `internal` split. **The `pinned` ring is not in it**: it
has no producer until phase 4 sets one, and an empty ring nothing writes to is a dead end a reader
has to chase — the same reason phase 1 left `reclaimed` out of `OmitReason`. The `ring` type it
lands in is here and the third instance is one line.

Measured on a 5900X, `-benchtime=300x -count=2..3`, same benchmark run against `b13dcbe1` and after:

| | depth | before | after |
| --- | --- | --- | --- |
| `ListSummaries`, one 50-row page | 1,000 | 64 µs · 164 KB | **3.2 µs · 8.4 KB** |
| | 10,000 | 700 µs · 1,606 KB | **3.6 µs · 8.4 KB** |
| `Add`, internal, population saturated | 1,000 | 2.0 µs | **0.52 µs** |
| | 10,000 | 44–53 µs | **0.55 µs** |

The shape matters more than the ratios: **before, both paths scaled with retained depth** — listing
11× and internal admission 22× going from 1,000 to 10,000. After, both are flat. That is the property
the ceiling rests on, and `BenchmarkBufferListSummaries_depth` and `BenchmarkBufferAdd_internal` exist
to fail if it is ever lost.

One caveat worth carrying: the `Add` benchmark only reaches the old scan once the internal population
is at its cap, which was `capacity/5` — 2,000 entries at depth 10,000. A version that timed a few
hundred cold admissions measured the wrong path entirely and reported no regression. The saturation
loop is the benchmark.

### The original phase-2 write-up

The current `Buffer` is one ring with an internal-trace quota carved out of it, and that carve-out
costs more than it looks:

- `oldestInternalLocked` is a **linear scan of the whole ring**, run on every internal admission once
  the quota is full — every health poll, every SSE reconnect, every 1 Hz UI poll. At capacity 1000 it
  is tolerable. At a 10,000 ceiling it is a 10,000-slot scan per health check.
- `replaceLocked` relocates the head occupant into the victim's slot to keep FIFO fairness. Correct,
  but it means **ring position no longer tracks age**, which forecloses every cheap ordered operation
  — including the age cull rule 2 needs.

Splitting the one ring into three removes both problems rather than working around them:

| Ring | Cap | Holds |
| --- | --- | --- |
| `live` | floor → ceiling | ordinary user-facing traces |
| `pinned` | 1000 | traces that went wrong |
| `internal` | 200, absolute | `isInternalPath` traffic |

Each is a plain insertion-ordered ring whose only eviction is *advance head*. So:

- Position tracks age again, exactly, within each ring.
- `oldestInternalLocked` and `replaceLocked` both **delete**. The internal quota enforces itself by
  the ring being small, and FIFO fairness is what a ring does natively once it is not shared by two
  populations.
- The internal cap becomes absolute rather than `capacity/5`, which at a 10,000 ceiling would
  otherwise mean retaining 2,000 health polls.
- The age cull is `advance head while oldest is outside the window and len > floor` — O(culled), no
  scan, no sweeper goroutine, no timer. Evaluated lazily on `Add`.

Implement it once as an unexported `ring` type, used for each population. They differ in capacity and
policy, not mechanics, and hand-rolled head/tail pairs are how they drift. Shipped with `push`, `at`,
`len` and `cap`; `cullBefore` arrives with the window in phase 4, since nothing calls it before then.

**One consequence to state plainly:** the internal ring is sized *alongside* the floor rather than
carved out of it, so a buffer asked for 1,000 retains 1,000 user-facing traces and up to 200 internal
ones, where it previously retained 1,000 of both mixed. Idle polling no longer costs a user-facing
trace at all. `Capacity()` reports the sum so that `Len()` can never exceed it.

### Admission, promotion, and what the index holds

**A trace cannot be classified when it is admitted.** `Add` runs at request start: the status is not
known yet, and for a CloudFormation deploy a hop can fail minutes later on a goroutine that outlives
the request. So a trace enters `live` and may have to move.

**Classify at eviction, not at admission.** When the `live` ring is about to drop its head, ask
whether it qualifies as pinned; if it does, push it to `pinned` instead of losing it. That is O(1),
needs no scan, no sweeper and no timer — and it is the *last* possible moment, which is exactly when
an asynchronous failure has had the most time to arrive. A trace that fails after it was already
evicted was never going to be saved by an earlier check.

**Promotion pulls children out of the middle of `live`.** Family pinning cannot wait for each child
to reach its own eviction, so it has to reach into the ring — and removing from the middle of a
positional ring leaves holes, which is exactly the property phase 2 buys.

Resolve it by letting a Recorder live in **both** rings:

- `index` is `map[string]*Recorder`, not a map to a slot. Lookup never has to know which ring holds
  what, and dual membership costs nothing.
- The rings hold pointers purely for age ordering and eviction policy.
- Eviction from `live` is still just "advance head": it drops one reference. A Recorder that is also
  pinned survives on the other one, and the garbage collector settles the rest.
- A `pinned bool` on the Recorder decides whether the index entry goes when `live` drops it. That is
  the whole bookkeeping.

An earlier draft had `index` map to `{ring uint8; pos int}`. That is wrong here: it assumes a trace
is in exactly one ring at exactly one position, and both halves stop being true the moment a family
is pinned.

### `ListSummaries` must change with it

Today it allocates a `Summary` slice sized to the entire ring, materialises every retained trace,
then `sort.Slice`s the lot, then takes `limit`. At 1,000 that is the 38–120 µs measured in
[trace-deep-search.md](./trace-deep-search.md). At a 10,000 ceiling it is ~10× the scan plus a larger
sort, run once a second by the UI poll, allocating megabytes of garbage per poll to return fifty rows.

With age-ordered rings it becomes a **three-way merge, newest-first, stopping at `limit`** — O(limit
log 3) plus whatever the filter skips, and an allocation sized to `limit` rather than to the ring.
The deep ceiling then costs nothing on the read path, which is the precondition for raising it.

One caveat to encode rather than discover: `Add` is called at request start, so insertion order and
`Summary.Timestamp` agree only to within the microseconds between `clk.Now()` and `buf.Add` under
concurrency. Gather `limit` plus a small slack and sort that window, rather than trusting positional
order to be exactly timestamp order.

**Behaviour-preserving at default settings.** Benchmarks must show `ListSummaries` flat or better at
capacity 1000 before the ceiling is raised in phase 4.

---

## Phase 3 — stop duplicating hop bodies ✅ shipped

`AddHop` retains no bodies; `Buffer.Get` resolves them from each hop's own trace via
`inlineHopBodies`, in two passes so the index lookups take `b.mu` and the copying takes only each
callee's lock. `MaxHopBodyBytes` and `MaxHopBody` are gone, replaced by `MaxInlinedHopBodies` —
**the bound moved from what the ring retains to what one response carries**, which is the change in a
line. `OmitEvicted` covers a callee that is no longer retained, and `Hop.Parent` was deleted
alongside it, as the rework recorded.

**`trace-budget` was predicted to disappear and did not.** It survives, repurposed: the same reason
now means "not inlined into this view", not "not retained". That is a better sentence than the one it
replaced — nothing has been deleted, and the notice already links to where the body is — but the
prediction was wrong, and the constant it names changed underneath it.

Two things the write-up below did not anticipate:

- **Deep search changed shape, not coverage.** It scanned hop bodies, which no longer exist, so
  `MatchHopResponse` / `MatchHopRequest` are gone with them. The bytes are still found — once, on the
  trace that owns them, rather than twice — so the ECR `ImageNotFoundException` that motivated deep
  search now matches the ECR call's own trace instead of the deploy's hop, with `hopsFor` linking
  back. `TraceMatchField` in the web types is a strict union precisely so a change here surfaces as a
  compile error rather than an unlabelled badge, and it did.
- **The memory claim is not benchmarkable as an allocation.** Before this, a hop under 1 MiB stored
  the caller's slice *without copying it*, so `allocs/op` is unchanged either way — what changed is
  what stays reachable. A benchmark would report "no difference" while measuring the wrong thing, so
  the guarantee is a structural test (`TestAddHop_keepsEveryHopAndNoBodies`), not a number.

### The original phase-3 write-up

Keep the `RequestID` the hop already carries; resolve the body from the callee's trace on read.

This deletes `MaxHopBodyBytes` and the `trace-budget` `OmitReason` with it, halves the retained bytes
for exactly the hop-heavy deploy traces this plan is sized around, and removes duplicate work from
`DeepSearch`, which currently scans both copies.

**It must land before the ceiling is raised**, because it is what makes the raise affordable.

Three constraints:

1. **The reader must not notice.** The detail endpoint resolves references before serving, so the UI
   keeps seeing one trace with its bodies inline. No UI change.
2. **A missing callee degrades honestly.** If the child trace is gone, the hop reports a new
   `OmitReason` — `evicted` — rather than an empty panel or a broken link. Phase 1's rendering
   already handles an arbitrary reason, so this is a constant, not a component.
3. **Eviction ordering.** A child trace is created *during* the parent's processing, so it is newer,
   so FIFO evicts it later: a retained parent implies retained children today. Rule 3's pinning
   breaks that, which is the next section.

**This deletes code phase 1 shipped** — the `trace-budget` branch of `describeOmission` and its
tests. That is not waste: phase 1 also fixed `size` and `streaming`, which remain, and the surfacing
contract is what lets phase 3 introduce `evicted` without touching a component.

### Pinning is family-aware; eviction stays dumb

The tempting version is to make eviction follow the relationships and keep whatever is still
referenced. That turns eviction into reachability: you can no longer decide whether the entry at the
head is evictable without knowing who points at it, so the O(1) head advance becomes a mark phase —
on the hot path, at a 10,000 ceiling, against a 1 Hz poll. **Rejected for that reason.**

Doing it at pin time costs nothing by comparison. The graph is already materialised in both
directions — `Entry.ParentRequestID` on the child and `Recorder.hopRequestIDs` on the parent, already
an O(1) map built for the `HopsFor` filter — and pinning happens once, on the rare path, for failures
only.

**Pin every child of a pinned parent, and let bytes be the bound.**

An earlier draft pinned the failing hops and the five before each, on the reasoning that the cause is
usually upstream of the symptom. That reasoning is right and the mechanism is wrong: **ordinal
adjacency does not track causation.** CloudFormation provisions in dependency order, but adjacent
hops need not be related — a `PutBucketPolicy` at hop 347 can fail because of a bucket created at hop
12, and a changeset with hundreds of resources makes that the normal case rather than the exotic one.
Raising five to fifty moves an arbitrary line without making the window mean anything.

The relation that *would* mean something — same logical resource, or dependency ancestor — is not
available: the dispatch funnel `internalCall.do` does not carry a logical resource ID, so a hop
cannot say which template resource it was materialising. See
[what would make this precise](#what-would-make-this-precise).

So pin the whole family, because it is cheap. A 3,000-hop deploy's children are ~2–5 KiB each — 6 to
15 MB, which is noise against a 512 MiB backstop. The case that is not noise is inline Lambda code,
bounded at 1 MiB per body by `maxTraceBody`, so ~100 Lambda functions is ≤200 MB; that is exactly the
pathological shape the backstop exists to catch, and it catches it by evicting oldest-non-pinned
first rather than by a guess made in advance.

Consequence for the `pinned` ring: its cap is **1,000 pinned *parents*, with families riding along**,
bounded overall by bytes — not a flat 1,000 entries. A failure and the calls it made are one unit of
retention, and counting them separately would let a single deploy exhaust the failure budget.

---

## Phase 4 — the retention policy

Ceiling, window, pinning, the byte backstop, the lazy age cull, and the config knobs. Retention
counters feeding the count endpoint, and the UI that surfaces them:

**Buffer edge.** The list is infinite-scroll, so the honest place is the end of it: scrolling past the
oldest trace reaches a terminal bar rather than nothing — *"1,204 older traces dropped · aged out
after 1h · oldest retained 14:22:07."* Needs counters on the `Buffer` (dropped-by-reason,
oldest-retained timestamp), which fold into the existing count endpoint at
[debug.go](../../internal/router/debug.go). It is also what you would want pasted into a bug report.

**Trace row.** A pinned trace gains a *kept: error* badge, so "why is this still here?" has an answer.

### The byte backstop, and why it is only a backstop

After phase 3 the worst-case trace is ~2.4 MiB, almost all of it the request and response bodies. Ten
thousand of those is ~24 GiB — which needs 10,000 requests each carrying ~1 MiB, and a seeding script
that uploads ten thousand 1 MiB objects to S3 does exactly that. It is not the common case, but it is
not exotic either, and it is the one way raising the ceiling could hurt someone.

So: a byte budget that evicts oldest-non-pinned first when it binds. Default 512 MiB. It is a guard
rail on the ceiling raise, **not** the mechanism the design rests on — in the original draft it was
the primary bound, and de-duplication is what demoted it.

**Do not account for it on the write path.** The obvious implementation keeps a running buffer total
updated from `AddHop` and `AddLog`, which are the hottest paths there are during a deploy — a
cross-object atomic touched thousands of times per trace, to maintain a number consulted rarely.

De-duplication removes the need. Once hop bodies are references, a trace's size is dominated by its
own request and response bodies, both of which are final when `SetResponse` returns; everything that
grows afterwards is hop *metadata* and capped log entries. So take the measurement once, when the
response is recorded, and treat later growth as the small, bounded term it is. The budget check then
reads a per-Recorder number that nothing on the hot path has to maintain.

---

## What this no longer needs

Recorded because deleting a phase is a decision, not an omission.

- **The shrink rule is gone.** An earlier draft had a fourth phase that shed successful hops' bodies,
  sub-warn logs and stacks under pressure, to stop a pinned deploy trace being unbounded. After
  de-duplication a pinned trace is bounded at ~2.4 MiB by caps that already exist, so there is
  nothing left to shed.
- **The five-hops-before-a-failure window is gone too**, and with it the last place this design tried
  to guess which calls mattered. Adjacency is not causation; see phase 3. What replaced it is pinning
  the whole family and bounding it by bytes.
- **The byte budget stopped being the point.** It was the primary bound because per-trace size spread
  four orders of magnitude; removing the duplicated hop bodies collapsed most of that spread.
- **Relationship-aware eviction was considered and rejected**, in favour of family-aware pinning.

---

## Testing

Failing-test-first throughout, per [AGENTS.md](../../AGENTS.md).

- **The scenario test is the acceptance criterion**, and it is a restatement of the promise at the
  top: six thousand traces, three of them failures placed in the first two hundred, then advance the
  fake clock two hours. The three failures are still retrievable at full fidelity, **and so are the
  calls they made** — the hop bodies resolve rather than reporting `evicted`. Write it first, before
  any of phases 2–4.
- A pinned parent with more children than the pinned ring would hold under a count-based cap: the
  family stays together, and it is the byte backstop that bounds it, not the parent count.
- Ring mechanics: wrap, cull, cap, and the index staying consistent across all three rings under
  `-race`.
- The cull is lazy, so it needs a test that it happens on `Add` and that a buffer nobody writes to
  does not cull. Both behaviours are intended; neither is obvious.
- De-duplication: a resolved hop body is byte-identical to what the old copy held; an evicted callee
  yields `evicted` and not an empty body; a pinned parent keeps its failure-window children.
- `clock.Clock` throughout — the window makes this the first part of `trace/` that is time-dependent,
  and a wall-clock test here would be flaky by construction.
- Bench `ListSummaries` at 1,000 and 10,000, and `Add` on the internal path, which is the one the
  removed linear scan was hurting.

## How the links actually work, and what would make them precise

Worth writing down, because the obvious guess is wrong. **Traces already navigate in both
directions**, and none of it uses `Hop.Parent`:

| Direction | Mechanism |
| --- | --- |
| Parent → child | `Hop.RequestID`, rendered as a link to that call's own trace |
| Child → parent | `Entry.ParentRequestID`, set from the `X-Overcast-Parent-Request-Id` header by `linkChildRequest` — the *Called by* line |
| "who called me?" | the `hopsFor` list filter — `HasHop` over `Recorder.hopRequestIDs`, an O(1) map built for exactly this |

`Hop.Parent` is not part of that and never was. It expresses *intra-trace hop nesting* — hop B issued
during hop A — and `internalCall.do` dispatches flat, so nothing has ever produced nesting. Nothing in
Go sets it and nothing in `web/src` reads it, unlike `Hop.Noisy`, which is dead in Go but genuinely
wired to a UI control.

- **Delete `Hop.Parent`** (and `parent?: string` from `TraceHop`). Its navigation job is done, by other
  fields; keeping an always-absent field on a public debug payload invites exactly the mistake of
  reaching for it. Fold it into phase 3, which is already restructuring `Hop`.
- **Carry the logical resource ID** through `internalCall` to the hop. The provisioner knows which
  template resource it is materialising at the point it dispatches; the hop does not record it, so a
  reader cannot ask "show me every call for `MyBucket`" and neither can the retention policy.

The second is what would let "pin the family" narrow to "pin the failing resource and what it depended
on", and would let the hops tab group 300 calls into 40 resources — the difference between a timeline
a person can read and one they scroll past. Not required for phases 2–4; a change at one call site.

## What this does not do

- **No persistence.** Traces stay in memory and still die with the process. An hour of retention is
  not durability, and a developer who restarts the emulator loses the deploy either way. Worth doing;
  not this plan.
- **No cross-trace deduplication.** Collapsing the thousand near-identical `DescribeStacks` polls in a
  deploy into one row with a count is the obvious next win, and `Hop.Noisy`
  ([trace.go](../../internal/trace/trace.go)) is already plumbed to the UI for it — the flow map's
  *hide N noisy* toggle is wired and working, and **no Go code has ever set the flag**, so that
  control is dead today. Phase 3 makes this more attractive, not less: once hop bodies are references,
  collapsing repeats costs only metadata.
