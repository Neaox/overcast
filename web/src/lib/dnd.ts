import type { Modifier } from "@dnd-kit/core"

/**
 * Pins a drag to the vertical axis.
 *
 * For a list that only reorders vertically, horizontal movement buys nothing — and inside
 * a scrollable container it actively misbehaves: a rightward translate extends the
 * container's scrollable overflow past its right edge, so dnd-kit's auto-scroller stops
 * seeing the container as fully scrolled and starts scrolling it sideways. That scroll is
 * then fed back into the drag transform, pushing the item further right again.
 * `overflow-x: hidden` does not save you — the element is still a scroll container, it
 * just has no visible scrollbar.
 */
export const restrictToVerticalAxis: Modifier = ({ transform }) => ({ ...transform, x: 0 })
