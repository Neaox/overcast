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

// Class → Highlight, filled at registration: the per-token hot loop below
// does one bounded-map lookup instead of a string concat plus a
// `CSS.highlights.get` per token.
const highlightByClass = new Map<string, Highlight>()

/**
 * Puts one named `Highlight` per themed token class into `CSS.highlights`.
 * Idempotent; the facade calls it once at module init when the API exists.
 */
export function registerTokenHighlights(): void {
  if (registered) return
  registered = true
  for (const cls of TOKEN_COLOR_CLASSES) {
    const name = TOKEN_HIGHLIGHT_PREFIX + cls
    let highlight = CSS.highlights.get(name)
    if (!highlight) {
      highlight = new Highlight()
      CSS.highlights.set(name, highlight)
    }
    highlightByClass.set(cls, highlight)
  }
}

const noopDispose = () => {}

/**
 * Adds `tokenRanges` over `textNode` to the global highlights and returns the
 * disposer that removes exactly those ranges.
 *
 * `text` is the source the ranges were tokenized from, and the guard is
 * all-or-nothing: a node whose data is not exactly that text gets NO colour,
 * because partially- or mis-aligned colour is worse than none. The guard
 * lives here — not in the calling hook — so every consumer of the kernel
 * inherits it. The caller re-applies (dispose, then apply) whenever the
 * node's text changes.
 */
export function applyTokenRanges(
  textNode: Text,
  text: string,
  tokenRanges: TokenRange[],
): () => void {
  if (textNode.data !== text) return noopDispose
  const appliedRanges: Range[] = []
  const appliedHighlights: Highlight[] = []
  for (const token of tokenRanges) {
    const cls = resolveTokenColorClass(token.type)
    if (cls === null) continue
    const highlight = highlightByClass.get(cls)
    if (!highlight) continue
    const range = document.createRange()
    range.setStart(textNode, token.start)
    range.setEnd(textNode, token.end)
    highlight.add(range)
    appliedRanges.push(range)
    appliedHighlights.push(highlight)
  }
  return () => {
    for (let i = 0; i < appliedRanges.length; i++) appliedHighlights[i].delete(appliedRanges[i])
  }
}
