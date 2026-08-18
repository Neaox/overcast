/**
 * Auto-load the next page of an infinite query as the user scrolls toward the
 * edge of a virtualized list.
 *
 * Extracted from the CloudWatch log stream viewer, which grew the pattern
 * first; the S3 object browser is the second consumer, and any virtualized
 * unbounded listing (DynamoDB items, debug-state pages) is a candidate. See
 * docs/plans/web-ui-dry-refactor.md § "Virtualized unbounded list".
 *
 * ## Why the effect is keyed on the page token, not on booleans
 *
 * The obvious effect — "fetch when near the edge and `hasNextPage` and not
 * `isFetchingNextPage`" — has a race that stalls the chain. `hasNextPage`
 * holds steady from one page to the next, and when a response lands fast
 * enough that no `isFetchingNextPage: true` render ever commits, an effect
 * keyed on the booleans sees nothing change and never fires again: the user
 * sits near the edge, more pages exist, and nothing loads. The pagination
 * token is different for every page, so each arrival re-arms the effect —
 * a fetch is attempted once per (token, nearEdge) combination, which is
 * exactly once per page while the user stays near the edge.
 *
 * ## Why the caller passes indices rather than the virtual items array
 *
 * `virtualizer.getVirtualItems()` returns a fresh array on every scroll
 * frame, so an effect depending on it re-runs at scroll frequency. The hook
 * reduces the indices to a "near the edge" boolean *outside* the effect; the
 * effect's dependencies then only change when the user crosses the threshold
 * or a page arrives, not on every frame.
 */
import { useEffect } from "react"

export interface LoadMoreAtEdgeOptions {
  /** Index of the first rendered row (overscan included), or undefined when none render. */
  firstIndex: number | undefined
  /** Index of the last rendered row (overscan included), or undefined when none render. */
  lastIndex: number | undefined
  /** How many rows the list currently holds. */
  count: number
  /** Which end of the list the next page will attach to. */
  edge: "start" | "end"
  /** How many rows from the edge count as "near". */
  threshold?: number
  /**
   * The pagination cursor of the newest loaded page — `nextToken`,
   * `NextContinuationToken`, a marker pair folded into a string — or
   * null/undefined when no further page exists. This is what re-arms the
   * effect (see module docs); a boolean here reintroduces the stall.
   */
  nextPageToken: unknown
  isFetchingNextPage: boolean
  fetchNextPage: () => unknown
  /** Gate for callers whose paging is sometimes handled elsewhere. */
  enabled?: boolean
}

export function useLoadMoreAtEdge({
  firstIndex,
  lastIndex,
  count,
  edge,
  threshold = 10,
  nextPageToken,
  isFetchingNextPage,
  fetchNextPage,
  enabled = true,
}: LoadMoreAtEdgeOptions): void {
  // Derived in render, so the effect below depends on a boolean that flips at
  // threshold crossings instead of on per-frame virtual-item identities.
  const nearEdge =
    edge === "end"
      ? (lastIndex ?? -1) >= count - threshold
      : (firstIndex ?? Number.MAX_SAFE_INTEGER) < threshold

  useEffect(() => {
    if (!enabled || !nearEdge || nextPageToken == null || isFetchingNextPage) return
    void fetchNextPage()
  }, [enabled, nearEdge, nextPageToken, isFetchingNextPage, fetchNextPage])
}
