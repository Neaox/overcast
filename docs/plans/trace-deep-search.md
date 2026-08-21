# Trace search — finding a request by what it says, not just what it is

> **Status:** all three phases complete and verified in a browser against a built image. Phase 1 — `Recorder.MatchesSearch`, benchmarked. Phase 2 — `Buffer.DeepSearch`, `GET /_overcast/debug/traces/search`, the bff proxy. Phase 3 — `useDeepSearch`, match rows with excerpts, the `hop` deep link, and the corrected empty state.
>
> One thing this document did not predict, recorded because it cost a debugging round: **TanStack Pacer takes no reactive state subscription unless you give `useDebouncedValue` a selector.** Without one the settled value updates inside the debouncer and never re-renders the component, so the search runs once — for whatever the query happened to be at the last unrelated render — and never again. It looks like a caching bug and is not one.
> **Scope:** `internal/trace/`, `internal/router/debug.go`, `internal/bff/bff.go`, `web/src/routes/debug/traces/`.
> **Audience:** any contributor or agent. Read [CONTRIBUTING.md](../../CONTRIBUTING.md) and [AGENTS.md](../../AGENTS.md) first; all their rules apply.

---

## The problem

A stack failed with `persistent state flush failed: context deadline exceeded`. The message is now logged into the originating request's trace, so it is *in* the ring buffer — and typing it into the trace list's search box returns nothing, because search matched request ID, path and service only.

That is the general shape: everything that explains a failure lives in hop bodies, hop errors and log entries, and none of it was reachable from the one box a person types into. The trace list's empty state made it worse by saying "No traces yet. Send a request to see it here." — a confident answer that was wrong.

## The number this design is built around

| Bound | Value | Source |
| --- | --- | --- |
| Hop bodies retained per trace | 8 MiB | `MaxHopBodyBytes` |
| Single hop body | 1 MiB | `MaxHopBody` |
| Log entries per trace | 500 | `AddLog` |
| Traces retained | 1000 (default) | `NewBuffer`, `OVERCAST_DEBUG_TRACE_BUFFER` |

> **Superseded bounds (2026-08):** [trace-retention.md](./trace-retention.md) phase 3 removed
> `MaxHopBodyBytes`/`MaxHopBody` — hop bodies are no longer duplicated into the parent trace, so
> deep search now matches each body once, on the trace that owns it (`MatchHopResponse`/
> `MatchHopRequest` are gone), and the retained-trace floor/ceiling is now governed by that plan's
> retention policy. The asymmetry argument below still holds; the specific numbers are historical.

**Ceiling for one full deep scan: ~8 GB.** Typical is a thousandth of that. A CDK deploy trace really does reach the cap, and a deploy fills the ring with them — so the same query is sub-millisecond on a quiet emulator and seconds on the one you actually need to debug. Everything below follows from that asymmetry: it is why the cheap half must not wait for the expensive half, and why the expensive half must be interruptible.

---

## Phase 1 — widen what the cheap path matches ✅ done

Search now also matches `Operation`, `AWSErrorCode` and `AWSErrorMessage`, alongside the request ID, path and service it always did. Every one of these is a short string already on the recorder, so the check is one read lock and a few substring tests regardless of how much the trace has recorded.

This alone answers a large class of question that previously needed paging by eye — "the `RunTask` that returned `AccessDenied`" — because the RPC protocols make every request `POST /`, leaving the operation as the only thing that distinguishes them.

Implementation notes worth keeping:

- `Recorder.MatchesSearch` ([internal/trace/trace.go](../../internal/trace/trace.go)) owns the whole match. `matchSummary` no longer duplicates a narrower copy of it; two places deciding what "search" means is how they drift.
- `containsFold` avoids the allocator on the path that matters. Measured on a full 1000-trace buffer, `BenchmarkBufferListSummaries`:

  | Case | Before | After |
  | --- | --- | --- |
  | `searchMiss` | 88 µs, 1002 allocs | **38 µs, 2 allocs** |
  | `searchHit` | 120 µs, 1004 allocs | 118 µs, 1004 allocs |

  The miss case is the one the UI runs constantly — every keystroke of a query that has not matched yet, once a second. It is settled by a length check and a lowercase-field check, both allocation-free. The hit case still lowers one mixed-case field per trace; at 1000 allocations of a short string, once, when results are about to be rendered anyway, that is **deliberately left alone**. A windowed `EqualFold` scan would remove it and is not worth the branch it needs to stay correct for non-ASCII queries.

---

## Phase 2 — the deep scan ✅ done

### 2.1 Not a job. A paginated read.

The tempting design is a search job: `POST /_overcast/debug/traces/search` returns an ID, the client polls it. Rejected — it puts state on the server that has to be garbage-collected, and it races with the ring evicting the very traces the job is walking.

