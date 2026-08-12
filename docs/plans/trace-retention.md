# Trace retention — keeping the thing that broke until someone looks at it

> **Status:** phase 1 complete. Phases 2–4 proposed; no retention behaviour has
> changed yet, and the buffer is still one ring with today's floor of 1000.
> **Scope:** `internal/trace/`, `internal/middleware/debugtrace.go`, `internal/router/debug.go`, `internal/bff/bff.go`, `web/src/features/debug-traces/`, `web/src/routes/debug/traces/`.
> **Audience:** any contributor or agent. Read [CONTRIBUTING.md](../../CONTRIBUTING.md) and [AGENTS.md](../../AGENTS.md) first; all their rules apply.

---

## The problem

A CDK deploy dispatches thousands of requests through the emulator in a couple of
minutes. The ring buffer holds a thousand. So the deploy overruns it, and what
survives is decided by arrival order: the newest thousand.

That is the wrong thousand. A deploy that fails does not stop — it rolls back,
and rollback is *chatty*: `DescribeStacks` polls, `DeleteStack`, resource
teardown, each one a trace. The failure is early and the noise is late, so
recency eviction reliably keeps the teardown and discards the `400` that
explains why any of it happened.

The buffer is sized by `OVERCAST_DEBUG_TRACE_BUFFER`, and that does not rescue
anyone. By the time you know you needed a bigger buffer, the trace that told you
is gone. A knob you have to have already turned is not a knob a developer has.

There is a second, quieter version of the same failure already shipped, and it is
**specific to hops**. At the entry level the UI does surface loss: `HeadersBodyView`
renders *(truncated at 1 MiB)* next to the body and a banner for a streamed
response. At the hop level it surfaces nothing. `TraceHop` in
[web/src/types/trace.ts](../../web/src/types/trace.ts) does not declare
`requestBodyTruncated` or `responseBodyTruncated` at all, and both hop-body
renderers are guarded on `{hop.requestBody && …}` — so a hop whose body was
dropped renders *no section whatsoever*, indistinguishable from a hop that
carried no body.

That is exactly backwards from where the loss happens. The entry-level cap is
1 MiB and almost nothing reaches it. The per-trace hop budget is 8 MiB, and a
CDK deploy trace reaches it reliably — after which **every subsequent hop in that
trace loses both bodies, silently**. The one place a deploy actually deletes
context is the one place we never say so.

A related conflation sits under it: `SetResponse` sets `ResponseBodyTruncated`
for a *streaming* response, where no body was captured at all. The UI happens to
render that correctly only because it reads the separate `Streaming` field first.
One flag means three different things already.

## The numbers this design is built around

| Bound | Value | Source |
| --- | --- | --- |
| Traces retained | 1000 | `NewBuffer`, `OVERCAST_DEBUG_TRACE_BUFFER` |
| Internal-trace quota | capacity / 5 | `Buffer.Add` |
| Request / response body | 1 MiB each | `maxTraceBody` |
| Single hop body | 1 MiB | `MaxHopBody` |
| Hop bodies per trace | 8 MiB | `MaxHopBodyBytes` |
| Log entries per trace | 500 | `AddLog` |
| Hop stacks per trace | 20 + 20 failed | `MaxHopStacks`, `MaxFailedHopStacks` |

Worst case per trace is therefore ~10 MiB, and worst case for the ring is ~10 GiB
— a bound nobody reaches, because a typical `DescribeStacks` trace is about 2 KiB.
**That four-order-of-magnitude spread is the whole design problem.** A count-based
bound has to be set for the worst case and is then absurdly conservative for the
real one. It is why the ceiling below is a byte budget and the count is only a
backstop.

---

## The policy

Three rules. A developer should be able to hold all of them at once.

**1. The newest 1,000 traces are always retained, at full fidelity.** Unchanged
from today. This is the floor, and nothing below reduces it.

**2. Beyond the floor, traces are retained for an hour, up to a ceiling.** A
deploy that produces 6,000 traces keeps all 6,000 while you investigate. An hour
later the overflow is culled back to the floor. In overcast a deploy is not
waiting on real CloudFront or RDS provisioning, so it completes in minutes — the
hour is not sized for the deploy, it is sized for the gap between the deploy
failing and a human opening the trace UI. It covers a coffee break and a meeting.
It does not cover overnight, which is what rule 3 is for.

**3. Traces that went wrong are exempt from both, up to their own cap.** A trace
with `StatusCode >= 400`, a non-empty `AWSErrorCode`, or any hop satisfying the
existing `hopFailed` is *pinned*: not culled by the floor, not culled by the hour.
Failures are rare by construction — a clean deploy has none and a broken one has a
handful — so pinning a thousand of them costs nothing in the common case and
converts "probably still there" into "still there".

### Defaults

