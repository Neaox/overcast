import { useEffect, useRef, useCallback } from "react"

type ScrollDirection = "down" | "up"

interface UseScrollTriggerOptions {
  /** Called when the sentinel element becomes visible in the viewport. */
  onTrigger: () => void
  /** Whether the trigger is currently active. Set to false to pause (e.g. while loading or when there's nothing left to load). */
  enabled?: boolean
  /** Scroll direction this trigger responds to. "down" (default) places the sentinel at the bottom; "up" at the top (e.g. older messages). */
  direction?: ScrollDirection
  /** IntersectionObserver rootMargin — how far outside the viewport to trigger. Default "200px" gives a head-start before the user reaches the edge. */
  rootMargin?: string
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
      rootMargin: margin,
      threshold: 0,
    })

    observer.observe(el)
    return () => observer.disconnect()
  }, [enabled, direction, rootMargin, handleIntersect])

  return sentinelRef
}
