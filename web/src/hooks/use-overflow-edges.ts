import * as React from "react"

/** Which side of a scroller still has content past the visible edge. */
export interface OverflowEdges {
  start: boolean
  end: boolean
}

const NO_OVERFLOW: OverflowEdges = { start: false, end: false }

/**
 * Sub-pixel slack. A scroller's `scrollWidth` and `clientWidth` are integers
 * rounded from fractional layout, so a table that fits exactly can report one
 * pixel of overflow — and a permanent one-pixel shadow on a table nobody can
 * scroll is worse than no shadow at all.
 */
const EPSILON = 1

/** Measures a scroller. Pure, so the boundary condition is testable without layout. */
export function overflowEdges(el: {
  scrollLeft: number
  scrollWidth: number
  clientWidth: number
}): OverflowEdges {
  // `scrollLeft` is negative in a right-to-left scroller, so both edges are
  // measured as distances rather than compared against zero.
  const left = Math.abs(el.scrollLeft)
  const max = el.scrollWidth - el.clientWidth
  if (max <= EPSILON) return NO_OVERFLOW
  return { start: left > EPSILON, end: left < max - EPSILON }
}

/**
 * Reports whether a horizontal scroller has more content off either edge, so a
 * caller can draw the affordance that says so.
 *
 * A table whose columns outrun the pane scrolls sideways, and a scrollbar is
 * the only thing that says so — one that overlays on macOS and Windows 11 and
 * fades out when the pointer stops, which is to say it says so to nobody. At a
 * narrow width that is the difference between a column being hard to reach and
 * a column being invisible (#1611), so the edge has to state it.
 *
 * Both the scroller and its content are observed: the content changes width
 * without the scroller moving whenever a column is toggled off, rows load, or
 * a cell's text arrives.
 */
export function useOverflowEdges<T extends HTMLElement>(): OverflowEdges & {
  ref: React.RefObject<T | null>
} {
  const ref = React.useRef<T>(null)
  const [edges, setEdges] = React.useState<OverflowEdges>(NO_OVERFLOW)

  React.useEffect(() => {
    const el = ref.current
    if (!el) return

    const measure = () =>
      setEdges((previous) => {
        const next = overflowEdges(el)
        // Bail on an unchanged reading: this runs on every scroll event, and a
        // fresh object each time would re-render the whole table per frame.
        return previous.start === next.start && previous.end === next.end ? previous : next
      })

    measure()
    el.addEventListener("scroll", measure, { passive: true })
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    if (el.firstElementChild) observer.observe(el.firstElementChild)
    return () => {
      el.removeEventListener("scroll", measure)
      observer.disconnect()
    }
    // The scroller and the element inside it are both created by the render
    // that mounts this hook and neither is replaced afterwards — the observer
    // is what catches the content growing, so nothing here needs re-running.
  }, [])

  return { ref, ...edges }
}
