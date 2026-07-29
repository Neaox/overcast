import { useCallback, useEffect, useRef } from "react"

/**
 * Swallows the click that terminates a pointer drag.
 *
 * Once a drag activates, dnd-kit adds its own capture-phase `click` listener on the
 * document that calls `stopPropagation()` — but never `preventDefault()`. That is enough
 * to stop React's synthetic `onClick` (so `<Link>` never gets to cancel the event), yet
 * the browser still performs the anchor's *default* action and hard-navigates to `href`.
 * Dropping a draggable row that contains a link therefore reloads the page.
 *
 * This guard listens at the same level and cancels the default action too. Listeners on
 * the same node and phase all run regardless of `stopPropagation`, and this one is
 * registered on mount — before dnd-kit's, which is added when the drag starts.
 *
 * Call the returned `markDragged` from `onDragStart`: it only fires once the sensor's
 * activation constraint is met, so a click without a drag is left untouched.
 */
export function useDragClickGuard() {
  const dragged = useRef(false)

  useEffect(() => {
    function suppressClick(event: MouseEvent) {
      if (!dragged.current) return
      dragged.current = false
      event.preventDefault()
      event.stopPropagation()
    }

    // A fresh gesture disarms the guard, so a drag that never produced a click (cancelled
    // with Escape, released off-window) cannot swallow an unrelated click later on.
    function disarm() {
      dragged.current = false
    }

    document.addEventListener("click", suppressClick, { capture: true })
    document.addEventListener("pointerdown", disarm, { capture: true })
    return () => {
      document.removeEventListener("click", suppressClick, { capture: true })
      document.removeEventListener("pointerdown", disarm, { capture: true })
    }
  }, [])

  return useCallback(() => {
    dragged.current = true
  }, [])
}
