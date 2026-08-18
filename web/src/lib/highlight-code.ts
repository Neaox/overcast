/**
 * PrismJS syntax highlighting with a memo, generalized over languages.
 *
 * Same design as `highlightJSON` in `lib/log-format.ts` (the original, grown
 * for the virtualized log viewers; the logs feature is to be re-pointed at
 * this module in a follow-up), with the language folded into the cache key.
 *
 * Why cached: the hot callers are virtualized rows — `@tanstack/react-virtual`
 * flush-syncs a render on every scroll event, so an uncached tokenise ran once
 * per visible highlighted row per scroll frame, which a Firefox profile of the
 * log stream viewer showed as 80–247 ms tasks inside the scroll handler.
 * Highlighting is a pure function of (text, language), so a repeat render is a
 * map lookup.
 *
 * Returning the *identical* string on a hit matters as much as the saved
 * work: React skips the DOM write when the value handed to
 * `dangerouslySetInnerHTML` is unchanged, and a row that mutates nothing
 * costs nothing downstream — no style recalc, and no MutationObserver record
 * for whatever extensions the user has installed. An equal-but-new string
 * would replace the nodes for identical content.
 */
import Prism from "@/lib/prism"

const CACHE_LIMIT = 400
/** Above this, a document is cheaper to re-highlight than to hold onto. */
const CACHE_MAX_CHARS = 100_000
const cache = new Map<string, string>()

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
  // Prism language names never contain a space, so the first space delimits
  // unambiguously and the key cannot collide across languages however the
  // text starts.
  const key = `${language} ${text}`
  const cached = cache.get(key)
  if (cached !== undefined) {
    // Re-insert so the map's insertion order is least-recently-used first.
    cache.delete(key)
    cache.set(key, cached)
    return cached
  }
  // The grammar record's typing claims every key exists; missing languages
  // are real at runtime, hence the assertion.
  const grammar = Prism.languages[language] as Prism.Grammar | undefined
  const html = grammar ? Prism.highlight(text, grammar, language) : escapeHTML(text)
  if (text.length <= CACHE_MAX_CHARS) {
    if (cache.size >= CACHE_LIMIT) {
      const oldest = cache.keys().next().value
      if (oldest !== undefined) cache.delete(oldest)
    }
    cache.set(key, html)
  }
  return html
}
