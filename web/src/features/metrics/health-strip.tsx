/**
 * HealthStrip — the Metrics & Health page's top-of-page summary row.
 *
 * Combines two data sources so the strip degrades gracefully when
 * OVERCAST_DEBUG is off (the common case — see debugMetricsQueryOptions's
 * doc comment):
 * - GET /_health (always available): storage mode + healthy/degraded status.
 * - GET /_debug/metrics (debug-gated): live journal mode + last flush time.
 *
 * Uptime is passed in from the parent, which already polls GET /_metrics for
 * the sparkline cards — no reason to fetch it a second time here.
 */
import { useQuery } from "@tanstack/react-query"
import { Badge, type BadgeProps } from "@/components/ui/badge"
import type { DebugMetricsResponse, HealthResponse } from "@/types"
import { StatPill } from "./stat-pill"
import { healthQueryOptions, debugMetricsQueryOptions } from "./data"

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

  const status = storageStatus(healthQuery.data, debugQuery.data)
  const storageMode = healthQuery.data?.storage.default ?? debugQuery.data?.stores[0]?.mode ?? "—"
  const journalMode = debugQuery.data?.stores.find((s) => s.journalMode)?.journalMode
  const lastFlush = lastFlushAt(debugQuery.data)

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="flex flex-col gap-0.5 rounded-md border border-border bg-bg-elevated px-3 py-2">
        <span className="font-mono text-[10px] font-medium tracking-wider text-fg-muted uppercase">
          Storage
        </span>
        <div className="flex items-center gap-2">
          <span className="font-mono text-sm font-medium text-fg">{storageMode}</span>
          <Badge variant={status.variant}>{status.label}</Badge>
        </div>
      </div>
      <StatPill label="Journal Mode" value={journalMode ?? "Debug mode required"} />
      <StatPill
        label="Last Flush"
        value={lastFlush ? new Date(lastFlush).toLocaleTimeString() : "No flushes yet"}
      />
      {uptime && <StatPill label="Uptime" value={uptime} />}
    </div>
  )
}