Instead the deep scan is **another page-shaped read**, like the list already is. One request scans backwards from a cursor until it exhausts a work budget, and returns what it found plus the next cursor. The client keeps asking until the cursor is empty.

- No server-side state.
- Cancellation is free: the client stops asking.
- It reuses the infinite-query machinery the list already runs on.
- It interleaves naturally with the 1 Hz live poll.

**The cursor must be a timestamp, not a request ID.** `ListSummaries`' `After` path returns `nil` when its cursor is not found, which is right for a list and catastrophic for a scan: an evicted cursor would silently end the search half way and report "no more matches".

### 2.2 The scan must not copy, and must not hold locks

The buffer holds **live** `*Recorder`s. `Recorder.Entry()` deep-copies bodies and hops — using it to scan would allocate gigabytes to answer one query.

Hop bodies are immutable once `AddHop` returns: it copies on truncation, and nothing writes to the arrays afterwards. So the scan may take slice *headers* out under a brief lock and do the matching outside it:

1. `b.mu.RLock` → copy the `*Recorder` pointers for this budget's slice → unlock.
2. Per recorder: `r.mu.RLock` → copy out body/log slice headers → unlock.
3. Match outside every lock.

That invariant is load-bearing and belongs in a comment on `AddHop`, because the next person to add mutation there will not know this depends on it.

A hop arriving mid-scan may or may not be included. That is correct behaviour for a search over a live system and should be documented rather than defended against — the trace you are debugging is usually still being written.

### 2.3 What gets searched, in priority order

1. Log entry messages and field values ← where the flush failure lives
2. Hop `Error`
3. Hop response bodies ← where the ECR 404 lived
4. Hop request bodies
5. Entry request/response bodies

### 2.4 Budget and exclusions

- Per request: ~64 MiB scanned or ~200 ms, whichever comes first. Small enough that each call feels instant and progress moves visibly.
- Skip internal traces by default — the UI already hides them, and they are a fifth of the ring.
- Substring, case-insensitive, matching Phase 1. **No regex**: someone pasting an error message containing `(` must get results, not a syntax error. A `regex:` toggle can come later if plain search proves insufficient.

### 2.5 Throttling — debounce is necessary and nowhere near sufficient

The box is already debounced: `useDebouncedTextParam` commits to the URL after 300 ms of quiet, so a burst of typing costs one navigation and one list request. That is the right amount of protection for a shallow search costing 38 µs.

It is not remotely enough for a scan that can run for seconds. A 300 ms debounce still launches a full-ring scan for **every prefix the typist pauses on** — `persi`, `persistent`, `persistent state` — each one walking the whole buffer for a query that was never the intended one. Four gates, and each is doing a different job:

1. **A longer settle for the deep phase than the shallow one.** Shallow fires at 300 ms; deep waits until the query has been stable for roughly 800 ms. The cheap answer stays immediate, and the expensive one only chases a query the person has stopped editing.
2. **A minimum query length — 3 characters.** A one- or two-character query matches nearly every body in the ring. The result is a flood that is expensive to produce and worthless to read, and it is never what anyone meant.
3. **Cancel on change.** When the query changes, the scan for the old one stops — see 2.6, which makes this a real abort rather than a client that merely stops listening.
4. **Deep search should not start on its own for a query the shallow pass already answered.** When shallow returns a healthy set, the deep scan is unsolicited work over gigabytes to add rows nobody asked for. Start it automatically only when shallow finds little or nothing — which is exactly the case this feature exists for — and otherwise offer it: `12 matches · also search bodies and logs`. The person then knows what they are asking for before it runs.

**Use TanStack Pacer** (`@tanstack/react-pacer`, not currently a dependency) rather than hand-rolling any of this. The repo is already committed to the TanStack ecosystem — Query, Router, Table, Form, Virtual — and Pacer is the piece of it that owns exactly this problem: debouncing, throttling and rate-limiting with async-aware variants that understand an in-flight call and cancellation, which is the part hand-rolled `setTimeout` code always gets wrong. The async debouncer is the relevant primitive; confirm the hook names against the installed version rather than trusting this paragraph.

Adopting it raises one question worth answering deliberately: `web/src/hooks/use-debounced-value.ts` and `use-debounced-text-param.ts` are hand-rolled and used elsewhere. Either they get reimplemented on top of Pacer, or the codebase carries two debouncing mechanisms and the next person has to work out which one to reach for. Prefer the former — the URL-binding behaviour in `useDebouncedTextParam` is the valuable part and is orthogonal to how the delay is timed.

Server side, the per-request work budget (2.1) is what bounds any single call; a client that keeps asking is by construction asking for one budget at a time, so no separate rate limit is needed. What is needed is that the budget is enforced **inside** the scan loop rather than checked once at the top, or a single trace with 8 MiB of bodies overruns it on its own.

### 2.6 A closed connection must stop the scan, not just the listening