| Knob | Default | Meaning |
| --- | --- | --- |
| `OVERCAST_DEBUG_TRACE_BUFFER` | `1000` | Floor. Existing name, existing meaning, still an override nobody needs to set. |
| `OVERCAST_DEBUG_TRACE_CEILING` | `10000` | Backstop on retained count. |
| `OVERCAST_DEBUG_TRACE_WINDOW` | `1h` | How long overflow above the floor survives. |
| `OVERCAST_DEBUG_TRACE_BYTES` | `256MiB` | The real bound. Shrinks traces before it drops them; see phase 3. |
| `OVERCAST_DEBUG_TRACE_PINNED` | `1000` | Cap on pinned failures. |

Deliberately **not** added: a burst-detection knob. An earlier draft opened a
larger buffer on a detected arrival-rate spike. It was rejected — the detector is
stateful, hard to test, and its failure mode is losing data on exactly the
unusual traffic it exists to catch. "Keep the last N, keep failures, cull
overflow after an hour" needs no detector.

---

## Hop bodies are duplicates — the budget guards a copy

Found while verifying phase 1 against a running instance, and it supersedes part
of what follows.

There is exactly one `AddHop` call site in the codebase — `dispatch` in
[internal/services/cloudformation/provisioner.go](../../internal/services/cloudformation/provisioner.go).
It always dispatches through `router.ServeHTTP`, and it always sets
`RequestID: childID` from `linkChildRequest`. **Every hop is therefore a
router-dispatched request with a trace of its own**, unconditionally. There is no
such thing as a hop without one.

So the `Hop.RequestBody` / `Hop.ResponseBody` stored on the parent are a second
copy of bodies the ring already holds. Measured on a live instance: a CreateStack
whose template declares 12 SSM parameters of ~900 KiB produced a parent trace
that spent its whole 8 MiB budget on the first nine hops and dropped the bodies of
hops 10–12 — while each of those hops' own traces still held its 921,668-byte
body in full, untruncated.

That reframes `MaxHopBodyBytes`. It is not protecting the ring from a deploy's
data; it is protecting it from a *duplicate* of that data which need not exist.
The better fix is to stop duplicating: keep the `RequestID` the hop already
carries and resolve the body from the callee's trace on read. That would

- delete `MaxHopBodyBytes` and the `trace-budget` reason with it,
- roughly halve the retained bytes for exactly the hop-heavy deploy traces this
  plan is sized around, which changes the numbers the byte budget below is set
  against, and
- remove duplicate work from `DeepSearch`, which currently scans both copies.

**It should land before the byte budget**, since it changes what the budget is
sizing. Two constraints it must respect:

1. **Eviction ordering.** A child trace is created *during* the parent's
   processing, so it is newer, so FIFO evicts it later: a retained parent implies
   retained children today. Pinning (rule 3) breaks that — a pinned failure could
   outlive unpinned children — so pinning a parent must pin the hops it
   references, or the reference must degrade to "the body is no longer retained"
   rather than to a broken link.
2. **The reader must not notice.** The detail endpoint should resolve references
   before serving, so the UI keeps seeing one trace with its bodies inline.

Until that lands, phase 1's notice tells the reader the body is on the hop's own
trace and links to it.

## Structure: three rings, not one

The current `Buffer` is one ring with an internal-trace quota carved out of it,
and that carve-out costs more than it looks:

- `oldestInternalLocked` is a **linear scan of the whole ring**, run on every
  internal admission once the quota is full — that is every health poll, every
  SSE reconnect, every 1 Hz UI poll. At capacity 1000 it is tolerable. At 10,000
  it is a 10,000-slot scan per health check.
- `replaceLocked` relocates the head occupant into the victim's slot to keep FIFO
  fairness. Correct, but it means **ring position no longer tracks age**, which
  forecloses every cheap ordered operation — including the age cull this plan
  needs.

Splitting the one ring into three removes both problems rather than working
around them:

| Ring | Cap | Holds |
| --- | --- | --- |
| `live` | floor → ceiling | ordinary user-facing traces |
| `pinned` | 1000 | traces that went wrong |
| `internal` | 200, absolute | `isInternalPath` traffic |

Each is a plain insertion-ordered ring whose only eviction is *advance head*. So:

- Position tracks age again, exactly, within each ring.
- `oldestInternalLocked` and `replaceLocked` both **delete**. The internal quota
  enforces itself by the ring being small, and FIFO fairness is what a ring does
  natively once it is not being shared by two populations.
- The internal cap becomes absolute rather than `capacity/5`, which at a 10,000
  ceiling would otherwise mean retaining 2,000 health polls.
- The age cull is `advance head while oldest is outside the window and len >
  floor` — O(culled), no scan, no sweeper goroutine, no timer. Evaluated lazily
  on `Add`.

