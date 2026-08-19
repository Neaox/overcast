# Logs views — performance at thousands of lines

> Status: investigation complete (2026-08-18), no code changed. Scope is the four surfaces that
> render CloudWatch log events: the stream/all-streams viewer
> (`web/src/features/cloudwatch/logs/components/log-events-viewer.tsx`), the map's log peek
> (`web/src/features/map/log-stream-peek.tsx`), the generic `LogViewer`
> (`web/src/components/logs/log-viewer.tsx`, used by the Lambda monitor tab, the map's
> invocations drawer, and ECS task detail), and the log-group detail page's cross-stream search
> results (`web/src/features/cloudwatch/logs/components/log-group-detail.tsx`).
>
> Goal: display and tail tens of thousands of log lines smoothly, in Chrome, Firefox, Edge and
> Safari, **without losing fidelity** — level badges, ANSI colour, platform-record summaries,
> clear/restore semantics, duplicate-count-preserving tail merge, and StrictMode-safe single
> live-tail sessions are all pinned by tests
> (`log-events-viewer.test.tsx`, `tail.test.tsx`) and must survive every phase.

---

## 1. How the surfaces are wired today

| Surface | Data source | Virtualized? | Live tail | Derived work |
| --- | --- | --- | --- | --- |
| `LogEventsViewer` (stream + all-streams) | one `FilterLogEvents` call, no pagination | yes (`@tanstack/react-virtual`) | yes, per-event `setState` | `rowMeta` re-derived for **all** events on every change |
| `LogStreamPeek` → `LogsPane` | `GetLogEvents` infinite query, 200/page | **no — plain `.map`** | yes, per-event `setState` | `dropTailedDuplicates` over full lists per event |
| `LogViewer` (monitor tab, invocations drawer, ECS) | caller-supplied; monitor tab fetches full filter result and slices last 100 | yes | no | `normalizedEvents` maps **all** events; `tryParseJSON` on every event when Format is on |
| Log-group detail search results | `FilterLogEvents`, no pagination | **no — plain `<Table>`** | no | none |

The emulator caps both `GetLogEvents` and `FilterLogEvents` at **10,000 events per page**
(`internal/services/cloudwatch/logs/typed_logic.go:394,514`) and returns a resumable `nextToken`.
The web client (`web/src/services/api/logs.ts` `filterEvents`) **never reads that token**, so every
`FilterLogEvents`-backed view silently truncates at 10k — and conversely, a single response can be
10k events parsed, held, and re-derived at once.

## 2. Findings, ranked by user-visible impact

### F1 — The log peek is not virtualized (the one surface that isn't)

`LogsPane` (`log-stream-peek.tsx:408-420`) renders every event as DOM. Paging back through a busy
Lambda stream (200/page) plus an open live tail grows the DOM without bound; each row is a flex div
plus `AnsiText` spans. This is the panel most likely to sit open while a load test runs. Symptoms:
long style/layout passes on every append, degrading in every browser (Safari worst — its
incremental layout of large `white-space: pre-wrap` blocks is slowest).

Also **F1b**: row keys embed the array index and a message prefix
(`key={`${e.timestamp}-${slice}-${i}`}`). Loading an older page prepends events, shifting every
index — React remounts the entire list on each backward page.

### F2 — Tail ingestion is O(n²) over a session's lifetime

Both tailing surfaces do, per arriving event:

1. `setTailEvents(prev => [...prev, event])` — an O(n) array copy per event
   (`log-events-viewer.tsx:151`, `log-stream-peek.tsx:111`).
2. `dropTailedDuplicates(fetched, tailed)` — rebuilds a `Map` over **all** fetched events and
   rescans **all** tailed events (`tail.ts:78-101`), per event.
3. `LogEventsViewer` then re-runs `sortEvents` (full O(n log n) copy+sort, line 181) and re-derives
   `rowMeta` — `stripAnsi` + `parsePlatformRecord` + `detectLogLevel` for **every** event, not just
   new ones (lines 187–206).

A tail that has accumulated 10k events pays ~10k units of work for the 10,001st. That is the
"loops running too often" smell: the per-event cost grows with history, so a quiet UI turns janky
precisely when the stream gets busy. React 18 batches same-tick updates so bursts coalesce a bit,
but the derive-everything memos still run over the whole list per batch.

### F3 — `LogViewer` derives everything for off-screen rows

`normalizedEvents` (`log-viewer.tsx:65-85`) maps the full event array on every identity change,
and when Format is on it `tryParseJSON`s **every** event — 10k JSON.parse calls on the main thread
for a view that shows ~30 rows. `LogEventsViewer` already got this right (row-local parse in
`LogMessage`); `LogViewer` predates that and was never aligned.

### F4 — Per-frame work inside visible rows

The virtualizer re-renders every visible row on each scroll frame. Inside those renders:

- `highlightJSON(...)` (Prism) is called **inline in JSX** in `LogViewer` (lines 180, 199) — a
  full Prism tokenize per visible JSON row per scroll frame. In `LogEventsViewer`, `jsonText` is
  memoized but `highlightJSON(jsonText)` at line 713 is not.
- `highlightMatches` (`log-events-viewer.tsx:51-69`) recompiles its `RegExp` and re-parses the
  filter pattern **per text chunk per row per render**. The compiled regex depends only on
  `activeFilter`.
- `LogMessage` is not `memo`ized, so toolbar state changes (e.g. the events-count label) re-render
  every visible row's full message pipeline.

### F5 — Unbounded tail buffers (the real "memory leak")

The effect/AbortController hygiene is actually good: live-tail sessions hang up on unmount, on
target change, and under StrictMode (pinned by `tail.test.tsx`); the document keydown listener is
removed; timers are cleared. What grows without limit is **state**: `tailEvents` /
`appendedEvents` accumulate for as long as tail is on. A tail left open against a chatty stream
grows memory linearly forever and (per F2) CPU quadratically. There is no cap, ring buffer, or
drop counter. Minor adjacent point: `tailLogEvents` leaves its `abort` listener on the signal when
the generator exits without abort (`tail.ts:156-158`) — bounded in practice because the controller
is per-effect, but cheap to remove in `finally`.

### F6 — Layout reads during render

`LogEventsViewer` reads `parentRef.current?.clientHeight` in the render body (line 314) to compute
the syntax-highlight window. A layout read in render forces synchronous style/layout on every
render — during a tail that is every batch. The virtualizer already knows the viewport
(`virtualizer.scrollRect`); use it, or track height with a `ResizeObserver`.

### F7 — Pagination fidelity gaps

- `filterEvents` ignores `nextToken`: the stream/all-streams viewer and group search **silently
  truncate at 10k** matched events. "Thousands upon thousands" needs a page loop.
