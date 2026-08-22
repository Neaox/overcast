/**
 * useGhostTracker — tracks items that have disappeared from a live list.
 *
 * When an item vanishes from the current data set it is kept as a "ghost"
 * for `ttl` ms so the UI can show a fade-out rather than an abrupt removal.
 *
 * Replaces the duplicated ghost-tracking useEffects in MapPage (Lambda
 * instances) and SqsMessageList (SQS messages) with a single reusable hook.
 */

import { useState } from "react"

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
 * What the tracker remembers between renders: the live items it last saw (so it
 * can tell what has since vanished) and the ghosts standing right now.
 */
interface Tracked<T> {
  live: Map<string, T>
  ghosts: Map<string, Ghost<T>>
}

function sameEntries<V>(a: Map<string, V>, b: Map<string, V>): boolean {
  if (a.size !== b.size) return false
  for (const [key, value] of a) {
    if (!b.has(key) || b.get(key) !== value) return false
  }
  return true
}

/**
 * Fold one observation of the live list into the tracker's memory.
 *
 * Pure, and returns `prev` unchanged when nothing moved — which is what stops a
 * caller that rebuilds its `items` array every render (SqsMessageList spreads
 * one) from re-rendering for ever.
 */
function track<T>(
  prev: Tracked<T>,
  items: T[],
  getKey: (item: T) => string,
  ttl: number,
  now: number,
): Tracked<T> {
  const live = new Map<string, T>()
  for (const item of items) live.set(getKey(item), item)

  const ghosts = new Map<string, Ghost<T>>()
  // Keep the ghosts that are still owed a fade-out: dropping the ones that came
  // back, and the ones whose ttl has run out.
  for (const [key, ghost] of prev.ghosts) {
    if (live.has(key) || now - ghost.deletedAt > ttl) continue
    ghosts.set(key, ghost)
  }
  // Promote whatever was live last time and is not any more.
  for (const [key, item] of prev.live) {
    if (live.has(key) || ghosts.has(key)) continue
    ghosts.set(key, { item, deletedAt: now })
  }

  return sameEntries(prev.live, live) && sameEntries(prev.ghosts, ghosts) ? prev : { live, ghosts }
}

/**
 * Derives the ghost map from the live list — no useEffect, no polling.
 *
 * The diff needs the *previous* live list, which is state, not a scratch ref:
 * this hook used to keep both the previous list and the ghost map in refs and
 * mutate them inside a `useMemo`, which is exactly the render-phase side effect
 * React forbids. A render that gets thrown away (Strict Mode, a concurrent
 * re-render, a suspended sibling) still ran the mutation, so a vanished item
 * could be recorded as a ghost against a render nobody ever saw and then never
 * promoted again — the fade-out simply not happening. Held in state and updated
 * with React's "adjust state when a prop changes" rule, the fold is pure and
 * runs exactly once per observation.
 *
 * The sweep is driven by the live-item changes themselves: every time the
 * parent re-renders with new data the ghosts are re-evaluated. For a time-based
 * expiry independent of data changes, the caller can pass a tick counter in the
 * dependency that triggers re-render (e.g. the existing 1 s tick in
 * SqsMessageList, or a dedicated setInterval).
 */
export function useGhostTracker<T>({
  items,
  getKey,
  ttl,
}: UseGhostTrackerOptions<T>): Map<string, Ghost<T>> {
  // Seeded from the first observation: nothing can have vanished yet, and
  // starting empty would only buy a discarded render on mount.
  const [tracked, setTracked] = useState<Tracked<T>>(() => ({
    live: new Map(items.map((item) => [getKey(item), item])),
    ghosts: new Map(),
  }))

  // eslint-disable-next-line react-hooks/purity -- ghost expiry is wall-clock by nature; the fold below is otherwise pure
  const next = track(tracked, items, getKey, ttl, Date.now())
  if (next !== tracked) setTracked(next)

  return next.ghosts
}