Implement it once as an unexported `ring` type with `push`, `oldest`,
`cullBefore` and `len`, used three times. The three differ in capacity and
policy, not in mechanics, and three hand-rolled head/tail pairs is how they drift.

`index` becomes `map[string]slot` where `slot` is `{ring uint8; pos int}` — one
lookup, one small struct, no second map.

### `ListSummaries` must change with it

Today it allocates a `Summary` slice sized to the entire ring, materialises every
retained trace, then `sort.Slice`s the lot, then takes `limit`. At 1,000 that is
the 38–120 µs measured in [trace-deep-search.md](./trace-deep-search.md). At a
10,000 ceiling it is ~10× the scan plus a larger sort, run once a second by the
UI poll, allocating megabytes of garbage per poll to return fifty rows.

With age-ordered rings it becomes a **three-way merge, newest-first, stopping at
`limit`** — O(limit log 3) plus whatever must be skipped by the filter, and an
allocation sized to `limit` rather than to the ring. The deep ceiling then costs
nothing on the read path, which is the precondition for raising it at all.

One caveat to encode rather than discover: `Add` is called at request start, so
insertion order and `Summary.Timestamp` agree to within the microseconds between
`clk.Now()` and `buf.Add` under concurrency. Gather `limit` plus a small slack
and sort that window — O(limit log limit) on a tiny window — rather than trusting
positional order to be exactly timestamp order.

---

## What a pinned trace keeps when it must shrink

Pinning cannot mean unbounded: one failed deploy trace can carry thousands of
hops. Under byte pressure a pinned trace **shrinks before anything is dropped**,
and the rule is:

> Full fidelity in a window around each failure. Metadata-only everywhere else.

**Never shed:**

- The `Entry` envelope — timing, method, path, host, service, operation, region,
  status, `Stack`, `AWSErrorCode`, `AWSErrorMessage`.
- The failing request's own request *and* response bodies. Both are small for an
  error response and they are the first two things anyone reads.
- Request headers. A large share of AWS errors are signing and content-type
  problems, and the evidence is a header.
- Every hop's metadata: order, service, operation, status, duration, error,
  request ID, timestamp. Already the policy under `MaxHopBodyBytes`, and right.
- The failing hop's full bodies, headers and stack.
- **The five hops preceding each failure**, at full fidelity. The cause is
  usually upstream of the symptom; this is the part that is easy to forget to
  protect and expensive to have lost.
- `LogEntries` at warn and above.

**Shed, in this order:**

1. Response bodies of *successful* hops outside a failure window. Essentially all
   the bytes live here — the successful `DescribeStacks` polls in a deploy that
   died on `CreateBucket`.
2. Their request bodies.
3. `LogEntries` below warn, oldest first.
4. Hop stacks outside the failure windows.
5. Successful hops' headers.

A property worth stating because it is load-bearing for the UI: the waterfall,
sequence diagram and flow map are built entirely from hop *metadata*, which is
never shed. **Those views stay complete on a maximally shrunk trace.** Only body
panels degrade, and they say so.

`hopFailed` already exists and already means exactly "a hop a reader would go
looking for". Reuse it — for the pinning predicate, for the failure windows, and
for the shed ordering. One definition of "went wrong", or the three will drift.

---

## Making the erasure legible

The governing rule, and the one that fixes today's silent loss:

> **An omitted body must never render the same as an absent one.**

### Wire format

The existing booleans conflate causes that read very differently to a person —
"this was 4 MiB", "we never captured it because it streamed" and "we reclaimed it
to fit" are not the same sentence, and the first two are conflated *today*.
Replace the flag with a reason, per body:

```go
type OmitReason string // "" | "size" | "trace-budget" | "streaming" | "reclaimed"
```

`Hop` and `Entry` carry `RequestBodyOmitted` / `ResponseBodyOmitted`. The existing
`*Truncated` bools stay in the JSON, derived, for compatibility — both types
already marshal through shadow structs, so deriving them is a line each and no new
machinery. `reclaimed` is the only value with no producer until phase 4, so it is
added there rather than sitting unused.

`Entry` gains a `Retention` object: class (`live` | `pinned` | `internal`), what
was shed, and bytes reclaimed. `Summary` gains only `pinned` and `shrunk` bools —
summaries go out fifty at a time at 1 Hz, so the list payload stays cheap.

`GET /_overcast/debug/traces/count` extends to carry the buffer-wide picture:
counts dropped by reason, oldest retained timestamp, per-ring occupancy, bytes
used against budget. It is already proxied by the bff and already polled by the
list page, and it is what you would want pasted into a bug report.

### UI

Three scopes, because context is dropped at three scopes.

