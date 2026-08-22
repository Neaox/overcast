/**
 * Reusable Monitor tab/section body (docs/plans/service-metrics-platform.md
 * phase 3 "Web UI plan"): a range selector, one card per catalogue group,
 * loading/error states via the shared QueryListState, and a visible local
 * -retention disclaimer. Lambda's Monitor tab and SQS's Monitor section both
 * render this against their own MonitorCardConfig list — the catalogue
 * differs, the chrome does not (plan: "Build one reusable
 * MetricChart/MetricCard component... rather than duplicating fetch/
 * aggregation/chart code").
 */
import { useEffect, useState } from "react"
import { RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { QueryListState } from "@/components/ui/primitives"
import { Select } from "@/components/ui/select"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import {
  CHART_RANGE_OPTIONS,
  CHART_RANGE_SPAN_MS,
  type ChartRangeToken,
} from "@/features/monitoring/types"
import { MetricLineChart, type ChartSeriesInput } from "./metric-line-chart"
import type { MonitorResponse } from "@/types"

export interface MonitorCardConfig {
  /** Card heading, e.g. "Invocations" or "Duration". */
  title: string
  /** AWS unit shared by every series on this card — cards never mix units on one axis. */
  unit: string
  series: { metric: string; statistic: string; label: string }[]
}

export interface MonitorPanelProps {
  range: ChartRangeToken
  onRangeChange: (range: ChartRangeToken) => void
  isLoading: boolean
  isFetching?: boolean
  error?: unknown
  data?: MonitorResponse
  cards: MonitorCardConfig[]
  onRefresh?: () => void
}

function retentionDisclaimer(range: ChartRangeToken): string {
  switch (range) {
    case "1h":
    case "6h":
    case "24h":
      return "up to 24 hours at 1-minute resolution"
    case "7d":
      return "up to 7 days at 5-minute resolution"
    case "30d":
      return "up to 30 days at 1-hour resolution"
  }
}

export function MonitorPanel({
  range,
  onRangeChange,
  isLoading,
  isFetching,
  error,
  data,
  cards,
  onRefresh,
}: MonitorPanelProps) {
  // "Now" is component state (lazily initialized, refreshed on the same
  // cadence the metrics query itself polls at) rather than a bare Date.now()
  // call in the render body, which React's purity rule flags as an impure
  // render — see web/src/features/map/lambda-invocations-drawer.tsx's
  // identical nowMs pattern.
  const [nowMs, setNowMs] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNowMs(Date.now()), 30_000)
    return () => clearInterval(id)
  }, [])

  const rangeEndMs = nowMs
  const rangeStartMs = rangeEndMs - CHART_RANGE_SPAN_MS[range]
  const periodSeconds = data?.periodSeconds || 60

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className={cn(fieldLabel, "flex items-center gap-2 text-fg-muted")}>
          Time range
          <Select value={range} onChange={(e) => onRangeChange(e.target.value as ChartRangeToken)}>
            {CHART_RANGE_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </Select>
        </label>
        {onRefresh && (
          <Button size="sm" variant="ghost" onClick={onRefresh} disabled={isFetching}>
            <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
            Refresh
          </Button>
        )}
      </div>

      <QueryListState
        isLoading={isLoading}
        isEmpty={!!error}
        error={error}
        errorTitle="Unable to load metrics"
      />

      {!isLoading &&
        !error &&
        (data && !data.enabled ? (
          <div className="rounded-lg border border-dashed border-border bg-bg-muted/40 p-6 text-center text-sm text-fg-muted">
            Service-metric collection is disabled (
            <code className="font-mono text-xs">OVERCAST_SERVICE_METRICS</code>). Enable it to see
            automatic Monitor charts here.
          </div>
        ) : (
          <>
            <div className="grid gap-4 md:grid-cols-2">
              {cards.map((card) => {
                const chartSeries: ChartSeriesInput[] = card.series.map((sk) => {
                  const found = data?.series.find(
                    (s) => s.metric === sk.metric && s.statistic === sk.statistic,
                  )
                  return {
                    key: `${sk.metric}/${sk.statistic}`,
                    label: sk.label,
                    unit: card.unit,
                    points: found?.points ?? [],
                  }
                })
                return (
                  <div
                    key={card.title}
                    className="flex flex-col gap-2 rounded-xl border border-border bg-bg-elevated p-4"
                  >
                    <h3 className="font-mono text-sm font-semibold text-fg">{card.title}</h3>
                    <MetricLineChart
                      series={chartSeries}
                      rangeStartMs={rangeStartMs}
                      rangeEndMs={rangeEndMs}
                      periodSeconds={periodSeconds}
                    />
                  </div>
                )
              })}
            </div>
            <p className="text-xs text-fg-muted">
              Local emulator data only ({retentionDisclaimer(range)}) — not the real AWS CloudWatch
              console.
            </p>
          </>
        ))}
    </div>
  )
}