- The monitor tab fetches the full (up to 10k) filter result and slices the last 100 client-side
  (`monitor-tab.tsx:13`) — it should pass a `limit`. Its footer claims "auto-refreshes every 5 s",
  but no `refetchInterval` exists on that query (global default is `staleTime: 30s`, no interval)
  — the label is wrong today.

### F8 — DRY: four row renderers, two merge pipelines

`log-format.ts` already consolidated the *vocabulary* (its header comment tells the drift story).
The remaining duplication is structural:

- Row rendering exists four times (LogViewer rows, LogEventsViewer's `LogMessage`, LogsPane's
  inline row, log-group-detail's `<Table>` rows — the last with no level tint, no virtualization).
- The fetched+tailed merge/dedup/reset pipeline exists twice with different bugs latent in each
  (`LogEventsViewer` lines 135–184, `LogStreamPeek` lines 73–137).
- Pin-to-bottom / unread-pill logic exists twice (`LogsPane` scroll math, `LogEventsViewer`
  `pinnedToLatestRef`).

### Browser notes (what to verify, per engine)

- **Virtualized `transform: translateY` rows, IntersectionObserver sentinels**: fine everywhere in
  support matrix; keep `threshold: 0` (Safari's IO is timer-coalesced — the 120px rootMargin
  already absorbs that).
- **`overflow-anchor`** is Chrome/Firefox-only; nothing may rely on it for pin-to-bottom — the
  manual pin refs are the right mechanism and must stay.
- **`scrollTo({behavior: "instant"})`** (`log-stream-peek.tsx:338`): older Safari treats unknown
  behaviors as `auto` — same effect, no action needed.
- **Main-thread parse/highlight jank** (Prism, `JSON.parse`, `stripAnsi` regex) is
  engine-independent — the fix is doing less of it (F2/F3/F4), not offloading; a worker is not
  warranted at 10k-row scale once derivation is row-local and cached.
- **Firefox** paints large `color-mix()` backgrounds fine; the ANSI ramp mapping already avoids
  per-row style recalc explosions by collapsing same-style runs.

## 2b. Trace evidence — Firefox profile, 2026-08-18 (stream view, production build)

An 11.8s Firefox performance profile of `/cloudwatch/logs/stream` (1ms sampling, macOS) was
analysed offline (gzipped processed profile → Python aggregation; script reusable). What it shows,
attributed to the tab's content-process main thread:

- **Responsiveness was catastrophic during the capture**: the profiler's `eventDelay` estimate
  exceeded 100ms for 92% of samples and 1s for 71%, peaking at 5.5s. Nine `LongTask` markers
  total ~8.9s of the 11.8s trace.
- **The two biggest stalls are environmental, not app code**: a 5.2s task is entirely a
  password-manager extension's injected content script (`processRecords`/`drainPending` — its
  MutationObserver digesting our DOM mutation storm), and a 2.5s task has no JS frames at all
  (cycle-collector/GC pause; `GCMajor` markers total 1.8s — DevTools' memory panel was open).
  Re-baseline in a clean profile with extensions disabled. But note the app *feeds* both: large
  unvirtualized DOM and index-keyed remounts create exactly the mutation batches that make
  observer-based extensions and the cycle collector melt down. Virtualization and stable keys
  (F1/F1b) shrink the app's extension/GC attack surface, not just its own render time.
- **The app's own jank is the scroll path, confirming F4/F6**: the remaining long tasks
  (80–247ms each, ~1.2s total) are all
  `virtualizer.notify → onChange → ReactDOM.flushSync → render` — @tanstack/react-virtual
  flush-syncs a render on every scroll event while scrolling, so the whole visible-row render
  cost lands synchronously inside the scroll handler. Prism `highlight`, `log-format` helpers,
  and a self-time-heavy function in the `log-events-viewer` chunk (the filter-highlight/regex
  path by elimination) appear inside these windows. The fix is exactly Phase 3: make one
  visible-row render cost <16ms (memoized rows, cached Prism HTML, precompiled filter regex,
  no render-body layout reads); optionally override the virtualizer's `onChange` to drop
  `flushSync` while scrolling if row memoization alone doesn't get under budget (trade-off:
  momentary blank overscan on fast flings).

This reorders priorities: **Phase 3 (row render cost) is co-first with Phase 1**, since it is the
measured source of in-app long tasks; Phase 2 (virtualize the peek) is the main lever on the
extension/CC amplification.

### The extension interaction is ours to fix (mutation budget)

A page cannot stop an extension's document-level MutationObserver, but the observer's cost is
proportional to the mutation records the page produces. The 5.2s password-manager stall is our
churn amplified through their observer, and every churn source has an owner in this plan:
unvirtualized inserts (Phase 2), full-list remounts from index keys (Phase 2), per-event append
batches (Phase 1), and per-scroll re-renders whose rebuilt `dangerouslySetInnerHTML` strings force
node replacement for identical content (Phase 3). Treat **mutation records per scroll tick /
append batch** as a budget alongside render time: unchanged rows must produce zero mutations.

Two additions: the filter inputs get `data-1p-ignore` + `data-lpignore="true"` so password
managers exclude them from field analysis (the page's only fill-candidate elements); and the
**acceptance re-trace runs with extensions enabled** — the clean-profile baseline is for
attribution only, but users run extensions, so "snappy at all times" is judged with 1Password on
and its `processRecords` invisible in the profile. Escalation of last resort if the budget work
falls short: render the list inside a shadow root (document-level observers cannot see into it),
at the cost of fighting global Tailwind styles — not planned, only noted.

## 2c. Prior art — how the AWS console copes

The AWS console's strategy is to bound everything before it reaches the DOM, then be honest about
what was dropped: Live Tail's wire protocol samples to 500 events/second and drops the oldest
events once 10 updates / 5,000 events are buffered. **Verified and fixed**: our emulator's
StartLiveTail buffered 10 *write batches*, not 5,000 events, and silently lost lines at eleven
quiet single-line writes per second — found by this plan's browser verification, fixed with the
buffer counted in events and `sampled: true` on the update after a drop
(`internal/services/cloudwatch/logs/live_tail.go`); the console tail view is a
rolling window that shows a "% displayed" figure when it samples and pauses on click; the events
viewer renders collapsed single-line fixed-height rows with JSON formatting only on per-row
expansion; filtering is always server-side; Logs Insights hard-caps results at 10,000 rows.

Adopted into this plan: the bounded rolling buffer with an explicit drop counter (Phase 1) gains a
"% displayed"-style honesty label when the tail samples or the cap trims. Collapsed single-line
rows with expand-on-demand — the single biggest reason the AWS viewer stays cheap (fixed heights
need no measurement pass) — **landed 2026-08-18 as the opt-in "Collapse" toggle** (§3c): collapsed
rows are a fixed-height constant the virtualizer never measures, badges/ANSI/summaries stay on the
one line, and clicking a row expands just that row to the full rendering. What remains a product
decision is only the *default*: the console collapses by default, we still expand by default, and
flipping that is a deliberate later call, not a side effect of shipping the lever.

## 3. Plan

Ordered so each phase is independently shippable and measurable. Failing-test-first where a phase
changes observable behavior; paced benchmarks before/after each phase (see Phase 0).

**Progress:** Phases 1 (tail batching/bounding, as `useLogTailBuffer`), 2 (peek virtualization),
3 (row render cost), 4 (nextToken paging, auto-load at the newest edge, "+" count label,
monitor-tab refetch made honest — and the backward half: time-window expansion at the oldest
edge, with "Start of logs"/"Start of range" markers and jump-to-timestamp riding the anchor
machinery) and 5 (shared `LogMessage`, virtualized search results — see its section for the two
consolidations deliberately declined) landed; so have the peek's forward paging (a dead-tail
token walk, `useForwardLogPages`) and §3b's staleness watchdog. Still open: Phase 0's formal
benchmark baseline. Phase 1 shipped without the full
`useLogFeed` extraction — the hook owns the session + buffer + cap and both surfaces consume it;
the fetched-side merge stayed in the viewers (sorted-merge, no per-batch re-sort), and the
remaining consolidation is Phase 5's. Phase 2's scroll anchoring cannot be exercised in jsdom
(the tests stub the virtualizer), so the in-browser pass against a built image is the required
verification for it.

