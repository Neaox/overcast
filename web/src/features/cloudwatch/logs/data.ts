/**
 * CloudWatch Logs query/mutation definitions.
 *
 * Key factory:
 *   logsKeys.all()                          -> ["logs"]
 *   logsKeys.groups()                       -> ["logs", "groups"]
 *   logsKeys.streams(groupName)             -> ["logs", "streams", groupName]
 *   logsKeys.filter(groupName)              -> ["logs", "filter", groupName]
 *   logsKeys.tags(groupName)                -> ["logs", "tags", groupName]
 */

import { queryOptions, infiniteQueryOptions, mutationOptions } from "@tanstack/react-query"
import { logs } from "@/services/api"
import type { CreateLogGroupInput } from "@/services/api/logs"
import { endpointStore } from "@/services/endpoint-store"
import { normalizeLogFilter, resolveLogFilter, type LogFilter } from "./log-filter"

// ─── Key factory ───────────────────────────────────────────────────────────

export const logsKeys = {
  all: () => [...endpointStore.getKeys(), "logs"] as const,
  groups: () => [...logsKeys.all(), "groups"] as const,
  streams: (groupName: string) => [...logsKeys.all(), "streams", groupName] as const,
  filter: (groupName: string) => [...logsKeys.all(), "filter", groupName] as const,
  tags: (groupName: string) => [...logsKeys.all(), "tags", groupName] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function logsGroupsQueryOptions() {
  return queryOptions({
    queryKey: logsKeys.groups(),
    queryFn: () => logs.listGroups(),
  })
}

export function logsGroupTagsQueryOptions(groupName: string) {
  return queryOptions({
    queryKey: logsKeys.tags(groupName),
    queryFn: () => logs.listGroupTags(groupName),
  })
}

export function logsStreamsQueryOptions(groupName: string) {
  return queryOptions({
    queryKey: logsKeys.streams(groupName),
    queryFn: () => logs.listStreams(groupName),
  })
}

export function logsFilterQueryOptions(
  groupName: string,
  opts: {
    filterPattern?: string
    startTime?: number
    endTime?: number
    logStreamNames?: string[]
    logStreamNamePrefix?: string
  } = {},
) {
  return queryOptions({
    queryKey: [...logsKeys.filter(groupName), opts] as const,
    queryFn: () => logs.filterEvents(groupName, opts),
  })
}

/**
 * `LogPanel`'s query: a single FilterLogEvents page for one declarative
 * `LogFilter`.
 *
 * The key is the *normalized filter itself* — never a hand-assembled tuple of
 * its fields — so two components that mean the same filter always share one
 * cache entry, and adding a field to `LogFilter` later (a stream-name prefix,
 * say) is a one-line change here rather than a new key-shape to thread
 * through every call site. The millisecond resolution of a relative window
 * happens inside `queryFn`, not in the key: `Date.now()` read there is what
 * lets auto-refresh slide the window forward without the key itself changing
 * (a key that moved every fetch would never be considered fresh, and every
 * tick would look like a brand new query rather than a refetch of the same
 * one).
 */
export function logPanelQueryOptions(filter: LogFilter) {
  const normalized = normalizeLogFilter(filter)
  return queryOptions({
    queryKey: [...logsKeys.filter(normalized.group), "panel", normalized] as const,
    queryFn: () => {
      const { groupName, opts } = resolveLogFilter(normalized, Date.now())
      return logs.filterEvents(groupName, opts)
    },
  })
}

/** The first backward chunk mirrors the anchored arrival's 15-minute lead-in. */
export const BACKWARD_CHUNK_INITIAL_MS = 15 * 60 * 1000

/**
 * One backward sub-window of the filter query — a plain time range, because
 * FilterLogEvents pages forward only and "load older" is therefore
 * time-window expansion, not reverse pagination. Both bounds are inclusive
 * (FilterLogEvents' endTime is inclusive, unlike GetLogEvents'), so adjacent
 * chunks meet as [a, b], [b + 1, c] and a boundary-millisecond event belongs
 * to exactly one of them.
 */
export interface LogsFilterWindowChunk {
  /** Inclusive start, or null for the final, unbounded close-out chunk. */
  chunkStart: number | null
  /** Inclusive end — one millisecond before the window it extends. */
  chunkEnd: number
}

/**
 * Forward pages ride nextTokens (string); backward pages are time-window
 * chunks; `undefined` is the initial page.
 */
export type LogsFilterPageParam = string | LogsFilterWindowChunk | undefined

function isWindowChunk(param: LogsFilterPageParam): param is LogsFilterWindowChunk {
  // `null` is not in the union, so `object` alone singles out a chunk.
  return typeof param === "object"
}

/**
 * How far backward expansion may run.
 *
 * `floor` is the oldest-event hint — min `firstEventTimestamp` over the
 * streams in view, one cheap DescribeLogStreams instead of probing history
 * with empty range scans. Real AWS updates the field on a best-effort delay,
 * so it is a scheduling hint, never a clip: the final chunk drops its
 * startTime entirely and catches anything older than the hint claims exists.
 */
export interface LogsBackwardExpansion {
  /**
   * True when the window's start was system-chosen (an anchor's lead-in) and
   * may widen; false when the user set it (a hard floor) or the window is
   * already unbounded.
   */
  enabled: boolean
  floor?: number
}

/**
 * The paged form of the filter query, for the stream/all-streams viewer.
 *
 * FilterLogEvents caps a page at 10,000 events and pages forward through the
 * time window via `nextToken`; a viewer that reads one page silently truncates
 * everything past the cap. The key carries a marker so this cache entry never
 * collides with the single-page query's differently-shaped data.
 *
 * With `expansion.enabled`, the query is bidirectional: previous pages extend
 * the window's start backward in doubling chunks (15m, 30m, 1h, …), each
 * fetched as its own fully-paged sub-query. Doubling reaches deep history in
 * O(log) requests over sparse stretches — the emulator's store pushes the
 * time-range predicate down, so a wide-but-empty window is cheap — while dense
 * history self-limits, because the next chunk only loads once the user has
 * scrolled through the previous one. When the next chunk would cross the
 * floor hint (or, with no hint, after a single empty probe), the chunk goes
 * out unbounded and expansion ends for good.
 *
 * `expansion` deliberately stays out of the query key: it steers scheduling,
 * not identity, and keying on it would refetch the world when a streams
 * response lands.
 */
export function logsFilterInfiniteQueryOptions(
  groupName: string,
  opts: {
    filterPattern?: string
    startTime?: number
    endTime?: number
    logStreamNames?: string[]
  } = {},
  expansion?: LogsBackwardExpansion,
) {
  return infiniteQueryOptions({
    queryKey: [...logsKeys.filter(groupName), "infinite", opts] as const,
    queryFn: async ({ pageParam }: { pageParam: LogsFilterPageParam }) => {
      if (isWindowChunk(pageParam)) {
        // A backward chunk is one fully-drained sub-window: its nextTokens are
        // consumed here, so the chunk lands whole and the infinite query's own
        // token walk stays the base window's alone.
        const events = []
        let searchedLogStreams: Awaited<ReturnType<typeof logs.filterEvents>>["searchedLogStreams"]
        let token: string | undefined
        do {
          const page = await logs.filterEvents(groupName, {
            ...opts,
            startTime: pageParam.chunkStart ?? undefined,
            endTime: pageParam.chunkEnd,
            nextToken: token,
          })
          events.push(...page.events)
          searchedLogStreams = page.searchedLogStreams
          token = page.nextToken
        } while (token)
        return { events, searchedLogStreams, nextToken: undefined }
      }
      return logs.filterEvents(groupName, { ...opts, nextToken: pageParam })
    },
    initialPageParam: undefined as LogsFilterPageParam,
    getNextPageParam: (lastPage) => lastPage.nextToken ?? undefined,
    getPreviousPageParam: (
      firstPage,
      _allPages,
      firstPageParam,
    ): LogsFilterWindowChunk | undefined => {
      if (!expansion?.enabled) return undefined
      const currentStart = isWindowChunk(firstPageParam)
        ? firstPageParam.chunkStart
        : (opts.startTime ?? null)
      // An unbounded window already reaches the beginning of history — either
      // it always did, or the final close-out chunk just landed.
      if (currentStart == null) return undefined
      const previousSpan =
        isWindowChunk(firstPageParam) && firstPageParam.chunkStart != null
          ? firstPageParam.chunkEnd - firstPageParam.chunkStart + 1
          : 0
      const span = previousSpan > 0 ? previousSpan * 2 : BACKWARD_CHUNK_INITIAL_MS
      const chunkEnd = currentStart - 1
      const proposedStart = currentStart - span
      if (expansion.floor != null) {
        // The hint says nothing older exists past the floor — close out with
        // an unbounded chunk rather than clipping to it, since a stale hint
        // may sit just above a real event.
        if (proposedStart <= expansion.floor) return { chunkStart: null, chunkEnd }
        return { chunkStart: proposedStart, chunkEnd }
      }
      // No hint: expand until a chunk comes back empty, then close out with
      // one unbounded chunk — a strict cap of one "wasted" probe, never an
      // open-ended search for the start of history.
      if (isWindowChunk(firstPageParam) && firstPage.events.length === 0) {
        return { chunkStart: null, chunkEnd }
      }
      return { chunkStart: proposedStart, chunkEnd }
    },
    // A focus refetch replays every page — including every backward window,
    // each a fully-paged sub-query. Refresh stays manual (the toolbar button)
    // and live arrival is the tail's job.
    refetchOnWindowFocus: false,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createLogGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...logsKeys.groups(), "create"] as const,
    mutationFn: (input: CreateLogGroupInput) => logs.createGroup(input),
  })
}

export function deleteLogGroupMutationOptions() {
  return mutationOptions({
    mutationKey: [...logsKeys.groups(), "delete"] as const,
    mutationFn: (name: string) => logs.deleteGroup(name),
  })
}

export function createLogStreamMutationOptions(groupName: string) {
  return mutationOptions({
    mutationKey: [...logsKeys.streams(groupName), "create"] as const,
    mutationFn: (name: string) => logs.createStream(groupName, name),
  })
}

export function deleteLogStreamMutationOptions(groupName: string) {
  return mutationOptions({
    mutationKey: [...logsKeys.streams(groupName), "delete"] as const,
    mutationFn: (streamName: string) => logs.deleteStream(groupName, streamName),
  })
}
