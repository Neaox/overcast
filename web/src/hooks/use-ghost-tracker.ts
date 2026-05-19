/**
 * useGhostTracker — tracks items that have disappeared from a live list.
 *
 * When an item vanishes from the current data set it is kept as a "ghost"
 * for `ttl` ms so the UI can show a fade-out rather than an abrupt removal.
 *
 * Replaces the duplicated ghost-tracking useEffects in MapPage (Lambda
 * instances) and SqsMessageList (SQS messages) with a single reusable hook.
 */

import { useRef, useMemo } from "react"

export interface Ghost<T> {
  item: T
  deletedAt: number
}

interface UseGhostTrackerOptions<T> {
  /** Current live items. */
  items: T[]
  /** Unique key extractor for each item. */
  getKey: (item: T) => string
  /** How long a ghost lingers before removal (ms). */
  ttl: number
}

/**
 * Derives the ghost map synchronously during render — no useEffect, no
 * setState.  Returns a stable Map reference (via useMemo) that only changes
 * when the live item list or the sweep tick changes.
 *
 * The sweep is driven by the live-item changes themselves: every time the
 * parent re-renders with new data the ghosts are re-evaluated.  For a
 * time-based expiry independent of data changes, the caller can pass a tick
 * counter in the dependency that triggers re-render (e.g. the existing 1 s
 * tick in SqsMessageList, or a dedicated setInterval).
 */
export function useGhostTracker<T>({
  items,
  getKey,
  ttl,
}: UseGhostTrackerOptions<T>): Map<string, Ghost<T>> {
  // Mutable ref survives across renders; mutated during render (safe for
  // component-local refs per React docs).
  const prevLive = useRef<Map<string, T>>(new Map())
  const ghostsRef = useRef<Map<string, Ghost<T>>>(new Map())
  const getKeyRef = useRef(getKey)
  getKeyRef.current = getKey

  // Derive ghost state synchronously during render.
  return useMemo(() => {
    // eslint-disable-next-line react-hooks/purity
    const now = Date.now()
    const currentKeys = new Set(items.map((item) => getKeyRef.current(item)))
    const ghosts = ghostsRef.current

    // Promote items that just disappeared to ghosts.
    for (const [key, item] of prevLive.current) {
      if (!currentKeys.has(key) && !ghosts.has(key)) {
        ghosts.set(key, { item, deletedAt: now })
      }
    }

    // Re-appear: if an item comes back, remove its ghost.
    for (const key of currentKeys) {
      ghosts.delete(key)
    }

    // Expire old ghosts.
    for (const [key, ghost] of ghosts) {
      if (now - ghost.deletedAt > ttl) {
        ghosts.delete(key)
      }
    }

    // Snapshot current live items for next diff.
    const nextLive = new Map<string, T>()
    for (const item of items) {
      nextLive.set(getKeyRef.current(item), item)
    }
    prevLive.current = nextLive

    // Return a shallow copy so React sees a new reference when ghosts change.
    return new Map(ghosts)
  }, [items, ttl])
}
