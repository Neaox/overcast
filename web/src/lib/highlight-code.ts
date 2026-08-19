/**
 * The syntax-highlight kernel's facade: one place callers ask for
 * highlighting, two backends behind it.
 *
 * - **markup** — `highlightCode`: cached `Prism.highlight` HTML for
 *   `dangerouslySetInnerHTML`. The original backend, unchanged: where the
 *   Custom Highlight API is missing this is what renders, byte-identical to
 *   what it always produced.
 * - **ranges** — `requestTokenRanges` + `applyTokenRanges`: token offsets
 *   painted through the CSS Custom Highlight API, so a highlighted block
 *   stays ONE text node — no spans, no DOM mutation when colour arrives,
 *   nothing for document observers (extension form-walkers, the
 *   accessibility tree) to churn on. Rows using it mount as cheaply settled
 *   as they do deferred.
 *
 * Every capability degrades independently, and the whole detection matrix
 * lives in this module — components ask `highlightPresentation()` and choose
 * *presentation*, never implementation:
 *
 * - No `CSS.highlights` (or no `Highlight`) → markup backend.
 * - No `Worker`, worker construction throws, or the worker errors later →
 *   the ranges backend tokenizes synchronously on the main thread; same API,
 *   callers cannot tell.
 * - `requestIdleCallback` is deliberately not used: range application
 *   mutates nothing and is already gated behind scroll-idle by the callers
 *   (`useScrollSettled`), so idle-scheduling it would only delay colour.
 *
 * Why the markup cache: the hot callers are virtualized rows —
 * `@tanstack/react-virtual` flush-syncs a render on every scroll event, so an
 * uncached tokenise ran once per visible highlighted row per scroll frame,
 * which a Firefox profile of the log stream viewer showed as 80–247 ms tasks
 * inside the scroll handler. Highlighting is a pure function of
 * (text, language), so a repeat render is a map lookup.
 *
 * Returning the *identical* value on a hit matters as much as the saved
 * work: React skips the DOM write when the string handed to
 * `dangerouslySetInnerHTML` is unchanged, and an effect keyed on a stable
 * ranges array never re-applies. An equal-but-new string would replace the
 * nodes for identical content.
 *
 * The ranges backend's tokenization runs in ONE persistent worker
 * ([highlight-worker.ts](./highlight-worker.ts)) — never a worker per call,
 * which is the overhead shape Prism's FAQ disables its own async mode over.
 * Three measures keep the messaging from costing more than it saves
 * (numbers: docs/plans/logs-view-performance.md §3f):
 *
 * - cache hits and small documents (`SYNC_TOKENIZE_MAX_CHARS`) never reach
 *   the worker;
 * - duplicate in-flight texts coalesce onto one round-trip (fifty visible
 *   rows of one cached document tokenize once);
 * - the reply travels as a transferable packed buffer — structured-cloning
 *   one object per token measured slower than the tokenize itself.
 */
import Prism from "@/lib/prism"
import { LruCache } from "@/lib/lru-cache"
import { tokenizeToRanges, unpackTokenRanges, type TokenRange } from "@/lib/prism-ranges"
import { registerTokenHighlights } from "@/lib/highlight-registry"
import { createWorkerClient } from "@/lib/worker-client"
import type { HighlightWorkRequest, HighlightWorkResponse } from "@/lib/highlight-worker"

export { applyTokenRanges } from "@/lib/highlight-registry"
export type { TokenRange } from "@/lib/prism-ranges"

// Prism language names never contain a space, so the first space delimits
// unambiguously and a key cannot collide across languages however the text
// starts.
const cacheKey = (text: string, language: string) => `${language} ${text}`

// ─── Markup backend ────────────────────────────────────────────────────────

// ~8M characters of HTML — the same order of retention the old 400-entry ×
// ≤100 KB policy allowed in practice, without refusing any document size.
const markupCache = new LruCache<string>(8_000_000)

/** Prism-neutral fallback for a language whose grammar is not loaded. */
function escapeHTML(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
}

/**
 * `text` highlighted as `language`, as HTML for `dangerouslySetInnerHTML`.
 *
 * A language whose grammar is not registered (see `lib/prism.ts` for what is)
 * falls back to escaped plain text rather than throwing — the callers decide
 * what to highlight from content types and file extensions, and an exotic
 * type must degrade to text, not take the row down.
 */
export function highlightCode(text: string, language: string): string {
  const key = cacheKey(text, language)
  const cached = markupCache.get(key)
  if (cached !== undefined) return cached
  // The grammar record's typing claims every key exists; missing languages
  // are real at runtime, hence the assertion.
  const grammar = Prism.languages[language] as Prism.Grammar | undefined
  const html = grammar ? Prism.highlight(text, grammar, language) : escapeHTML(text)
  markupCache.put(key, html, html.length)
  return html
}