### Phase 0 — Baseline harness (S)

Seed a group with 10k+ events (mixed plain/JSON/ANSI/platform-record lines; a seeding script under
`web/scripts/` or reuse of the existing seed tooling — pin `AWS_REGION`, this machine exports
`ap-southeast-2`). Record, per surface: initial render time, scroll frame times, per-event tail
cost at 1k/5k/10k buffered, and heap growth over a 5-minute tail. Chrome DevTools performance
traces + heap snapshots; repeat the tail measurement in Firefox. Numbers land in this doc.
Firefox profiles are analysed offline with the gzip→Python aggregation script (long-task windows,
`eventDelay` distribution, per-window JS attribution) rather than by eyeballing the viewer.
Measure in a clean browser profile — extensions off, DevTools panels closed — the 2026-08-18
trace shows a password-manager content script and a CC/GC pause contributing 7.7s of stall that
is not app code (§2b).

### Phase 1 — Shared ingestion model, incremental derivation (M) — fixes F2, F5, most of F3 — **landed**

Extract a `useLogFeed` hook (feature-local, `features/cloudwatch/logs/`) that owns:

- **Fetched + tailed merge** with an *incremental* `dropTailedDuplicates`: keep the unclaimed-count
  `Map` in a ref, update it when the fetched list changes identity, and dedup only newly-arrived
  tail events. Preserve the count-based semantics exactly (the "logged twice shows twice" test).
- **Batched appends**: accumulate events from the tail generator and flush once per animation
  frame (or ~50ms), replacing per-event `setState`. The generator API in `tail.ts` is untouched.
- **Order maintenance without full re-sort**: fetched pages are sorted once on arrival; tail
  batches are sorted within the batch and appended — a merge step handles the (rare) out-of-order
  arrival instead of re-sorting the world. Descending view derives from the same ascending array
  by index math in the row renderer, not by re-sorting (`sortEvents` today copies+sorts on every
  toggle *and* every batch).
- **Per-event derived metadata** (`plain`, `level`, `summary`) in a `WeakMap<event, meta>` computed
  on first sight, so history is never re-derived. Format/syntax-dependent derivation stays
  row-local (as `LogMessage` already does).
- **A bounded buffer**: cap the combined in-memory list (default 10k, matching the server page
  cap). When the cap trims oldest events, surface a "N earlier events dropped" affordance
  analogous to the existing "Show N earlier" — never silently.
- Clear/restore (`clearedThrough`) semantics move in unchanged; the existing viewer tests keep
  passing against the refactored component.

`LogEventsViewer` and `LogStreamPeek` both consume this hook.

### Phase 2 — Virtualize the log peek (M) — fixes F1, F1b — **landed**

`LogsPane` adopts the same `useVirtualizer` pattern as `LogEventsViewer` (dynamic `measureElement`,
overscan ~15), keeping: reverse pagination via the top sentinel, prepend scroll anchoring (the
snapshot ref logic translates to `scrollToOffset` with the virtualizer's measured delta, or
`shouldAdjustScrollPositionOnItemSizeChange` where applicable), pin-to-bottom, and the unread pill.
Keys become stable per event (timestamp + ingestionTime + stream + message hash — or the event
object's identity index within the feed), never the array index.

### Phase 3 — Row render costs (S) — fixes F4, F6 — **landed**

- `memo(LogMessage)` and a memoized shared row component; props reduced to primitives + the event.
- Compile the filter-highlight regex once per `activeFilter` (`useMemo` at viewer level, passed
  down); `parseLogFilterTerms` runs once, not per chunk.
- Cache Prism output per (message, pretty) in a small LRU or on the event's WeakMap entry, so a
  scroll frame never re-tokenizes; align `LogViewer` to row-local JSON handling (delete its
  whole-array `normalizedEvents` derivation) — the rest of F3.
- Replace the render-body `clientHeight` read with `virtualizer.scrollRect`.
- `data-1p-ignore` + `data-lpignore="true"` on the log filter inputs (see §2b — keeps password
  managers' field analysis away from the page's only fill-candidate elements).

### Phase 4 — Pagination fidelity (M) — fixes F7 — **landed** (stream/all-streams both
directions; the peek's dead-tail forward paging landed with §3b's watchdog)

- `filterEvents` gains `nextToken`/`limit` passthrough; `LogEventsViewer` moves to
  `useInfiniteQuery` with a bottom sentinel ("load more" as the user nears the end of loaded
  data), so >10k results are reachable instead of silently cut. The result-count label says
  "10,000+ (more available)" when a token remains.
- **Bidirectional expansion, matching the AWS console and each API's directionality.**
  `GetLogEvents` (peek) is natively bidirectional — backward paging exists, forward paging is
  added for the non-tailing case. `FilterLogEvents` (stream/all-streams) pages forward only, so
  "load older" is *time-window expansion*: extend `startTime` backward and fetch the exposed
  sub-range `[T−δ, oldestLoaded)` as its own fully-paged query, prepended (with the virtualizer's
  scroll anchoring). Newer events come from the `nextToken` walk or the live tail.
