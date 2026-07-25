import { queryOptions } from "@tanstack/react-query"
import { health, debugMetrics } from "@/services/api/misc"

export const metricsHealthKeys = {
  all: ["metrics-health"] as const,
  health: () => [...metricsHealthKeys.all, "health"] as const,
  debugMetrics: () => [...metricsHealthKeys.all, "debug-metrics"] as const,
}

/**
 * GET /_health, polled — always available (not debug-gated), so this is the
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
 * GET /_debug/metrics, polled — storage diagnostics (live journal mode,
 * flush history, data-dir probe) and the server-computed advisories array.
 * Only available when OVERCAST_DEBUG=true; when disabled, the query's
 * `error` carries a human-readable explanation (see debugMetrics.get's doc
 * comment) that callers render the same way debug-page.tsx already does for
 * the raw-state debugger — as the error message text, not a special case.
 * `retry: false` because a disabled debug namespace won't start responding
 * by retrying the same request.
 */
export function debugMetricsQueryOptions() {
  return queryOptions({
    queryKey: metricsHealthKeys.debugMetrics(),
    queryFn: () => debugMetrics.get(),
    staleTime: 3_000,
    refetchInterval: 3_000,
    retry: false,
  })
}
