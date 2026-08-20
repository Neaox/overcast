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
import { applyTokenRanges, requestTokenRanges } from "@/lib/highlight-code"

/**
 * Paints `text`'s token colours over `ref`'s first text node.
 *
 * Pass `text: null` to render un-highlighted — the caller's "not yet" signal
 * (deferred rows mid-scroll, syntax toggle off, markup presentation active).
 * That null contract is the hook's ONLY gate: the caller owns presentation
 * selection, exactly as the facade's header promises, so there is no second
 * detection site here to fall out of agreement with the first.
 *
 * The element's first child must be the text node holding `text` — that node
 * is what the ranges anchor to, so later siblings (an extension-injected
 * overlay, say) cannot misalign anything and are tolerated. A first child
 * that is not our text (split, rewritten, replaced) skips painting entirely:
 * `applyTokenRanges` is all-or-nothing on the text match.
 */
export function useHighlightRanges(
  ref: RefObject<HTMLElement | null>,
  text: string | null,
  language: string,
): void {
  // A layout effect so cleanup + re-apply happen in the same commit that
  // swaps the text: no painted frame can show the old ranges over new text.
  useLayoutEffect(() => {
    if (text === null) return
    const element = ref.current
    if (!element) return
    const node = element.firstChild
    if (!(node instanceof Text)) return
    let dispose: (() => void) | null = null
    let cancelled = false
    const result = requestTokenRanges(text, language)
    if (Array.isArray(result)) {
      dispose = applyTokenRanges(node, text, result)
    } else {
      // The facade's promise never rejects (its contract — worker failures
      // settle through the synchronous fallback), so no rejection path here.
      void result.then((ranges) => {
        // The reply may land after this row re-rendered other text (Format
        // flipped) or unmounted; the next effect run owns that state.
        if (cancelled || ref.current?.firstChild !== node) return
        dispose = applyTokenRanges(node, text, ranges)
      })
    }
    return () => {
      cancelled = true
      dispose?.()
    }
  }, [ref, text, language])
}