- **Backward half, as landed.** One bidirectional `useInfiniteQuery`: forward pageParams are
  nextTokens, backward pageParams are time-window chunks, each chunk a fully-paged sub-query of
  its window. Chunks start at the anchor lead-in's 15 minutes and *double* per step (uncapped):
  O(log) requests across sparse history — the store's time-range pushdown makes wide-but-empty
  windows cheap — while dense history self-limits, since the next chunk loads only after the
  user scrolls through the previous one. Boundaries honour FilterLogEvents' inclusive `endTime`:
  adjacent chunks are `[a, b]`, `[b+1, c]`, so a boundary-millisecond event loads exactly once.
  The stop condition is metadata, not probing: min `firstEventTimestamp` over the streams in
  view (one `DescribeLogStreams`) is the floor *hint* — best-effort in real AWS, so the final
  chunk goes out with no `startTime` at all rather than clipping to the hint; with no hint, one
  empty probe is tolerated before the unbounded close-out. Expansion runs only when the window
  start is system-chosen (an anchor's lead-in): a user-set start is a hard floor, never fetched
  past, and the oldest edge says "Start of range" with the time filter as the reason —
  "Start of logs" when history is truly exhausted, "Loading older events…" in flight, mirrored
  top/bottom with the sort direction. Prepends anchor by row key (the peek's pattern), sort each
  chunk once and merge (never a re-sort of loaded pages), keep one backward window in flight,
  and the query skips focus refetches so a refocus never replays every window. Jump-to-timestamp
  (toolbar, next to the time filter) navigates to the same route with `anchorTs` and no
  signature; anchor matching falls back to the first event at-or-after the timestamp, with the
  persistent mark reserved for exact matches.
- Note on mutation cost (see §2b): React's commit already inserts each newly-mounted row's
  subtree as one detached-built insertion, and a whole commit reaches observers as one
  microtask-batched callback — so pagination prepends are cheap *provided keys are stable*;
  no manual DOM batching is needed or wanted.
- Monitor tab requests `limit: 100` (server returns the tail once `startFromHead` semantics are
  respected — verify against the emulator; otherwise page from the end) and either gets a real
  `refetchInterval: 5_000` or loses the "auto-refreshes" claim. Failing test first for the label.

### Phase 5 — DRY consolidation (M) — fixes F8 — **landed** (scoped)

What landed: `LogMessage`/`LevelBadge` extracted to `components/logs/log-message.tsx` (with the
filter-mark helper) and consumed by the stream viewer, the generic `LogViewer`'s rows, and the
log-group search results; the search results left their raw `<Table>` for a virtualized region
(`log-search-results.tsx`) with level tint + badge, ANSI colour and filter-match marks — the one
surface that had none of them — keeping click-through-to-stream. `logEventKey`, the comparator
and the merge already live once in `tail.ts` from earlier phases.

Two consolidations deliberately **not** taken, because the abstraction would have been worse
than the duplication:

- The pin-to-bottom/unread logic stays per-surface. The stream viewer's version is
  sort-direction-aware and drives `scrollToIndex`; the peek's is bottom-only `scrollTop` pinning
  entangled with prepend anchoring and the unread pill. One hook serving both needs more
  configuration than either implementation has code.
- The peek's row stays a bare timestamp + `AnsiText` line. Routing it through `LogMessage` with
  everything switched off would add indirection and no shared behaviour; if the peek ever grows
  badges or summaries, adopt `LogMessage` then.

- One shared `LogRow`/`LogMessage` used by all four surfaces (level badge, ANSI, platform summary,
  optional stream column, optional filter highlight).
- Log-group detail's search results render through the shared virtualized viewer instead of a raw
  `<Table>` (gains level tint, ANSI dedup with the stream page, and stops rendering unbounded DOM).
- Pin-to-bottom/unread logic lives once (hook shared by peek + viewer).
- `LogViewer` either becomes a thin preset of the shared components or is absorbed; its three
  call sites (monitor tab, invocations drawer, ECS task detail) migrate unchanged visually.

### Verification (every phase)

- `pnpm` vitest suites for the logs features stay green; new tests: ring-cap drop affordance,
  incremental-dedup equivalence with the current `dropTailedDuplicates` on randomized sequences,
  batched-append flush, filter pagination, stable peek keys under prepend.
- Re-run the Phase 0 measurements; paste before/after into this doc in the same commit
  (per-commit plan-doc discipline).
- Cross-browser pass: Chrome + Firefox locally (tail 10k events, scroll, toggle Format), Safari
  via BrowserStack or a borrowed machine for the peek's scroll anchoring — the one place engines
  differ meaningfully.
- Web UI visual verification happens against a Docker image built from this worktree, published to
  127.0.0.1 (host `preview_start` resolves the wrong tree).

## 3b. Live-session health (landed with Phase 5; watchdog + dead-tail forward paging after)

Tailing is a push stream (StartLiveTail over AWS event-stream framing), not polling, so the
indicator worth showing is session *health*, not activity: `useLogTailBuffer` exposes
`status: idle | live | error`, and both tailing surfaces render a wordless dot — green pulse
while the session is open, red and still when it died without being asked to (emulator restart,
dropped network), which on a quiet stream is otherwise indistinguishable from healthy. The
emulator writes an empty `sessionUpdate` every second, a de-facto heartbeat, and the client now
consumes it: `tailLogEvents` reports every received frame (`onActivity`, a ref-timestamp write —
never React state, so the per-second heartbeat costs no re-render), and `useLogTailBuffer` runs
one O(1)-tick watchdog interval per session that flips `status` to `error` after ~10 s of
silence, catching the connection that dies *without* an error (no FIN — machine sleep, NAT
timeout) and would otherwise look live forever. The 10 s threshold is emulator-tuned — ten
missed beats of *our* 1 s heartbeat — not an AWS contract: real Live Tail documents no fixed
update cadence.

A dead tail also no longer strands the peek's newest edge (the Phase 4 open item): GetLogEvents
is natively bidirectional, so `useForwardLogPages` walks `nextForwardToken` from the newest
fetched page — only while the tail is dead and the user sits at the bottom, chaining page to
page off state changes (no timer, no polling) until AWS's canonical end signal (the same token
coming back; an empty page is not the stop condition) latches it off. The walk lives outside the
backward infinite query deliberately — React Query's bidirectional cache latches "no previous
page" permanently and deposits an empty probe page per attempt. Forward pages overlap what the
dead session already delivered; they reconcile through the same count-based
`dropTailedDuplicates`, against a stream-stamped view of the stored events (GetLogEvents output
carries no `logStreamName`, tailed events do — without the stamp no pair could ever match).

## 3c. Anchored search navigation (landed after Phase 5)

A search hit now deep-links to its stream anchored on the exact event (`anchorTs` + a djb2
message signature in the URL): the viewer widens its window to 15 minutes before the anchor,
pages until the event is loaded, centres it, and keeps it marked. Context below is the ordinary
newest-edge infinite scroll; context *above* beyond the 15-minute lead-in now loads through
Phase 4's backward time-window expansion — the anchor flow inherited it for free, as predicted
(pinned by a test), since the lead-in is exactly the system-chosen window start expansion widens.
The stream column sits on the right of the results, as the console has it.

## 3d. Storage-side access audit (2026-08-18) — every log-view pattern measured, four fixed

The emulator side of this plan: an audit of `internal/services/cloudwatch/logs/`'s storage layer
against each access pattern the surfaces above generate, benchmark-first
(`access_pattern_bench_test.go`, run against BOTH backends — memory and SQLite). Median of
`-count=5` on the dev box (Ryzen 9 5900X, Windows; SQLite in a temp dir):

| Pattern | Verdict | Before → after (median) |
| --- | --- | --- |
| FilterLogEvents window chunk (backward window-walk, Phase 4) | already O(chunk), both backends | mem ~38µs, SQL ~2.8ms per 1k-event chunk, flat at any depth in a 500k-event group |
| GetLogEvents token page 400k deep (peek walking back) | **fixed** — was O(distance from window edge) | mem 664µs → 2.6µs; SQL 89ms → 0.36ms (flat vs shallow) |
| FilterLogEvents scan-budget batch resume at depth | **fixed** — same O(distance) shape | mem 913µs → 46µs; SQL 92ms → 3.4ms (flat vs shallow) |
| FilterLogEvents `logStreamNames: [one]` (single-stream viewer) | **fixed** — scanned the whole group's window | 3.0ms → 82µs (20-stream group, mem) |
| DescribeLogStreams (firstEventTimestamp as history floor) | already metadata-only, O(1) per put | ~15µs regardless of 1k vs 500k events behind it |
| PutLogEvents flush vs stream history | **fixed** — SQL re-ran `MAX(seq)` (a per-stream index scan) per batch; mem re-sorted the whole stream per flush | SQL 129ms → 0.65ms per batch at 500k events; mem 3.8ms → 0.9µs |
| Retention expiry | already indexed (`region, group_name, ts` range delete via `idx_logs_events_group` — the same index the flat window-chunk numbers above exercise) | — |

The fixes, all in `event_backend.go` (+ one line in `typed_logic.go`): binary-search positioning
for the memory backend's range reads (the matching set is contiguous in the `(ts, seq)` order, so
deep pages are O(log n + page)); a monotonic-append fast path skipping the full re-sort; folding a
cursor's implied ts bound into the SQL window predicate so a resume is an index SEEK (the cursor
expressed only as a row-value/OR residual is treated as a filter by the planner — measured, not
assumed); a per-process seq counter replacing the per-batch `MAX(seq)` (aligning SQL with the
memory backend's monotonic-across-delete semantics, pinned by a new parity test); and pushing a
single explicit `logStreamNames` entry down as its own prefix bound.

Consequence for Phase 4's backward time-window expansion (landed the same day): each
`[T−δ, oldestLoaded)` chunk lands on an O(chunk) storage path in both backends, and repeated
"load older" walks cost the same for the oldest chunk as the newest. No wire-visible semantics
changed (inclusive/exclusive bounds, token shapes, interleaving order all pinned by the existing
integration suite).

### QOL backlog (viewer-side, fidelity-safe)

Ideas gathered 2026-08-18, all display-layer only, no API behaviour changes. **Landed 2026-08-18**
(stream/all-streams viewer):

- **UTC/local timestamp toggle** — `formatLogTime`/`formatLogDate` grew an explicit `utc` flag
  (local by default, so every other call site is untouched); the toolbar pill switches rows, the
  plaintext prefix and the time-range chip's absolute label together. The jump-to-timestamp picker
  stays local: `datetime-local` is inherently zone-local input.
- **Inter-row time deltas** — opt-in "Deltas" toggle, off by default; the gap to the
  chronologically previous event renders beside the Time column's timestamp (Table mode; the
  plaintext prefix stays as it was). O(1) per row from the neighbouring index, no derived array;
  signed, so out-of-order ingestion shows its direction rather than a lie.
