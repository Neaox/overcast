/**
 * The DOM side of the highlight kernel's "ranges" backend: one global
 * `Highlight` object per themed token class, and the bookkeeping that ties a
 * row's text node to the ranges it contributed.
 *
 * Registration model: `CSS.highlights` is a page-global registry, so the
 * `Highlight` objects are page-global too — every row's `string` ranges live
 * in the one `overcast-token-string` highlight, coloured by one
 * `::highlight(overcast-token-string)` rule in syntax-tokens.css. Rows own only
 * their ranges: `applyTokenRanges` returns a disposer that removes exactly
 * what it added, so unmounting a row (or swapping its text) leaves the
 * registry holding precisely the live rows' ranges.
 *
 * Nothing here mutates the DOM: a `Range` is a pointer into it and a
 * `Highlight` is a set of pointers. That is the entire point — painting token
 * colours this way produces zero mutation records for the document observers
 * (extension form-walkers, the accessibility tree) that made span-per-token
 * markup expensive at scale.
 *
 * This module makes no feature-detection decisions. The facade
 * ([highlight-code.ts](./highlight-code.ts)) owns the detection matrix and
 * only routes here when the API exists.
 */
import type { TokenRange } from "./prism-ranges"

/**
 * `CSS.highlights` key prefix; `string` registers as `overcast-token-string`,
 * styled by `::highlight(overcast-token-string)` in syntax-tokens.css.
 */
export const TOKEN_HIGHLIGHT_PREFIX = "overcast-token-"

/**
 * Every token class the theme colours — the single source of truth that the
 * `--token-*` variables, the `.token.*` rules, and the `::highlight()` rules
 * in syntax-tokens.css all mirror, pinned by the grammar-coverage test. A
 * class absent here renders uncoloured in the ranges backend exactly as a
 * class with no `.token.*` rule does in the markup backend.
 *
 * ORDER IS CONTRACTUAL: syntax-tokens.css declares its `.token.*` rules in
 * this order (the coverage test pins that), and `resolveTokenColorClass`
 * resolves a multi-class token to the *highest-indexed* class here — which
 * is exactly the rule the CSS cascade picks for the markup backend, where
 * equal-specificity rules resolve by stylesheet order. Keeping the list, the
 * stylesheet, and the resolver aligned is what makes the two backends colour
 * every token identically.
 */
export const TOKEN_COLOR_CLASSES = [
  "punctuation",
  "property",
  "comment",
  "tag",
  "doctype",
  "doctype-tag",
  "selector",
  "attr-name",
  "name",
  "parameter",
  "string",
  "attr-value",
  "template-string",
  "interpolation",
  "number",
  "keyword",
  "operator",
  "boolean",
  "null",
  "function",
] as const

/**
 * Languages whose full grammar the theme is tested to cover (see
 * `grammarTokenClasses`). A surface adopting the ranges backend for a new
 * language adds it here; the coverage test then forces a `--token-*` variable
 * and `::highlight()` rule for every class that grammar can emit.
 */
export const COVERED_TOKEN_LANGUAGES = ["json"] as const

/**
 * Languages the ranges backend serves under the weaker *markup-parity*
 * contract instead: every token class the grammar can emit must colour
 * identically in both backends — themed in both or themed in neither — but
 * need not be themed at all. These grammars (the S3 object preview's) emit
 * dozens of classes the theme never coloured under the markup backend
 * either; demanding total coverage would force inventing a palette for
 * classes no surface has ever painted, while parity keeps the true promise:
 * switching backends changes zero colours. Promote a language to
 * `COVERED_TOKEN_LANGUAGES` when its theming is meant to be exhaustive.
 */
export const PARITY_TOKEN_LANGUAGES = ["markup", "css", "javascript"] as const

const colorClassIndex: ReadonlyMap<string, number> = new Map(
  TOKEN_COLOR_CLASSES.map((cls, index) => [cls, index]),
)

// Distinct type strings are bounded by the grammars (a handful per language),
// so the memo stays tiny while removing a split + scan per token per row.
const resolvedColorClass = new Map<string, string | null>()

/**
 * The one themed class a token's markup classes resolve to, or null for a
 * token the theme does not colour.
 *
 * Cascade-faithful on purpose: for `class="token null keyword"` the markup
 * backend paints whichever of `.token.null` / `.token.keyword` appears LAST
 * in syntax-tokens.css (equal specificity resolves by stylesheet order), and
 * the stylesheet declares rules in `TOKEN_COLOR_CLASSES` order — so the
 * highest-indexed themed class is the markup backend's winner, and choosing
 * it here is what keeps the two backends from ever colouring the same token
 * differently.
 */
export function resolveTokenColorClass(type: string): string | null {
  const memo = resolvedColorClass.get(type)
  if (memo !== undefined) return memo
  let winner: string | null = null
  let winnerIndex = -1
  for (const cls of type.split(" ")) {
    const index = colorClassIndex.get(cls)
    if (index !== undefined && index > winnerIndex) {
      winnerIndex = index
      winner = cls
    }
  }
  resolvedColorClass.set(type, winner)
  return winner
}

