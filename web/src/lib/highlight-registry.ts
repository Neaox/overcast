/**
 * The DOM side of the highlight kernel's "ranges" backend: one global
 * `Highlight` object per themed token class, and the bookkeeping that ties a
 * row's text node to the ranges it contributed.
 *
 * Registration model: `CSS.highlights` is a page-global registry, so the
 * `Highlight` objects are page-global too — every row's `string` ranges live
 * in the one `overcast-token-string` highlight, coloured by one
 * `::highlight(overcast-token-string)` rule in global.css. Rows own only
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
 * styled by `::highlight(overcast-token-string)` in global.css.
 */
export const TOKEN_HIGHLIGHT_PREFIX = "overcast-token-"

/**
 * Every token class the theme colours — the single source of truth that the
 * `--token-*` variables, the `.token.*` rules, and the `::highlight()` rules
 * in global.css all mirror, pinned by the grammar-coverage test. A class
 * absent here renders uncoloured in the ranges backend exactly as a class
 * with no `.token.*` rule does in the markup backend.
 */
export const TOKEN_COLOR_CLASSES = [
  "attr-name",
  "attr-value",
  "boolean",
  "comment",
  "doctype",
  "doctype-tag",
  "function",
  "interpolation",
  "keyword",
  "name",
  "null",
  "number",
  "operator",
  "parameter",
  "property",
  "punctuation",
  "selector",
  "string",
  "tag",
  "template-string",
] as const

/**
 * Languages whose full grammar the theme is tested to cover (see
 * `grammarTokenClasses`). A surface adopting the ranges backend for a new
 * language adds it here; the coverage test then forces a `--token-*` variable
 * and `::highlight()` rule for every class that grammar can emit.
 */
export const COVERED_TOKEN_LANGUAGES = ["json"] as const

const colorClasses: ReadonlySet<string> = new Set(TOKEN_COLOR_CLASSES)

/**
 * The one themed class a token's markup classes resolve to, or null for a
 * token the theme does not colour. Primary type first, then aliases — the
 * order `TokenRange.type` carries them — matching which single-class
 * `.token.*` rule effectively colours the same span in the markup backend
 * (classes that share a token in practice share a colour value, so the
 * backends cannot disagree).
 */
export function resolveTokenColorClass(type: string): string | null {
  if (colorClasses.has(type)) return type
  for (const cls of type.split(" ")) {
    if (colorClasses.has(cls)) return cls
  }
  return null
}

let registered = false

/**
 * Puts one named `Highlight` per themed token class into `CSS.highlights`.
 * Idempotent; the facade calls it once at module init when the API exists.
 */
export function registerTokenHighlights(): void {
  if (registered) return
  registered = true
  for (const cls of TOKEN_COLOR_CLASSES) {
    const name = TOKEN_HIGHLIGHT_PREFIX + cls
    if (!CSS.highlights.has(name)) CSS.highlights.set(name, new Highlight())
  }
}

/**
 * Adds `tokenRanges` over `textNode` to the global highlights and returns the
 * disposer that removes exactly those ranges. The caller re-applies (dispose,
 * then apply) whenever the node's text changes; ranges whose offsets overrun
 * the node are skipped rather than clamped, so a stale call cannot paint
 * misaligned colour.
 */
export function applyTokenRanges(textNode: Text, tokenRanges: TokenRange[]): () => void {
  const applied: [Highlight, Range][] = []
  const length = textNode.data.length
  for (const token of tokenRanges) {
    if (token.end > length) continue
    const cls = resolveTokenColorClass(token.type)
    if (cls === null) continue
    const highlight = CSS.highlights.get(TOKEN_HIGHLIGHT_PREFIX + cls)
    if (!highlight) continue
    const range = document.createRange()
    range.setStart(textNode, token.start)
    range.setEnd(textNode, token.end)
    highlight.add(range)
    applied.push([highlight, range])
  }
  return () => {
    for (const [highlight, range] of applied) highlight.delete(range)
  }
}
