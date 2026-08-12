import { useEffect, useMemo } from "react"
import { useInfiniteQuery, infiniteQueryOptions } from "@tanstack/react-query"
import { useDebouncedValue } from "@tanstack/react-pacer"
import { debugTrace } from "@/services/api/misc"
import { debugTraceKeys } from "./data"
import type { TraceMatch, TraceSearchResponse } from "@/types"

/**
 * How long the query must be still before a deep scan starts.
 *
 * The list's own search settles at 300 ms, which is the right protection for a
 * filter costing microseconds. This one reads bodies — up to 8 MiB a trace —
 * so settling at the same speed would launch a full-ring scan for every prefix
 * the typist pauses on: `persi`, `persistent`, `persistent state`, none of
 * which was the query they meant. The cheap answer stays immediate; the
 * expensive one waits until they have stopped editing.
 */
export const DEEP_SEARCH_SETTLE_MS = 800

/**
 * The shortest query the server will scan for. Mirrors minDeepSearchQuery in
 * internal/router/debug.go, which refuses anything shorter — so the UI holds
 * back rather than sending a request it knows will be turned down.
 *
 * A one- or two-character query matches nearly every body in the ring: it is
 * what a search box looks like halfway through a word, never what anyone meant.
 */
export const MIN_DEEP_SEARCH_QUERY = 3

export interface DeepSearchState {
  /** Matches found so far, in the order the server returned them (newest first). */
  matches: TraceMatch[]
  /** True once a scan is running or has run for the settled query. */
  active: boolean
  /** True while a page is in flight. */
  scanning: boolean
  /** The scan reached the oldest retained trace. */
  done: boolean
  /** Traces looked at so far, and how many are left — for a progress readout. */
  scanned: number
  remaining: number
  /** The settled query the results belong to, which lags what is in the box. */
  query: string
}

/**
 * Drives the deep scan of trace bodies, hop errors and log entries.
 *
 * The server scans one budget's worth per call and hands back a cursor, so this
 * keeps asking until the scan reports itself done — the pages are progress, not
 * pagination, and the caller renders them as they arrive rather than waiting
 * for the last one.
 *
 * Cancellation is the part that matters and it is why `signal` is threaded
 * through. React Query aborts the in-flight fetch when the query key changes or
 * the component unmounts; that closes the connection, the bff propagates it via
 * `doGet(r.Context(), …)`, and the scan checks its context between hops. Without
 * the signal the request would be abandoned but the emulator would carry on
 * scanning gigabytes for a result nobody is going to read.
 *
 * @param query    the raw text in the search box
 * @param enabled  whether to scan at all — callers gate this on the cheap search
 *                 having come up short, so a query the list already answered
 *                 does not spend the budget
 */
export function useDeepSearch(query: string, enabled: boolean): DeepSearchState {
  const [settled] = useDebouncedValue(query, { wait: DEEP_SEARCH_SETTLE_MS })
  const trimmed = settled.trim()

  // Below the minimum the server refuses outright, so asking would produce a
  // 400 the UI would have to explain. Holding back is the same answer, sooner.
  const active = enabled && trimmed.length >= MIN_DEEP_SEARCH_QUERY

  const options = infiniteQueryOptions({
    queryKey: [...debugTraceKeys.all, "deep", trimmed] as const,
    queryFn: ({ pageParam, signal }) => debugTrace.search({ q: trimmed, cursor: pageParam }, signal),
    getNextPageParam: (last: TraceSearchResponse) => (last.done ? undefined : last.nextCursor),
    initialPageParam: undefined as string | undefined,
    enabled: active,
    // A scan is a point-in-time answer over a ring that is still moving. Retrying
    // it on focus or reconnect would silently restart a multi-second walk that
    // the reader has already got their answer from.
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  })
  const { data, fetchNextPage, hasNextPage, isFetching } = useInfiniteQuery(options)

  // Keep asking until the scan says it has reached the oldest retained trace.
  // The budget exists to bound one call, not to stop the search — so the client
  // drives it to exhaustion, one page at a time, staying responsive throughout.
  useEffect(() => {
    if (active && hasNextPage && !isFetching) void fetchNextPage()
  }, [active, hasNextPage, isFetching, fetchNextPage])

  return useMemo(() => {
    const pages = data?.pages ?? []
    const last = pages.at(-1)
    return {
      matches: pages.flatMap((p) => p.matches),
      active,
      scanning: isFetching,
      done: last?.done ?? false,
      scanned: pages.reduce((total, p) => total + p.scanned, 0),
      remaining: last?.remaining ?? 0,
      query: trimmed,
    }
  }, [data, active, isFetching, trimmed])
}