// ─── Detection matrix ──────────────────────────────────────────────────────

export type HighlightPresentation = "markup" | "ranges"

/** Chrome 105+, Safari 17.2+, Firefox 140+; everything else takes markup. */
export function supportsHighlightRanges(): boolean {
  return (
    typeof CSS !== "undefined" &&
    typeof Highlight !== "undefined" &&
    (CSS as { highlights?: unknown }).highlights != null
  )
}

/**
 * Which presentation a component should render: a plain text node it lets
 * the ranges backend colour, or the markup backend's HTML. Resolved once at
 * module load — the answer is constant for the life of the page, and the hot
 * callers are virtualized rows rendering on every scroll frame, so they read
 * a constant rather than re-running the probes.
 */
export const HIGHLIGHT_PRESENTATION: HighlightPresentation = supportsHighlightRanges()
  ? "ranges"
  : "markup"

/** Function form of `HIGHLIGHT_PRESENTATION`, for call sites that prefer it. */
export function highlightPresentation(): HighlightPresentation {
  return HIGHLIGHT_PRESENTATION
}

if (HIGHLIGHT_PRESENTATION === "ranges") registerTokenHighlights()

// ─── Ranges backend ────────────────────────────────────────────────────────

/**
 * At or below this many characters, tokenizing on the main thread is cheaper
 * than talking to the worker. Measured on the dev box (Node 24, realistic
 * nested JSON; docs/plans/logs-view-performance.md §3f): sync tokenize is
 * ~0.2 ms at this size — comparable to the round-trip's own main-thread
 * share and without the async second pass — and typical log lines sit far
 * below it, so most rows never message at all. Above it the worker takes
 * over: at 131 KB the main thread pays ~0.03 ms to send plus the unpack,
 * instead of ~4 ms of tokenize.
 */
export const SYNC_TOKENIZE_MAX_CHARS = 8_192

// ~600k tokens of range arrays (~30 MB of small objects at the worst) —
// token count is the honest cost unit here, and the budget admits even a
// 256 KB CloudWatch-cap document (~34k tokens) without letting large
// documents crowd out everything else.
const rangeCache = new LruCache<TokenRange[]>(600_000)
const inFlightRanges = new Map<string, Promise<TokenRange[]>>()

// The correlation/lifecycle machinery lives in the shared kernel
// (lib/worker-client.ts); this module supplies the semantics. failureLimit 2
// = one respawn: a single error (a pathological document, a transient load
// failure) should not cost the whole session its off-thread path, but a
// worker that fails twice is broken in a way retrying won't fix.
const highlightWorker = createWorkerClient<HighlightWorkResponse>({
  create: () => new Worker(new URL("./highlight-worker.ts", import.meta.url), { type: "module" }),
  failureLimit: 2,
})

/**
 * Token spans for `text` under `language`, for `applyTokenRanges`.
 *
 * Synchronous — a plain array — on a cache hit, for a small document, or
 * when no worker is available; a promise only when the tokenize is genuinely
 * off-thread. Callers should apply a synchronous result in the same pass
 * (no flash) and treat a promise as the upgrade path. Duplicate calls for an
 * in-flight text share one promise and one round-trip.
 */
export function requestTokenRanges(
  text: string,
  language: string,
): TokenRange[] | Promise<TokenRange[]> {
  const key = cacheKey(text, language)
  const cached = rangeCache.get(key)
  if (cached !== undefined) return cached
  const inFlight = inFlightRanges.get(key)
  if (inFlight !== undefined) return inFlight
  const finish = (ranges: TokenRange[]): TokenRange[] => {
    inFlightRanges.delete(key)
    rangeCache.put(key, ranges, ranges.length)
    return ranges
  }
  if (text.length <= SYNC_TOKENIZE_MAX_CHARS) return finish(tokenizeToRanges(text, language))
  // The promise NEVER rejects — that is this function's contract, and what
  // the kernel guarantees: worker replies, stranded-work recovery (the
  // fallback below), and failed posts all resolve. Every resolution path
  // flows through `finish`, so an entry can never stay poisoned in
  // `inFlightRanges` — and the kernel answers synchronously (no worker,
  // postMessage threw) exactly where the old code tokenized inline.
  const outcome = highlightWorker.request<TokenRange[]>({
    message: (id) => ({ id, text, language }) satisfies HighlightWorkRequest,
    decode: (reply) => unpackTokenRanges(reply),
    fallback: () => tokenizeToRanges(text, language),
  })
  if (!outcome.async) return finish(outcome.value)
  const work = outcome.promise.then(finish)
  inFlightRanges.set(key, work)
  return work
}