- **Persisted view preferences** — Table/Plaintext, Format, Syntax, Wrap, sort direction, UTC,
  Deltas and Collapse survive revisits via one versioned localStorage key
  (`overcast.logs.viewPrefs.v1`, `use-log-view-prefs.ts`): read once at mount, one write per
  change, corrupt/missing state falls back to the defaults silently. Tail, the cleared-through
  cut and per-row expansion are session state and deliberately do not persist.
- **Collapsed single-line rows** — the §2c lever, as an opt-in "Collapse" toggle (persisted,
  default OFF): every row is one fixed-height truncated line the virtualizer never measures
  (constant `estimateSize`, no `measureElement` ref), badges/tints/ANSI/platform-summaries stay,
  click expands just that row to the full current rendering and click again folds it.
  **Collapsed-by-default — the console's behaviour — is left open as a product decision.**
- **Export visible events** — a toolbar Export control downloading exactly what the list holds,
  in the displayed sort order: CSV in the console's search-results shape (ISO 8601 timestamps,
  raw stored message bytes — never the stripped or summarised rendering) and JSON with the four
  raw fields. When more events remain server-side the control says the export excludes them,
  never implying full history.

**Landed 2026-08-18, second batch** (the three interaction items):

- **RequestId click-to-filter** — `describeLogEvent` derives one `requestId` per event with the
  rest of the WeakMap-cached metadata (never per render), from the three places Lambda writes
  one: the `RequestId: <id>` label of Text-format platform lines and rendered summaries, a JSON
  document's `"requestId"` field, and the Node runtime's tab-separated UUID column. Rows carrying
  one get a hover filter affordance (beside the copy button — text spans stay out of the
  memoized ANSI/highlight pipeline) that fills the filter with the QUOTED id: a plain
  FilterLogEvents quoted-term search, exactly what typing it would do, no invented semantics.
- **Copy-deep-link on any row** — a second hover icon (the `CopyButton` grew an optional `icon`
  glyph so the pair stays readable) copying an absolute URL to the stream route with the event's
  `anchorTs` + `anchorSig`; the link inherits the anchor machinery whole. Built off
  `window.location.origin` in the row — cheap, and the row wrapper is per-frame territory anyway.
