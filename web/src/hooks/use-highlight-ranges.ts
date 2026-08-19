/**
 * React binding for the highlight kernel's ranges backend
 * ([highlight-code.ts](../lib/highlight-code.ts)): keep one element's single
 * text node painted with token colours via the CSS Custom Highlight API.
 *
 * Application is imperative on purpose. Ranges are pointers into the DOM,
 * not part of it — applying them mutates nothing and renders nothing — so
 * routing a worker reply through state would buy an extra commit for zero
 * output. The effect applies a synchronous result in the same commit as the
 * text (no uncoloured flash on cached or small documents) and an async one
 * whenever the worker answers, with zero React re-renders either way.
 *
 * Cleanup removes exactly this element's ranges, so mounting, unmounting,
 * and text swaps (Format on/off re-serializes the document) all round-trip
 * leaving the global registry holding only live elements' ranges.
 */
import { useLayoutEffect, type RefObject } from "react"
import { applyTokenRanges, highlightPresentation, requestTokenRanges } from "@/lib/highlight-code"

/**
 * Paints `text`'s token colours over `ref`'s single text node.
 *
 * Pass `text: null` to render un-highlighted — the caller's "not yet" signal
 * (deferred rows mid-scroll, syntax toggle off, markup presentation active).
 * The element must contain exactly the one text node holding `text`;
 * anything else (unexpected children, stale content) skips painting, because
 * misaligned colour is worse than none.
 */
export function useHighlightRanges(
  ref: RefObject<HTMLElement | null>,
  text: string | null,
  language: string,
): void {
  // A layout effect so cleanup + re-apply happen in the same commit that
  // swaps the text: no painted frame can show the old ranges over new text.
  useLayoutEffect(() => {
    if (text === null || highlightPresentation() !== "ranges") return
    const element = ref.current
    if (!element) return
    const node = element.firstChild
    if (!(node instanceof Text) || node.nextSibling !== null || node.data !== text) return
    let dispose: (() => void) | null = null
    let cancelled = false
    const result = requestTokenRanges(text, language)
    if (Array.isArray(result)) {
      dispose = applyTokenRanges(node, result)
    } else {
      void result.then((ranges) => {
        // The reply may land after this row re-rendered other text (Format
        // flipped) or unmounted; the next effect run owns that state.
        if (cancelled || ref.current?.firstChild !== node || node.data !== text) return
        dispose = applyTokenRanges(node, ranges)
      })
    }
    return () => {
      cancelled = true
      dispose?.()
    }
  }, [ref, text, language])
}