let registered = false

/**
 * Puts one named `Highlight` per themed token class into `CSS.highlights`,
 * so every `::highlight()` name exists from the start. Idempotent; the
 * facade calls it once at module init when the API exists. The named sets
 * are only ever REPLACED wholesale by `swapHighlights` below — nothing
 * mutates a registered set range-by-range.
 */
export function registerTokenHighlights(): void {
  if (registered) return
  registered = true
  for (const cls of TOKEN_COLOR_CLASSES) {
    const name = TOKEN_HIGHLIGHT_PREFIX + cls
    if (!CSS.highlights.has(name)) CSS.highlights.set(name, new Highlight())
  }
}

const noopDispose = () => {}

/**
 * One token's range, static where the platform allows.
 *
 * A live `Range` obliges the engine to fix up its boundary points on every
 * DOM mutation in the document — and a virtualized log view mutates
 * continuously, with tens of thousands of token ranges mounted, so that
 * bookkeeping scales with exactly the product this backend exists to shrink.
 * The CSS Highlight API spec recommends `StaticRange` for highlights for
 * this reason — and an invalid static range (one whose node has left the
 * tree) simply does not paint, which the garbage model below leans on
 * directly.
 *
 * `Highlight` is setlike over `AbstractRange`, so browsers that paint the
 * API accept both forms; the constructor check only covers a hypothetical
 * engine shipping highlights without `StaticRange`.
 */
function createTokenRange(node: Text, start: number, end: number): AbstractRange {
  if (typeof StaticRange !== "undefined") {
    return new StaticRange({
      startContainer: node,
      startOffset: start,
      endContainer: node,
      endOffset: end,
    })
  }
  const range = document.createRange()
  range.setStart(node, start)
  range.setEnd(node, end)
  return range
}

/** One row's registered contribution, with the range objects it painted. */
interface RangeApplication {
  node: Text
  /** Parallel arrays: the painted range and the class it resolved to. */
  appliedRanges: AbstractRange[]
  appliedClasses: string[]
}

const liveApplications = new Set<RangeApplication>()
/** Ranges stranded in the registered sets by disposed rows. */
let garbageRanges = 0
let swapQueued = false
let sweepTimer: ReturnType<typeof setTimeout> | null = null
/**
 * The debounce timer `queueSwap` arms for a rapid successor swap. Tracked
 * (unlike an ordinary fire-and-forget `setTimeout`) so the test seam below
 * can cancel it: left to fire on its own, it reads the page-global `CSS`
 * whenever the real clock gets there, which in a test is whatever the
 * *current* test stubbed `CSS` to — or, once `vi.unstubAllGlobals()` has run,
 * the unstubbed jsdom global that has no `.highlights` at all.
 */
let swapTimer: ReturnType<typeof setTimeout> | null = null
const pendingDisposals: RangeApplication[] = []
let triageQueued = false

/**
 * Mutation policy — the fourth iteration, and the one every Firefox trace
 * agrees on (plan doc §3f has the full history):
 *
 * 1. Per-range `Highlight.delete` against registered sets: 15 s of a 31 s
 *    trace, quadratic with scroll depth.
 * 2. Swap-rebuild on every change: disposals scheduled it, so continuous
 *    scroll re-added the hydrated viewport nearly every frame — 12.5 s of a
 *    17 s trace.
 * 3. In-place `Highlight.add` at settle: a bottom→top jump hydrated one
 *    large window and paid ~1 ms per add into the registered sets — one
 *    21-second task. Registered-set mutation is unaffordable in Firefox in
 *    BOTH directions; adds into fresh, unregistered sets measure ~11 µs.
 *
 * So there is exactly ONE way ranges reach `CSS.highlights`: `swapHighlights`
 * builds fresh, unregistered `Highlight`s from the live applications' range
 * objects and swaps them in wholesale — linear, one paint invalidation per
 * class. Everything else is bookkeeping that chooses the swap's FREQUENCY:
 *
 * - A settle commit's hydrations coalesce into one microtask swap (before
 *   paint — colour lands in the same frame, no flash).
 * - A disposal whose node stayed connected (Syntax toggled off in place; a
 *   text swap whose re-apply already registered the new ranges) also queues
 *   the microtask swap: stale ranges over living text would paint wrong.
 * - A disposal whose node left the tree — every scrolled-away row; the
 *   question is asked one microtask post-commit, because React runs effect
 *   cleanups BEFORE detaching host nodes — just counts garbage: invalid
 *   static ranges paint nothing, so cleanup can wait for a quiet second.
 *
 * Scrolling therefore performs zero mutations of registered sets, in either
 * direction, at any depth.
 */
const SWEEP_QUIET_MS = 1_000

