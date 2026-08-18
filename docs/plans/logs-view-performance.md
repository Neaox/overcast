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
"% displayed"-style honesty label when the tail samples or the cap trims. Deliberately **not**
adopted without a product decision: collapsed-by-default single-line rows with expand-on-demand —
it is the single biggest reason the AWS viewer stays cheap (fixed heights need no measurement
pass), but our rows wrap and show badges/ANSI inline by design; if Phases 1–3 don't reach budget
on very wide events, this is the next lever, as an explicit UX change.

## 3. Plan

Ordered so each phase is independently shippable and measurable. Failing-test-first where a phase
changes observable behavior; paced benchmarks before/after each phase (see Phase 0).

**Progress:** Phases 1 (tail batching/bounding, as `useLogTailBuffer`), 2 (peek virtualization),
3 (row render cost), 4's forward direction (nextToken paging, auto-load at the newest edge,
"+" count label, monitor-tab refetch made honest) and 5 (shared `LogMessage`, virtualized search
results — see its section for the two consolidations deliberately declined) landed. Still open:
Phase 0's formal benchmark baseline, Phase 4's *backward* time-window expansion, and the peek's
forward paging. Phase 1 shipped without the full
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

### Phase 4 — Pagination fidelity (M) — fixes F7 — **landed** (forward direction)

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

## 4. Explicit non-goals

- No web worker for the standard row path. A worker's price is the string round-trip plus a
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
