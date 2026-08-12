import { queryOptions } from "@tanstack/react-query"
import { health, debugMetrics } from "@/services/api/misc"
import type { DebugMetricsResponse } from "@/types"

export const metricsHealthKeys = {
  all: ["metrics-health"] as const,
  health: () => [...metricsHealthKeys.all, "health"] as const,
  debugMetrics: () => [...metricsHealthKeys.all, "debug-metrics"] as const,
}

/**
 * GET /_overcast/health, polled — always available (not debug-gated), so this is the
 * baseline data source for the health strip's storage mode / healthy badge
 * even when OVERCAST_DEBUG is off.
 */
export function healthQueryOptions() {
  return queryOptions({
    queryKey: metricsHealthKeys.health(),
    queryFn: () => health.check(),
    staleTime: 3_000,
    refetchInterval: 3_000,
  })
}

/**
 * The outcome of one /_overcast/debug/metrics poll: the diagnostics, or the reason
 * they can't be read right now (`reason` is already human-readable — for the
 * usual case, debug mode being off, it is the server's own explanation).
 */
export type DebugMetricsResult =
  | { available: true; metrics: DebugMetricsResponse }
  | { available: false; reason: string }

/**
 * GET /_overcast/debug/metrics, polled — storage diagnostics (live journal mode,
 * flush history, data-dir probe) and the server-computed advisories array.
 *
 * Unavailability resolves as data rather than rejecting, for two reasons.
 * The first is modelling: a disabled debug namespace (404 `DebugDisabled`,
 * the common case) is an expected answer about the emulator's configuration,
 * not a failure to reach it.
 *
 * The second is that a query which *never* holds data cannot be polled
 * without flickering. TanStack Query rewinds a data-less query to `pending`
 * for the duration of every refetch (`fetchState` in @tanstack/query-core),
 * so a rejecting query on a 3-second `refetchInterval` would drop the
 * advisories list back to its loading skeleton on every single poll — a
 * layout shift every 3 seconds, for as long as the page is open. Resolving
 * keeps `data` defined, which keeps the status at `success` across refetches
 * and leaves the rendered sections still.
 */
export function debugMetricsQueryOptions() {
  return queryOptions({
    queryKey: metricsHealthKeys.debugMetrics(),
    queryFn: async (): Promise<DebugMetricsResult> => {
      try {
        return { available: true, metrics: await debugMetrics.get() }
      } catch (e) {
        return {
          available: false,
          reason: e instanceof Error ? e.message : "Storage diagnostics are unavailable.",
        }
      }
    },
    staleTime: 3_000,
    refetchInterval: 3_000,
  })
}