If the browser navigates away, the query changes, or the tab closes, the work has to stop **on the server**, immediately. Otherwise every abandoned keystroke leaves a goroutine scanning gigabytes for an answer nobody will read, and a person typing a long query can pile up a dozen of them.

The chain is three links, and all three already exist:

1. **Browser → bff.** TanStack Query hands `queryFn` an `AbortSignal`. It has to be passed to `fetch`; today's `debugTrace.list` does not take one, so this is a real change in `web/src/services/api/`. Without it, changing the query abandons the *result* but never closes the socket.
2. **bff → emulator.** Already correct: `proxyDebugJSON` calls `doGet(r.Context(), …)` ([internal/bff/bff.go](../../internal/bff/bff.go)), so a client disconnect cancels the request context and Go tears down the upstream call. Verified, not assumed — this is the link most likely to be silently buffered by a proxy, and it is not.
3. **Emulator → the scan loop.** `net/http` cancels `r.Context()` when the client goes away. The scan must **check it in the loop**, and at a granularity that means something: per trace is not enough when one trace holds 8 MiB of bodies, so check between hops too. A budget check and a cancellation check want to happen at the same points, so they should be one condition evaluated in one place.

The acceptance test for this is behavioural, not structural: start a scan over a full buffer, close the connection, and assert the scan goroutine returns promptly rather than running to completion. A test that only asserts `ctx.Err()` is consulted somewhere proves nothing about how long the work actually continues.

### 2.7 Match shape

A match must say *where* it hit, or it is a riddle in a 300-hop trace:

```json
{
  "requestId": "8666db4c-…",
  "field": "log",              // log | hopError | hopResponse | hopRequest | body
  "hopId": "hop-42",           // when the match is in a hop
  "label": "warn · cfn: terminal stack state not yet persisted",
  "excerpt": "…state not yet persisted error=", 
  "match": "context deadline exceeded",
  "excerptAfter": "…"
}
```

Excerpt ~60 characters either side, split into before/match/after so the client highlights without re-searching. **Rune-boundary safe**, and hop bodies may be CBOR: check UTF-8 validity first and degrade to `matched in binary body at offset 1234` rather than emitting mojibake.

---

## Phase 3 — what the user sees ✅ done

```
┌────────────────────────────────────────────────────────────┐
│ 🔍 persistent state flush failed                           │
├────────────────────────────────────────────────────────────┤
│ 2 matches · searching bodies and logs  ▓▓▓▓░░ 612/1000  [Stop] │
└────────────────────────────────────────────────────────────┘

  09:02:14  POST  /  cfn  200  8666db4c…            ← shallow, instant
  09:02:23  POST  /  cfn  200  a41f0c2e…  [log]
     └ warn · cfn: terminal stack state not yet persisted
       …not yet persisted error=**context deadline exceeded**
  09:01:58  POST  /  ecs  500  3d9e77b1…  [hop 42 · body]
     └ ecs.RunTask → 500
       …CannotPullContainerError: …**404 Not Found**…
```

Four decisions, and the reasons they are decisions:

1. **One list, not two.** Deep results insert into the same timeline by timestamp. Relevance ranking would be actively wrong: a trace list is a chronology, and re-sorting it destroys the correlation with a deploy log that a reader is using it for.
2. **A badge naming where it hit.** `[log]`, `[hop 42 · body]`.
3. **The excerpt is the feature.** Without it the result says only that an answer exists somewhere.
4. **The empty state must change.** While a deep search runs with no shallow hits it must say *"No matches in paths or IDs — searching bodies and logs…"*. The current wording is the single most misleading thing in the flow.

### 3.1 The deep link is not optional

Clicking a deep match must open the matching hop, not the top of a 300-hop trace. `selectedHopId` is currently `useState` in [$requestId.tsx](../../web/src/routes/debug/traces/$requestId.tsx) and needs lifting into `validateSearch`, so `/debug/traces/abc?hop=hop-42` opens and scrolls to it.

Without this, the feature tells you the answer exists and then makes you find it by hand — which is the problem it was built to solve.

### 3.2 Interaction with the live poll

New traces arrive at the head while the scan walks backwards. Keep a "deep-scanned as of" timestamp watermark so arrivals are picked up rather than skipped.

---

## Acceptance

- A trace whose only occurrence of the query is in a hop response body is returned, with an excerpt naming the hop.
- A search over a full buffer of deploy-sized traces returns its first page in under ~250 ms and never blocks `AddHop` for longer than one hop's slice-header copy.
- Cancelling (clearing the box, navigating away, closing the tab) stops the work **on the server**, promptly — measured as scan-goroutine lifetime after the connection closes, not as the absence of further requests.
- The empty state never claims there are no traces while a deep search is in flight.
- A cursor whose trace has been evicted resumes rather than truncating.
- Typing a long query one character at a time starts **one** deep scan, not one per pause — provable by counting requests.
