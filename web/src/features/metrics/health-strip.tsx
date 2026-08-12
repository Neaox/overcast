/**
 * HealthStrip — the Metrics & Health page's top-of-page summary row.
 *
 * Combines two data sources so the strip degrades gracefully when
 * OVERCAST_DEBUG is off (the common case — see debugMetricsQueryOptions's
 * doc comment):
 * - GET /_overcast/health (always available): storage mode + healthy/degraded status.
 * - GET /_debug/metrics (debug-gated): live journal mode + last flush time.
 *
 * Uptime is passed in from the parent, which already polls GET /_overcast/metrics for
 * the sparkline cards — no reason to fetch it a second time here.
 */
import { useQuery } from "@tanstack/react-query"
import { cn } from "@/lib/utils"
import { fieldLabel } from "@/lib/typography"
import { Badge, type BadgeProps } from "@/components/ui/badge"
import type { DebugMetricsResponse, HealthResponse } from "@/types"
import { StatPill } from "./stat-pill"
import { healthQueryOptions, debugMetricsQueryOptions, type DebugMetricsResult } from "./data"

type StorageStatus = {
  label: string
  variant: BadgeProps["variant"]
}

function storageStatus(
  health: HealthResponse | undefined,
  debug: DebugMetricsResponse | undefined,
): StorageStatus {
  if (debug?.stores.some((s) => s.degraded)) {
    return { label: "Degraded", variant: "danger" }
  }
  const persistent = health?.storage.persistent
  if (persistent && !persistent.healthy) {
    return { label: "Unhealthy", variant: "warning" }
  }
  if (persistent) {
    return { label: "Healthy", variant: "success" }
  }
  return { label: "Memory-only", variant: "default" }
}

/**
 * The live `PRAGMA journal_mode` readback, or a reason there isn't one to
 * show. The three no-value cases are genuinely different and were previously
 * collapsed into a single "Debug mode required": only a *missing* diagnostics
 * payload is about debug mode. A memory-only backend has no SQLite to have a
 * journal mode at all, and no setting the reader could turn on would give it
 * one — telling them to enable debug mode there sends them nowhere.
 */
function journalModeValue(result: DebugMetricsResult | undefined): string {
  if (!result) return "—" // first poll still in flight
  if (!result.available) return "Debug mode required"
  const reported = result.metrics.stores.find((s) => s.journalMode)?.journalMode
  if (reported) return reported
  // No store reported one: "not applicable" only if none of them is
  // SQLite-backed, otherwise this is a gap in what the server sent (an older
  // binary behind the dev BFF) and claiming otherwise would be a guess.
  const stores = result.metrics.stores
  return stores.length > 0 && stores.every((s) => s.mode === "memory") ? "Not applicable" : "—"
}

/** Most recent flush timestamp across every reporting store, or undefined if none. */
function lastFlushAt(debug: DebugMetricsResponse | undefined): string | undefined {
  const timestamps = (debug?.stores ?? [])
    .flatMap((s) => s.flushHistory ?? [])
    .map((f) => f.timestamp)
    .sort()
  return timestamps.at(-1)
}

export function HealthStrip({ uptime }: { uptime?: string }) {
  const healthQuery = useQuery(healthQueryOptions())
  const debugQuery = useQuery(debugMetricsQueryOptions())
  // Undefined covers both "not fetched yet" and "debug mode is off"; every
  // field below already falls back for the debug-gated half.
  const debug = debugQuery.data?.available ? debugQuery.data.metrics : undefined

  const status = storageStatus(healthQuery.data, debug)
  const storageMode = healthQuery.data?.storage.default ?? debug?.stores[0]?.mode ?? "—"
  const lastFlush = lastFlushAt(debug)

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="flex flex-col gap-0.5 rounded-md border border-border bg-bg-elevated px-3 py-2">
        <span className={cn(fieldLabel, "text-fg-muted")}>Storage</span>
        <div className="flex items-center gap-2">
          <span className="font-mono text-sm font-medium text-fg">{storageMode}</span>
          <Badge variant={status.variant}>{status.label}</Badge>
        </div>
      </div>
      <StatPill label="Journal Mode" value={journalModeValue(debugQuery.data)} />
      <StatPill
        label="Last Flush"
        value={lastFlush ? new Date(lastFlush).toLocaleTimeString() : "No flushes yet"}
      />
      {uptime && <StatPill label="Uptime" value={uptime} />}
    </div>
  )
}