**Buffer edge.** The list is infinite-scroll, so the honest place is the end of
it: scrolling past the oldest trace reaches a terminal bar rather than nothing —
*"1,204 older traces dropped · aged out after 1h · oldest retained 14:22:07"*.
The page header at [index.tsx:207](../../web/src/routes/debug/traces/index.tsx)
already renders `N of M buffer slots used` and extends naturally to name the
pinned count.

**Trace row.** A shrunk trace keeps its row and gains a badge — *summary only*.
A pinned one gains *kept: error*. A trace never silently becomes a different-
looking trace.

**Field.** ✅ shipped in phase 1. A partial loss annotates the body that is still
on screen with an inline chip; a total loss renders a notice in place of it,
naming the limit and saying what survived. Because hop metadata survives, a hop
whose body was dropped still shows its section in the hops tab rather than
vanishing. The timeline stays honest about what happened; only the payload is
gone, and it says so.

The split is by whether a prefix survived, not by which reason applied — so
phase 4's `reclaimed` needs no new rendering, only the new constant.

---

## Phases

Ordered so that the phase which fixes a live bug ships first and independently.

### Phase 1 — surfacing what is already dropped ✅ done

No retention changes. `OmitReason` in [internal/trace/omission.go](../../internal/trace/omission.go)
with its four producers, the `*Truncated` bools derived from it in both
`MarshalJSON`s, `describeOmission` plus `BodyOmissionChip` / `BodyOmissionNotice`
in the web feature, and hop-body sections that render when the body is *absent
but explained* rather than only when present.

This fixes the silent hop-body loss against today's `MaxHopBodyBytes` — the bug a
CDK deploy hits every time — and establishes the contract the later phases report
through.

Three things worth keeping from doing it:

- **The duplication was in the tests, not just the UI.** Four existing tests
  already asserted the boolean at each producer, so the new coverage is the wire
  shape only (`omission_test.go`); the rest became assertions on *which* reason,
  which is strictly more than they checked before.
- **Both hop-body renderers collapsed into one `HopBodySection`.** They were
  already near-identical, differing only in a label colon and a redundant
  `text-xs`. Fixing the bug in two places would have been the third copy.
- **`reclaimed` was left out.** It has no producer until phase 4, and an enum
  value nothing emits is a dead end a reader has to chase.
- **Verifying it against a running instance is what found the duplication.** The
  unit tests were green and said nothing about it; reaching `trace-budget` with
  real traffic meant looking at a hop's own trace, which is where the second copy
  of the body was sitting. See the section above — it changes phase 3.

### Phase 2 — three rings

The `ring` type, the split, the deletion of `replaceLocked` and
`oldestInternalLocked`, the absolute internal cap, the merge-based
`ListSummaries`. Behaviour-preserving at default settings: same floor, same
fidelity. Benchmarks must show `ListSummaries` flat or better at capacity 1000
before the ceiling is raised in phase 3.

### Phase 3 — the policy

Ceiling, window, pinning, byte budget, lazy age cull, and the config knobs.
Retention counters feeding the count endpoint.

### Phase 4 — shrink

`Recorder.Shrink`, failure windows, shed ordering, `Retention` on the entry, and
the badges. Last because it is the only phase that needs all the others in place
to be observable.

---

## Testing

Failing-test-first throughout, per [AGENTS.md](../../AGENTS.md).

- **The scenario test is the acceptance criterion.** Six thousand traces, three
  of them failures placed in the first two hundred, then advance the fake clock
  two hours. The three failures are still retrievable at full fidelity, with
  their preceding-hop windows intact. This test is the entire point of the plan
  and should be written first, before any of it.
- Ring mechanics: wrap, cull, cap, and the index staying consistent across all
  three rings under `-race`.
- The cull is lazy, so it needs a test that it happens on `Add` and that a buffer
  nobody writes to does not cull. Both behaviours are intended; neither is
  obvious.
- Shrink: idempotent, never sheds inside a failure window, never sheds the
  envelope, and reports what it shed.
- `clock.Clock` throughout — the window makes this the first part of `trace/`
  that is time-dependent, and a wall-clock test here would be flaky by
  construction.
- Bench `ListSummaries` at 1,000 and 10,000, and `Add` on the internal path,
  which is the one the removed linear scan was hurting.

## What this does not do

- **No persistence.** Traces stay in memory and still die with the process. An
  hour of retention is not durability, and a developer who restarts the emulator
  loses the deploy either way. Worth doing; not this plan.
- **No cross-trace deduplication.** Collapsing the thousand identical
  `DescribeStacks` polls in a deploy to one row with a count is the obvious next
  win, and `Hop.Noisy` is already plumbed to the UI for it — the flow map's *hide
  N noisy* toggle at [flow-map.tsx](../../web/src/features/debug-traces/components/flow-map.tsx)
  is wired and working, and no Go code ever sets the flag, so it is dead today.
  The classifier that lights it up is a natural follow-on once retention is
  no longer deciding by age alone.