function swapHighlights(): void {
  if (sweepTimer !== null) {
    clearTimeout(sweepTimer)
    sweepTimer = null
  }
  const fresh = new Map<string, Highlight>()
  for (const application of liveApplications) {
    const { appliedRanges, appliedClasses } = application
    for (let i = 0; i < appliedRanges.length; i++) {
      let highlight = fresh.get(appliedClasses[i])
      if (!highlight) {
        highlight = new Highlight()
        fresh.set(appliedClasses[i], highlight)
      }
      highlight.add(appliedRanges[i])
    }
  }
  for (const cls of TOKEN_COLOR_CLASSES) {
    CSS.highlights.set(TOKEN_HIGHLIGHT_PREFIX + cls, fresh.get(cls) ?? new Highlight())
  }
  garbageRanges = 0
  lastSwapAt = Date.now()
}

/**
 * Floor between consecutive swaps. One swap is linear in the hydrated
 * viewport (~11 µs/range in Firefox — plan doc §3f), which is fine per
 * settle but not per frame: a pinned live tail hydrates every append batch,
 * and without a floor that is a swap per commit. The first swap after quiet
 * runs in the same commit's microtask (colour lands pre-paint, no flash);
 * rapid successors trail on a timer, so a busy tail's newest rows wear
 * colour at most this much later — imperceptible against the stream's own
 * motion.
 */
const SWAP_MIN_INTERVAL_MS = 150
let lastSwapAt = 0

/** One swap per microtask flush, however many rows applied or withdrew. */
function queueSwap(): void {
  if (swapQueued) return
  swapQueued = true
  const elapsed = Date.now() - lastSwapAt
  if (elapsed < SWAP_MIN_INTERVAL_MS) {
    swapTimer = setTimeout(() => {
      swapTimer = null
      swapQueued = false
      swapHighlights()
    }, SWAP_MIN_INTERVAL_MS - elapsed)
    return
  }
  queueMicrotask(() => {
    swapQueued = false
    swapHighlights()
  })
}

function scheduleQuietSweep(): void {
  if (sweepTimer !== null || swapQueued) return
  sweepTimer = setTimeout(() => {
    sweepTimer = null
    if (garbageRanges > 0) swapHighlights()
  }, SWEEP_QUIET_MS)
}

/**
 * Post-commit triage of disposals: by now the commit has finished detaching,
 * so connectivity means what it says. Disconnected rows are garbage (the
 * scroll path — no swap, no urgency); a still-connected withdrawal must
 * un-paint promptly, so it queues the swap.
 */
function triageDisposals(): void {
  let needSwap = false
  for (const application of pendingDisposals) {
    garbageRanges += application.appliedRanges.length
    if (application.node.isConnected) needSwap = true
  }
  pendingDisposals.length = 0
  if (needSwap) queueSwap()
  else if (garbageRanges > 0) scheduleQuietSweep()
}

function scheduleDisposalTriage(): void {
  if (triageQueued) return
  triageQueued = true
  queueMicrotask(() => {
    triageQueued = false
    triageDisposals()
  })
}

/**
 * Test seam: run any pending triage and swap now, instead of waiting.
 *
 * Also cancels a pending debounced swap (see `swapTimer`): otherwise it
 * survives this synchronous flush and fires on its own clock, which in a
 * test can land after `vi.unstubAllGlobals()` has restored jsdom's real
 * (highlight-less) `CSS` — an unhandled exception unrelated to whatever
 * test happens to be running when the timer's turn comes up.
 */
export function sweepHighlightGarbageForTests(): void {
  triageDisposals()
  if (swapTimer !== null) {
    clearTimeout(swapTimer)
    swapTimer = null
  }
  swapQueued = false
  swapHighlights()
}

/**
 * Registers `tokenRanges` over `textNode` for painting and returns the
 * disposer that withdraws exactly this registration.
 *
 * `text` is the source the ranges were tokenized from, and the guard is
 * all-or-nothing: a node whose data is not exactly that text gets NO colour,
 * because partially- or mis-aligned colour is worse than none. The guard
 * lives here — not in the calling hook — so every consumer of the kernel
 * inherits it.
 *
 * Both registration and disposal are pure bookkeeping; painting happens in
 * the coalesced swap (same commit's microtask — before paint, no flash).
 */
export function applyTokenRanges(
  textNode: Text,
  text: string,
  tokenRanges: TokenRange[],
): () => void {
  if (textNode.data !== text) return noopDispose
  const appliedRanges: AbstractRange[] = []
  const appliedClasses: string[] = []
  for (const token of tokenRanges) {
    const cls = resolveTokenColorClass(token.type)
    if (cls === null) continue
    appliedRanges.push(createTokenRange(textNode, token.start, token.end))
    appliedClasses.push(cls)
  }
  const application: RangeApplication = { node: textNode, appliedRanges, appliedClasses }
  liveApplications.add(application)
  queueSwap()
  return () => {
    if (!liveApplications.delete(application)) return
    pendingDisposals.push(application)
    scheduleDisposalTriage()
  }
}