- **Keyboard row navigation** — the scroll container is focusable (visible inset focus ring);
  ArrowDown/ArrowUp and j/k move a `logEventKey`-keyed cursor (inset-ring marking, distinct from
  the anchor's), Enter toggles the focused row's expansion in Collapse mode, `c` copies the
  focused row's `meta.plain`. Announced with `aria-activedescendant` on the container over stable
  per-event row ids. Keys typed into inputs pass through untouched, Ctrl/Cmd+F keeps its
  document-level shortcut (modified keys are ignored), and cursor moves scroll via
  `scrollToIndex(…, { align: "auto" })` — no scroll-jacking. The cursor lives on the row
  wrappers; `LogMessage` receives no cursor-related props, pinned by a render-count test that a
  cursor move re-renders zero rows' memoised content.

Still open: "filter for selection". Jump-to-timestamp landed with Phase 4's backward half
(it reuses the anchor flow, as planned).

## 3e. Trace 2 (2026-08-19) — scroll-time DOM weight — **landed**

A second Firefox profile (stream view, all phases above shipped) still showed lock-ups and
blank regions while scrolling. The 10.3 s capture, same offline rollup as §2b:

- **37.1% of the tab's main thread inside 1Password's `injected.js`** (MutationObserver
  processRecords/acceptNode; single tasks of 2.0 s and 1.2 s) and **6.3% in Firefox's own
  `FormAutofillHandler.sys.mjs`** — two form-detection walkers, both fed by us.
- **~148k accessibility-tree node removals** (`DocAccessible::ContentRemovedNode` n=148,392;
  `ShutdownChildrenInSubtree` 1.67 s) — the count that names the real defect: each virtualized
  row was a large element subtree, so every scroll frame's mount/unmount churned thousands of
  nodes, and every document observer pays per node whether or not it cares about the content.
- App JS itself: **6.2%** (react-virtual 4.7% + bundle 1.5%), plus 628/431 ms tasks under the
  virtualizer's `flushSync`/`resizeItem` — mostly style/layout of the same heavy subtrees.
- `eventDelay` >100 ms for 48.5% of samples, >1 s for 18.6%, max 2.3 s.

Two per-row weights, both deferred rather than removed (`useScrollSettled`, a render-phase
latch: seeded false when the row mounts mid-`virtualizer.isScrolling`, flipped on the first
idle render, never flipped back):

- **Syntax-highlight spans** (`LogMessage`): a row mounted mid-scroll renders its JSON as one
  text node in the same `<pre>`, and hydrates the Prism markup when the scroll settles. The
  §4 worker analysis already established this swap is layout-stable — identical text, identical
  classes — so `measureElement` never re-fires and the prepend anchor never moves.
- **The hover-action buttons** (`RowActions`, extracted from inline JSX): buttons are exactly
  what form-detection walkers inspect, and the cluster is `opacity-0` until hover anyway. The
  `w-16` wrapper always renders (layout never depends on settling); the buttons mount on the
  first idle frame and stay.

The peek's `LogViewerRow` threads the same flag through to `LogMessage` for its Format mode.
Steady-state rendering is byte-identical to before — the deferral only exists in the frames
where rows are being churned. Pinned by `log-message.test.tsx`: no token spans mid-scroll,
same text/classes, hydrate on settle, markup survives the next scroll.

Verified against the reporting user's real database (207 events, ~460 highlight spans per
row at rest): mid-scroll a fling mounted 125 fresh rows with **zero spans, zero buttons, max
7 elements each**; 400 ms after settling, every mounted row had its buttons and every JSON
row its highlight. Two follow-ups came out of that session:

- **Hydration is gated by viewport proximity, not just settling** (`nearViewport`, same
  module). The settle latch alone hydrates every *mounted* row in one commit — and with
  Format on, this stream's rows pretty-print to thousands of spans each, so a 30-row
  overscan window hydrating at once was a six-figure node insertion that froze the tab.
  Rows hydrate when the scroll is idle AND they sit within about two viewports; the latch
  still keeps them hydrated afterwards. (`LogMessage`'s prop is `defer` — the caller passes
  `isScrolling || !nearViewport(...)`.) Overscan rose 15 → 30 in the same change: mounts
  are ~7 nodes now, so the headroom that kills blank track during flings is nearly free.
- **Wrap mode gained `min-w-0` on the message `<pre>`** (a flex item): min-width:auto pins
  a flex item to min-content width and break-word does not lower min-content, so one
  whitespace-free Powertools error dump forced a ~42,000px scroller under a 2,700px
  viewport. Live-patching confirmed exactly-viewport width after; no-wrap mode keeps the
  wide scroller deliberately.

