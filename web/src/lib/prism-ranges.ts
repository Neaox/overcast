/**
 * Offset-form tokenization — the shared core of the highlight kernel's
 * "ranges" backend (see [highlight-code.ts](./highlight-code.ts)).
 *
 * `Prism.highlight` answers "what does this text look like as HTML"; this
 * module answers the same tokenization as "which byte spans are which token",
 * which is what the CSS Custom Highlight API consumes. Same grammar, same
 * token boundaries, no strings of markup — so it is also what crosses the
 * worker boundary: an array of `{start, end, type}` is structured-clone
 * friendly where a DOM `Range` is not.
 *
 * Imported by both the main thread and the highlight worker; nothing here may
 * touch the DOM.
 */
import Prism from "@/lib/prism"

/** One token's span in the source text, `[start, end)`, in UTF-16 units. */
export interface TokenRange {
  start: number
  end: number
  /**
   * The token's Prism markup classes, space-joined and `token`-less — exactly
   * what `Prism.highlight` would put after `token ` in the span's class
   * (`"string"`, `"null keyword"`). Primary type first, aliases after, so a
   * consumer resolving to one colour walks the list in the same order the
   * markup backend's class selectors effectively do.
   */
  type: string
}

function tokenClasses(token: Prism.Token): string {
  const alias = token.alias as string | string[] | undefined
  if (alias == null) return token.type
  return [token.type, ...(Array.isArray(alias) ? alias : [alias])].join(" ")
}

/**
 * Tokenizes `text` under `language`'s grammar into leaf spans.
 *
 * The spans tile the text: sorted, non-overlapping, and every character is in
 * exactly one span or in an un-tokenized gap (plain text keeps the element's
 * own colour, in both backends). A nested token — markup inside a template
 * string, say — contributes its *leaves*, each labelled with the innermost
 * enclosing token's classes: that is the colour the markup backend paints
 * those characters, since an inner span's own colour rule beats the inherited
 * outer one and no rule of ours matches on ancestry.
 *
 * A language with no registered grammar yields no spans, mirroring the markup
 * backend's escaped-plain-text fallback.
 */
export function tokenizeToRanges(text: string, language: string): TokenRange[] {
  // The grammar record's typing claims every key exists; missing languages
  // are real at runtime, hence the assertion (same note as the facade).
  const grammar = Prism.languages[language] as Prism.Grammar | undefined
  if (!grammar) return []
  const ranges: TokenRange[] = []
  let offset = 0
  const walk = (parts: (string | Prism.Token)[], enclosing: string | null): void => {
    for (const part of parts) {
      if (typeof part === "string") {
        if (enclosing !== null && part.length > 0) {
          ranges.push({ start: offset, end: offset + part.length, type: enclosing })
        }
        offset += part.length
        continue
      }
      const classes = tokenClasses(part)
      const content = part.content
      if (typeof content === "string") {
        ranges.push({ start: offset, end: offset + content.length, type: classes })
        offset += content.length
      } else {
        walk(Array.isArray(content) ? content : [content], classes)
      }
    }
  }
  walk(Prism.tokenize(text, grammar), null)
  return ranges
}

/**
 * A token-range array in transferable form: `(start, end, type-index)`
 * triples over a small table of distinct type strings.
 *
 * Why this exists, measured (see docs/plans/logs-view-performance.md §3f): a
 * structured clone of tens of thousands of little `{start, end, type}`
 * objects costs more than the tokenize that produced them — cloning made a
 * worker a net loss at every document size. A `Uint32Array` moves between
 * threads as a zero-copy transfer, so the packed reply makes the worker's
 * round-trip cost independent of how many tokens it found.
 */
export interface PackedTokenRanges {
  /** `ranges.length * 3` entries: start, end, index into `types`. */
  packed: Uint32Array
  /** Distinct `TokenRange.type` strings, indexed by the triples. */
  types: string[]
}

export function packTokenRanges(ranges: TokenRange[]): PackedTokenRanges {
  const types: string[] = []
  const typeIndex = new Map<string, number>()
  const packed = new Uint32Array(ranges.length * 3)
  for (let i = 0; i < ranges.length; i++) {
    const range = ranges[i]
    let index = typeIndex.get(range.type)
    if (index === undefined) {
      index = types.length
      typeIndex.set(range.type, index)
      types.push(range.type)
    }
    packed[i * 3] = range.start
    packed[i * 3 + 1] = range.end
    packed[i * 3 + 2] = index
  }
  return { packed, types }
}

export function unpackTokenRanges({ packed, types }: PackedTokenRanges): TokenRange[] {
  const ranges = new Array<TokenRange>(packed.length / 3)
  for (let i = 0; i < ranges.length; i++) {
    ranges[i] = { start: packed[i * 3], end: packed[i * 3 + 1], type: types[packed[i * 3 + 2]] }
  }
  return ranges
}

/**
 * Every token class `language`'s grammar can emit — rule names, their
 * aliases, and the same recursively for `inside` sub-grammars.
 *
 * This is the coverage side of the theming contract: the grammar-coverage
 * test enumerates these for each language the ranges backend serves and
 * asserts the theme colours every one, so adding a grammar rule (or a new
 * language) cannot silently render uncoloured.
 */
export function grammarTokenClasses(language: string): Set<string> {
  const classes = new Set<string>()
  const grammar = Prism.languages[language] as Prism.Grammar | undefined
  if (!grammar) return classes
  const seen = new Set<object>()
  const visit = (g: Prism.Grammar): void => {
    if (seen.has(g)) return
    seen.add(g)
    for (const [name, value] of Object.entries(g)) {
      if (name === "rest") {
        if (value != null) visit(value as Prism.Grammar)
        continue
      }
      classes.add(name)
      for (const pattern of Array.isArray(value) ? value : [value]) {
        if (pattern instanceof RegExp || pattern == null) continue
        const { alias, inside } = pattern as { alias?: string | string[]; inside?: Prism.Grammar }
        for (const a of alias == null ? [] : Array.isArray(alias) ? alias : [alias]) {
          classes.add(a)
        }
        if (inside != null) visit(inside)
      }
    }
  }
  visit(grammar)
  return classes
}
