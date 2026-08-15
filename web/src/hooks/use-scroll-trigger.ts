import { useEffect, useRef, useCallback, type RefObject } from "react"

type ScrollDirection = "down" | "up"

interface UseScrollTriggerOptions {
  /** Called when the sentinel element becomes visible in the observed scroll root. */
  onTrigger: () => void
  /** Whether the trigger is currently active. Set to false to pause (e.g. while loading or when there's nothing left to load). */
  enabled?: boolean
  /** Scroll direction this trigger responds to. "down" (default) places the sentinel at the bottom; "up" at the top (e.g. older messages). */
  direction?: ScrollDirection
  /** IntersectionObserver rootMargin — how far beyond the observed edge to trigger. Default "200px" gives a head-start before the user reaches the edge. */
  rootMargin?: string
  /**
   * Scrollable element containing the sentinel. Omit to observe against the browser viewport.
   *
   * The element must already be mounted the first time `enabled` becomes true. A ref object keeps
   * the same identity across renders, so listing it as an effect dependency does not make the
   * effect re-run when `rootRef.current` goes from `null` to an element — the observer is built
   * once, from whatever `current` held at that moment. If the scroll container mounts in a later
   * commit than the sentinel, the observer is created with `root: null` and silently watches the
   * browser viewport instead: nothing throws, but a sentinel sitting inside an off-screen or
   * overflow-clipped container never intersects, so infinite scroll simply stops loading pages —
   * or, in the opposite case, a sentinel that is inside the viewport but far from the container's
   * scroll edge intersects immediately and loads every page at once.
   *
   * Render the container and the sentinel in the same commit and this does not arise, which is
   * what the current caller does. A container that genuinely mounts later needs a callback ref or
   * a re-subscribe trigger here, not just a dependency entry.
   */
  rootRef?: RefObject<Element | null>
}

/**
 * Returns a ref to attach to a sentinel element (an empty div at the edge of
 * a scrollable list). When the sentinel scrolls into view, `onTrigger` fires.
 *
 * Works for both forward pagination (sentinel at the bottom) and reverse
 * pagination (sentinel at the top, e.g. message threads that load older
 * messages on scroll-up).
 *
 * @example
 * ```tsx
 * const sentinelRef = useScrollTrigger({
 *   onTrigger: () => fetchNextPage(),
 *   enabled: hasNextPage && !isFetchingNextPage,
 * })
 *
 * return (
 *   <div>
 *     {items.map(item => <Row key={item.id} />)}
 *     <div ref={sentinelRef} />
 *   </div>
 * )
 * ```
 */
export function useScrollTrigger({
  onTrigger,
  enabled = true,
  direction = "down",
  rootMargin = "200px",
  rootRef,
}: UseScrollTriggerOptions) {
  const sentinelRef = useRef<HTMLDivElement>(null)
  const callbackRef = useRef(onTrigger)
  // eslint-disable-next-line react-hooks/refs
  callbackRef.current = onTrigger

  const handleIntersect = useCallback(
    (entries: IntersectionObserverEntry[]) => {
      if (entries[0]?.isIntersecting && enabled) {
        callbackRef.current()
      }
    },
    [enabled],
  )

  useEffect(() => {
    const el = sentinelRef.current
    if (!el || !enabled) return

    // Build rootMargin so only the relevant edge triggers.
    // "down": margin on bottom only → fires when scrolling toward the end.
    // "up":   margin on top only   → fires when scrolling toward the start.
    const margin = direction === "down" ? `0px 0px ${rootMargin} 0px` : `${rootMargin} 0px 0px 0px`

    const observer = new IntersectionObserver(handleIntersect, {
      // Read once, when the observer is built. `rootRef` is in the dependency list below for
      // completeness, but a ref's identity never changes, so a container that mounts after this
      // effect first runs is never picked up — see the constraint documented on `rootRef`.
      root: rootRef?.current ?? null,
      rootMargin: margin,
      threshold: 0,
    })

    observer.observe(el)
    return () => observer.disconnect()
  }, [enabled, direction, rootMargin, rootRef, handleIntersect])

  return sentinelRef
}