Answer to "is React the hindrance?": no — measured. App JS was ~6% of the main thread; the
cost was DOM node volume, which a vanilla or canvas rewrite would pay differently but a
framework swap would not remove (a hand-rolled list inserting the same 300-span rows feeds
the same walkers). The escalation ladder if churn ever dominates again: default to collapse
mode (fixed 30 px rows, no measurement — AWS's own default), then DOM row recycling for the
fixed-height mode, then canvas — each step buys throughput by giving up DOM affordances
(find-in-page, selection, a11y for free), which is why none is taken pre-emptively.

## 3f. Zero-DOM syntax highlighting (2026-08-19) — CSS Custom Highlight API — **landed**

§3e deferred the highlight spans; this removes them. Where the browser has the
CSS Custom Highlight API (Chrome 105+, Safari 17.2+, Firefox 140+), a
syntax-highlighted log row renders as ONE text node and token colours paint as
registered `Highlight` ranges — ~460 span elements per settled row (that
session's real stream) become zero, for every document observer that §2b/§3e
showed charging per node. Where the API is missing, the cached Prism markup
path renders byte-identically to before.

Shape (a kernel, not a logs feature — the S3 preview adopted it 2026-08-20
through the shared `HighlightedCode` component; see the known-and-accepted
list below):

- **Facade** `web/src/lib/highlight-code.ts`: the whole detection matrix, the
  markup backend (unchanged LRU: 400 entries / 100 KB cap), and the ranges
  backend — `requestTokenRanges` (own LRU, same policy) returning an array
  synchronously on cache hit / small doc / no worker, a promise only when the
  tokenize is genuinely off-thread. Components ask `highlightPresentation()`
  and never feature-detect.
- **Offset tokenizer** `prism-ranges.ts`: `Prism.tokenize` walked into
  `{start, end, type}` leaf spans that tile the text (pinned against the
  markup backend token-for-token), plus the packed wire form.
- **One persistent worker** `highlight-worker.ts` (Prism's own async mode
  stays off — its FAQ's worker-per-call overhead is exactly what this
  design amortizes away): in-flight coalescing means fifty rows of one
  document tokenize once; cache hits never message at all.
- **Registry + hook** `highlight-registry.ts` / `use-highlight-ranges.ts`:
  one global `Highlight` per themed token class (`overcast-token-string`, …)
  registered at module init; rows apply ranges in a layout effect keyed on
  (text, node) whose cleanup removes exactly that row's ranges — Format
  toggles, collapse/expand and unmount round-trip leaving only live rows'
  ranges (pinned by `log-message.ranges.test.tsx`, including a
  MutationObserver probe asserting range application produces zero records).
- **Theme** `web/src/styles/syntax-tokens.css` (extracted whole from
  global.css): `--token-*` variables per class in both palettes, consumed by
  BOTH selector families — `.token.<class>` for markup, and
  `::highlight(overcast-token-<class>)` as deliberately *separate* rules,
  because a browser without the API drops any selector list containing an
  unknown pseudo-element, which would have uncoloured the markup fallback in
  exactly the browsers that need it. Theme switches recolour ranges via CSS
  alone. The grammar-coverage test enumerates every class the registered
  grammars can emit and fails on any class missing a variable, a `.token`
  rule or a `::highlight` rule. (Incidental fix: the old literal dark token
  rules only matched explicit `[data-theme="dark"]`, so system-dark sessions
  read light token colours; the variables wire both dark scopes.)
  `::highlight()` can only express color / background-color / text-decoration
  / text-shadow — this theme is colour-only, so nothing is lost.

**Sync-vs-worker threshold, measured** (Node 24 worker_threads as the
messaging stand-in, this dev box, realistic nested JSON, median of 30):

| doc size | tokens | sync tokenize | worker round-trip (object clone) | echo (clone only) | packed round-trip |
| --- | --- | --- | --- | --- | --- |
| 2.1 KB | 275 | 0.05 ms | 0.27 ms | 0.36 ms | 0.12 ms |
| 8.5 KB | 1,085 | 0.21 ms | 0.84 ms | 1.31 ms | 0.28 ms |
| 33 KB | 4,217 | 0.95 ms | 3.46 ms | 5.16 ms | 1.01 ms |
| 131 KB | 16,745 | 4.10 ms | 12.9 ms | 20.1 ms | 4.79 ms |

The object-form numbers vindicate Prism's FAQ from an unexpected angle:
structured-cloning one `{start,end,type}` object per token costs more than
the tokenize at *every* size — a worker replying in object form is a strict
loss. The reply therefore travels as a transferable `Uint32Array` of
(start, end, type-index) triples over a small cloned type table: transfer is
a pointer handoff, so the main thread pays only the request clone
(0.001–0.03 ms) plus the unpack, and the packed round-trip tracks the
worker-side tokenize almost exactly. Requests stay plain clones (strings
cannot transfer; pre-encoding would cost more than the clone), and
`SharedArrayBuffer` is declined outright — it needs cross-origin isolation
headers the console does not ship and cannot beat a transfer for one-shot
request/reply. **Threshold: `SYNC_TOKENIZE_MAX_CHARS = 8192`** — below it
sync tokenize is ≤0.2 ms (comparable to the messaging's own main-thread
share, without the async second pass), and typical log lines sit far below
that, so most rows never message at all; above it the worker keeps the main
thread's cost at send + unpack instead of the full tokenize (4 ms saved at
131 KB; CloudWatch's 256 KB event cap bounds the worst legal case).
`requestIdleCallback` is deliberately not used anywhere in the path: range
application mutates nothing and is already idle-gated by `useScrollSettled`,
so idle-scheduling it would only delay colour.

Degradation is per-feature and tested: no Highlight API → markup backend
byte-identical to today; no Worker / constructor throws / later error → the
same facade call tokenizes synchronously (a dying worker resolves its
stranded requests synchronously before being dropped for the session). The
`defer` gating from §3e still applies — deferred rows get no ranges until
they settle near the viewport, and under the ranges backend the deferred and
settled DOM are *the same DOM*, so hydration stops being a DOM event
entirely.

### Review findings folded in (2026-08-19, PR #1070 review)

An eight-angle review of the first cut surfaced and fixed, in order of
severity:

- **Prism's own worker message handler was killing the worker on its first
  request.** prismjs registers a `JSON.parse`-ing `message` listener at its
  module init inside any scope with no `document`, unless
  `disableWorkerMessageHandler` was set *before* it loaded — and our protocol
  is a structured-cloned object. The first >8 KiB document threw in the
  worker, fired `onerror`, and silently retired the worker for the session:
  every visual check passed (the sync fallback paints identical colours),
  which is precisely how it slipped through. `prism-global-config.ts` now
  sets the flag in an import that executes first, pinned by a test on the
  flag Prism copies in.
- **A rejected or undeliverable request could poison its cache key forever.**
  `requestTokenRanges` now guarantees a never-rejecting, always-settling
  promise: one bookkeeping closure (cache put + in-flight delete) sits behind
  worker replies, stranded-work recovery, and a guarded `postMessage` whose
  synchronous failure answers on the main thread.
- **The worker gets one respawn** (`WORKER_FAILURE_LIMIT = 2`) instead of
  dying to the session on a single error, and `onmessageerror` recovers the
  same way `onerror` does.
- **The caches are bounded by cost, not entry count**: the markup cache by
  characters of HTML, the ranges cache by token count — which also removes
  the old 100 KB text refusal that denied caching to exactly the documents
  the worker serves. One entry may take at most a quarter of its budget.
- **Alias resolution is now cascade-faithful and contractual**:
  `TOKEN_COLOR_CLASSES` order == stylesheet rule order (a coverage test pins
  it), and `resolveTokenColorClass` (memoized) picks the highest-indexed
  class — the same winner the CSS cascade gives the markup backend, so the
  backends cannot colour an aliased token differently.
- **`applyTokenRanges` is all-or-nothing on its source text** — the staleness
  guard moved into the kernel (`applyTokenRanges(node, text, ranges)`), so a
  consumer other than the hook cannot paint misaligned colour partially.
- The detection matrix collapsed to one module-load constant
  (`HIGHLIGHT_PRESENTATION`) read everywhere; the hook's only gate is its
  null-text contract. The worker chunk now builds as `format: "es"` to match
  its `type: "module"` construction. The markup backend is *not* quite
  byte-identical after all: the theme gained a `.token.comment` colour that
  never existed before (comments were full-strength foreground on every
  markup surface) — kept as an improvement and disclosed in the changelog
  rather than shipped silently.

Known-and-accepted from the same review — all three since landed:

- ~~Live `Range` objects carry per-DOM-mutation fix-up cost while mounted~~ —
  **landed 2026-08-20**: `applyTokenRanges` now mints `StaticRange`s (with a
  live-`Range` fallback for a hypothetical engine shipping highlights without
  them). Staleness was already a non-issue by construction — ranges are
  disposed and re-applied on any text change and refused on mismatched text.
  Chrome paint verified against a built image (full token colour, zero spans,
  every registered range a `StaticRange`); the Firefox paint check was
  deliberately left to a human eye before merge — the one step this work
  could not automate.

  **And a third Firefox trace (2026-08-20, 31 s) forced a registry redesign
  in the same branch.** "Page locks up after scrolling down pretty far":
  48.4% of the whole trace — 15,109 self-samples, eventDelay max 14.9 s —
  sat inside ONE closure, which the built bundle de-minifies to the
  *dispose loop*: per-range `Highlight.delete` against the page-global
  per-class sets. Firefox charges each mutation of a registered highlight
  with work scaling on the set's size (every mutation also invalidates its
  paint), so a continuously scrolled stream — hydrated rows unmounting at
  hundreds of ranges each, sets tens of thousands strong — degraded
  quadratically with depth. Chrome does not exhibit it: a 36-cycle
  deep-scroll protocol (2,800 px + settle per cycle, ~100k px deep) held
  flat at 16–19 ms per scroll frame on the *buggy* build. The fix removes
  the delete path entirely: registration/disposal are O(1) set ops on a
  `liveApplications` registry, and one coalesced microtask rebuild
  constructs fresh, unregistered `Highlight`s from the live rows and swaps
  them into `CSS.highlights` wholesale — zero `Highlight.delete` calls
  ever, one invalidation per class per flush, cost bounded by the hydrated
  viewport instead of scroll history. Same protocol on the fixed build:
  17–18 ms flat, settle ≤ 1 ms, and exactly the hydrated viewport's ranges
  (915) live after 36 churn cycles — the swap bookkeeping is exact.
  Microtasks run before paint, so settle-hydration still colours in the
  same frame. Firefox confirmation comes from the reporter's re-trace.
- ~~The S3 preview's adoption needs a `FormattedPreview` shape change plus a
  shared rendering component before the "kernel adopts unchanged" promise is
  real~~ — **landed 2026-08-20** (#1074): `HighlightedCode`
  (web/src/components/ui/highlighted-code.tsx) is the shared renderable unit
  owning the whole presentation fork (ranges pre + hook | settled markup
  innerHTML | deferred plain) plus the settle latch; `LogMessage` and the S3
  preview both consume it, `FormattedPreview` returns `{text, language,
  skipped}` policy (the >256 KiB plain-text cap and its notice kept), and the
  preview's wrap-class split stays deliberate — a language-chosen document
  scrolls as code (`whitespace-pre`), plain text wraps. Coverage for the
  preview's grammars (markup/css/javascript) takes the *markup-parity* form
  this review recommended — every token class themed in both backends or in
  neither, pinned per grammar and globally in token-theme-coverage.test.ts —
  while json keeps its stricter total-coverage test.
- ~~Three modules hand-roll the persistent-worker-client shape (docs search,
  map layout, highlighting) that deserves one shared helper~~ — **landed
  2026-08-20** (#1073): `web/src/lib/worker-client.ts` owns the
  correlation/lifecycle machinery (lazy factory construction, request-id
  correlation, pending-map hygiene, onerror/onmessageerror recovery via
  caller-supplied synchronous fallbacks, guarded postMessage,
  respawn-limited retry), and the three call sites keep their distinct
  semantics as parameters: highlight keeps its never-rejecting promises and
  retry-once ladder, docs search keeps its abort/cancel path, map layout
  keeps its stale-reply guard.

## 3g. The formatting pass (2026-08-20) — memoised, deliberately synchronous — **landed**

`LogMessage`'s parse + re-serialise (`tryParseJSON` + `stringifyJSON`) ran per row *mount* —
so virtualizer churn re-paid it continuously for the same messages, and a Format toggle
re-paid it for every mounted row at once (~1–3 ms per 100 KB-class document). It is now
`jsonDocumentText(message, pretty)` in log-format: an LRU keyed by (form, message), computed
once per distinct message and form, identity-stable on hits. The `LruCache` behind it moved
to `lib/lru-cache.ts` — the highlight kernel's markup and ranges caches are the other two
consumers.

Considered and declined, same reasoning as §4's worker analysis but stronger: a worker or
rAF/idle deferral cannot carry the formatting pass at all, because the formatted text is the
row's *content* — it must exist at render time, and text that arrives late changes the row's
height after measurement (compact→pretty pop-in plus measure churn plus anchor drift). What
remains after the memo is layout of the pretty-printed blocks themselves, which is the
irreducible cost of showing them; the ranges backend already keeps colour out of that path.

## 4. Explicit non-goals

- ~~No web worker for the standard row path~~ — superseded by §3f, which landed the size
  threshold this bullet itself predicted ("if real streams hit that case"), measured at 8 KiB
  rather than the ~50 KiB estimated here, and only after the packed-transfer protocol removed
  the round-trip's dominant cost (the reply clone; see §3f's table — in object form the worker
  was indeed the loss this bullet called it). The reasoning below stands for the *markup*
  backend, where the worker's reply would be an HTML string and the upgrade a DOM swap; under
  the ranges backend the upgrade is imperative range application — no second commit, no
  mutation batch. A worker's price is the string round-trip plus a
  second render when the result lands — an extra commit and mutation batch per row, a step
  backwards for rows whose synchronous highlight is sub-millisecond (nearly all of them, and a
  repeat highlight is a cache hit since Phase 3). Measured (Node 24, this dev machine,
  `Prism.highlight` on realistic nested JSON): 2 KiB → 0.33 ms, 17 KiB → 2 ms, 83 KiB → 8 ms,
  166 KiB → 19 ms, 414 KiB → 59 ms, 827 KiB → 123 ms. A typical log line is under 2 KiB; the
  16 ms frame budget falls around **~70 KiB**, and CloudWatch caps a single event at 256 KB, so
  the worst *legal* first-highlight is a ~35–60 ms hitch (row memoisation makes it per-mount,
  not per-frame; the LRU declines >100 KB documents by design). If real streams hit that case,
  the fix is a size threshold: below ~50 KiB highlight synchronously, above it render plain text
  and upgrade when the worker responds. The upgrade is layout-stable by construction — Prism
  only wraps text in spans, the text content is identical, so row height and wrapping cannot
  change and `measureElement` never re-fires. Note the same is NOT true of Format-mode
  pretty-printing: that changes the text itself and therefore the row height, so it can never be
  applied as an async upgrade and stays synchronous. No OffscreenCanvas / canvas log rendering
  under any trigger.
- No change to `tail.ts`'s generator contract or the emulator's live-tail wire behavior.
- No redesign of the viewers' look or controls; toggles, badges, and empty states stay as-is.
- `use-event-stream.ts` / map animation performance is out of scope (separate surface).
