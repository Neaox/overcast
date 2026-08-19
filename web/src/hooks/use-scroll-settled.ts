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
 * `scrolling` comes from `virtualizer.isScrolling`, whose flips already
 * re-render the list; rows that were mounted while idle start (and stay)
 * settled, so the steady state renders exactly what it did before this hook
 * existed.
 */
export function useScrollSettled(scrolling: boolean): boolean {
  const [settled, setSettled] = useState(!scrolling)
  // Render-phase adjustment (React's "storing information from previous
  // renders" pattern), not an effect: the latch flips inside the same render
  // pass that first sees the scroll go idle, so no un-hydrated frame commits
  // and no cascading second render runs.
  if (!scrolling && !settled) setSettled(true)
  return settled
}
