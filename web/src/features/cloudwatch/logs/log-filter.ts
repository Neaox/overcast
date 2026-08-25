/**
 * The declarative vocabulary for "which logs" that `LogPanel` (and, later,
 * anything else that wants FilterLogEvents/GetLogEvents semantics without
 * hand-assembling a request object) filters through.
 *
 * This exists because the obvious alternative — a pile of `startTime`,
 * `endTime`, `logStreamNames`, `filterPattern` props threaded straight into a
 * query — is how `MonitorTab`'s old inline query, the flagship stream
 * viewer's window math, and `LogPanel`'s first draft each grew a slightly
 * different idea of what "the same filter" means. A `LogFilter` is the one
 * shape every reader normalizes into, `resolveLogFilter` is the one function
 * that turns it into a wire request, and a query key derived from the
 * `LogFilter` itself (see `logPanelQueryOptions` in `./data`) is mechanical
 * rather than hand-assembled per call site.
 *
 * `time` is a discriminated union on purpose: a *relative* window ("last
 * hour") is meant to slide forward as auto-refresh re-fetches, which only
 * works if the millisecond bounds are computed fresh at fetch time rather
 * than baked into the filter (and, transitively, into the query key — a
 * sliding key would refetch on every tick and never settle). An *absolute*
 * window ("this invocation's `[acquiredAt, releasedAt]`") is exactly the
 * opposite: it must never move no matter how many times auto-refresh re-polls
 * it, because the whole point is "everything this one invocation logged",
 * not "whatever falls in a rolling window that happens to overlap it".
 */

export type RelativeLogWindowToken = "15m" | "1h" | "6h" | "24h"

export type LogTimeFilter =
  | { kind: "relative"; token: RelativeLogWindowToken }
  | { kind: "absolute"; startMs: number; endMs: number }

/** Every relative window token names, in milliseconds. */
export const RELATIVE_LOG_WINDOW_MS: Record<RelativeLogWindowToken, number> = {
  "15m": 15 * 60 * 1000,
  "1h": 60 * 60 * 1000,
  "6h": 6 * 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
}

export const RELATIVE_LOG_WINDOW_LABELS: Record<RelativeLogWindowToken, string> = {
  "15m": "Last 15 minutes",
  "1h": "Last hour",
  "6h": "Last 6 hours",
  "24h": "Last 24 hours",
}

/**
 * A log group, optionally narrowed to one or more streams, a time window, a
 * filter pattern and a result cap — every field FilterLogEvents/GetLogEvents
 * can be asked to narrow by, and nothing else.
 */
export interface LogFilter {
  group: string
  /** Convenience for the common single-stream case; equivalent to `streams: [stream]`. */
  stream?: string
  /** Multiple streams, for the rare caller that wants more than one but not the whole group. */
  streams?: string[]
  time: LogTimeFilter
  pattern?: string
  limit?: number
}

/** Deep-equality-friendly normal form, so two callers who mean the same
 * filter produce the same query key regardless of how they built the object
 * (an empty-string pattern and an absent one, `stream: "x"` vs `streams:
 * ["x"]`, …). This is what `logPanelQueryOptions` keys its query on. */
export function normalizeLogFilter(filter: LogFilter): LogFilter {
  const streams = filter.streams?.length
    ? filter.streams
    : filter.stream
      ? [filter.stream]
      : undefined
  const pattern = filter.pattern?.trim() || undefined
  return {
    group: filter.group,
    ...(streams ? { streams } : {}),
    time: filter.time,
    ...(pattern ? { pattern } : {}),
    ...(filter.limit != null ? { limit: filter.limit } : {}),
  }
}

/** The FilterLogEvents call shape `logs.filterEvents(groupName, opts)` accepts. */
export interface ResolvedLogQuery {
  groupName: string
  opts: {
    filterPattern?: string
    startTime?: number
    endTime?: number
    logStreamNames?: string[]
    limit?: number
  }
}

/**
 * The one function that turns a `LogFilter` into a FilterLogEvents request.
 *
 * `now` is a parameter rather than read internally so a relative window
 * resolves fresh on every call — auto-refresh re-reads it and the window
 * slides forward — while staying deterministic for callers, tests included,
 * that pin an instant. An absolute window ignores `now` entirely, which is
 * what makes it immune to the sliding a relative one wants.
 */
export function resolveLogFilter(filter: LogFilter, now: number): ResolvedLogQuery {
  const streams = filter.streams?.length
    ? filter.streams
    : filter.stream
      ? [filter.stream]
      : undefined
  // `startTime` is set either way — a relative window is open-ended on its
  // *end* only (still "up to now" every time it is fetched), while an
  // absolute window pins both.
  const { startTime, endTime } =
    filter.time.kind === "relative"
      ? { startTime: now - RELATIVE_LOG_WINDOW_MS[filter.time.token], endTime: undefined }
      : { startTime: filter.time.startMs, endTime: filter.time.endMs }
  return {
    groupName: filter.group,
    opts: {
      ...(filter.pattern ? { filterPattern: filter.pattern } : {}),
      startTime,
      ...(endTime != null ? { endTime } : {}),
      ...(streams ? { logStreamNames: streams } : {}),
      ...(filter.limit != null ? { limit: filter.limit } : {}),
    },
  }
}
