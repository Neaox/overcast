import { useState } from "react"

/**
 * True once the surface has been idle at any point in this component's
 * lifetime — the latch behind scroll-time row lightening.
 *
 * A virtualized list mounts and unmounts rows continuously while it scrolls,
 * and everything watching the document pays per node it churns: a Firefox
 * trace of the log stream viewer showed 37% of the main thread inside
 * 1Password's MutationObserver walker, another 6% in Firefox's own form
 * autofill scanner, and ~148k accessibility-tree node removals in ten
 * seconds — none of it app code, all of it fed by rows that mount hundreds
 * of elements (syntax-highlight spans, hover-action buttons) only to be
 * unmounted a frame later.
 *
 * The cure is to mount those rows cheap and hydrate them when the scroll
 * settles. This hook is the per-row switch: seeded false when the row mounts
 * mid-scroll, flipped true on the first idle render, and never flipped back —
 * a row that has hydrated must not shed its DOM when the next scroll starts,
 * or the churn would return as visible flicker.
 *
 * `defer` is the caller's "stay cheap for now" signal — typically
 * `virtualizer.isScrolling || !nearViewport(row, virtualizer)`, both of whose
 * flips already re-render the list. A row first rendered with `defer` false
 * starts (and stays) settled, so the steady state renders exactly what it
 * did before this hook existed.
 */
export function useScrollSettled(defer: boolean): boolean {
  const [settled, setSettled] = useState(!defer)
  // Render-phase adjustment (React's "storing information from previous
  // renders" pattern), not an effect: the latch flips inside the same render
  // pass that first sees the row become worth hydrating, so no un-hydrated
  // frame commits and no cascading second render runs.
  if (!defer && !settled) setSettled(true)
  return settled
}

/**
 * Is a virtual row close enough to the viewport that hydrating it is worth
 * the observers' time? The settle latch alone bounds hydration to *mounted*
 * rows — but mounted includes the whole overscan window, and hydrating it in
 * one commit is where a Format-mode stream of pretty-printed documents froze
 * a real session: ~90 mounted rows × thousands of spans each is a six-figure
 * node insertion, walked by everything watching the document. Gating on
 * proximity keeps each hydration commit to about two viewports' worth; rows
 * further out stay plain until scrolling brings them near, and the latch in
 * the row keeps them hydrated once they have been.
 *
 * Plain data in, boolean out — the caller re-renders on scroll anyway (the
 * virtualizer's onChange), so this needs no subscription of its own.
 */
export function nearViewport(
  item: { start: number; end: number },
  instance: { scrollOffset: number | null; scrollRect: { height: number } | null },
): boolean {
  const offset = instance.scrollOffset ?? 0
  const viewport = instance.scrollRect?.height ?? 800
  return item.start < offset + viewport * 2 && item.end > offset - viewport
}
