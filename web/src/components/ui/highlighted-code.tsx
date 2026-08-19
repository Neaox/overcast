/**
 * The one way a syntax-highlighted block of code renders.
 *
 * The highlight kernel ([highlight-code.ts](../../lib/highlight-code.ts)) has
 * two presentations — token colours painted as CSS Custom Highlight ranges
 * over ONE text node where the browser supports it, cached Prism markup via
 * `dangerouslySetInnerHTML` where it does not — plus a deferral latch for
 * virtualized surfaces that mount rows mid-scroll. That three-way fork
 * (ranges | settled markup | deferred plain) used to live inline in
 * `LogMessage`, and PR #1070's review found the S3 preview about to re-derive
 * it; this component is the fork's single home. Consumers choose *text,
 * language, and classes* — never a backend.
 *
 * Layout stability is part of the contract: every branch renders the same
 * `<pre>` with the same `className`, and Prism markup only wraps text in
 * spans, so no backend switch, deferral settle, or async worker reply can
 * change the pixels a measured row occupies — colour arrives, layout never
 * moves. (The wrap classes are the caller's to pick, which is also where a
 * surface that wraps plain text but scrolls code expresses that policy.)
 */
import { memo, useRef } from "react"
import { HIGHLIGHT_PRESENTATION, highlightCode } from "@/lib/highlight-code"
import { useHighlightRanges } from "@/hooks/use-highlight-ranges"
import { useScrollSettled } from "@/hooks/use-scroll-settled"

/**
 * Memoised for the same reason `LogMessage` is: the hot consumers are
 * virtualized rows whose list flush-syncs a render on every scroll event.
 * Every prop is a primitive, so an unchanged block re-renders to identical
 * output without re-entering the kernel.
 */
export const HighlightedCode = memo(function HighlightedCode({
  text,
  language,
  defer = false,
  className,
}: {
  text: string
  /**
   * Prism language to highlight as, or null to render the text plain — the
   * caller's policy decision (an unrecognised content type, a document too
   * large to format). Null renders the same `<pre>`, uncoloured.
   */
  language: string | null
  /**
   * Defer the highlight while true: the text renders plain (under the markup
   * backend that skips mounting hundreds of spans; under ranges it merely
   * withholds colour) and hydrates on the first render where `defer` is
   * false, never shedding it again. See `useScrollSettled` for the numbers.
   */
  defer?: boolean
  /** Applied identically to every branch's `<pre>` — see the layout note above. */
  className?: string
}) {
  const settled = useScrollSettled(defer)
  // Where the browser has the CSS Custom Highlight API, a highlighted block
  // is ONE text node and token colour arrives as ranges — no spans to mount,
  // so the `defer` latch gates only the (mutation-free) range application,
  // not the DOM. Elsewhere the markup path renders exactly what it always
  // has.
  const ranges = HIGHLIGHT_PRESENTATION === "ranges"
  const highlighted = language !== null
  const preRef = useRef<HTMLPreElement>(null)
  useHighlightRanges(
    preRef,
    ranges && settled && highlighted ? text : null,
    // The hook ignores the language while its text is null; "text" has no
    // registered grammar, so even a misroute would yield zero ranges.
    language ?? "text",
  )

  if (!highlighted || ranges || !settled) {
    // One text node, whichever reason: plain policy renders nothing else,
    // under the ranges presentation the text node IS the rendering (settled
    // or not — colour arrives as ranges via `useHighlightRanges`, zero DOM
    // mutation), and under markup a deferred block renders the same pixels
    // un-highlighted until it settles. Identical element and classes in
    // every case, so no swap can move a measured row.
    return (
      <pre ref={highlighted && ranges ? preRef : undefined} className={className}>
        {text}
      </pre>
    )
  }
  return (
    <pre
      className={className}
      dangerouslySetInnerHTML={{ __html: highlightCode(text, language) }}
    />
  )
})
