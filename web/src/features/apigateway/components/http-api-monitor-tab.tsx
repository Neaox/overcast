/**
 * HTTP (v2) API Monitor tab (#1307) — see rest-api-monitor-tab.tsx's doc
 * comment for the shared design. The only difference is the card catalogue:
 * HTTP APIs report their client-error metrics as lowercase "4xx"/"5xx",
 * genuinely different names than REST's "4XXError"/"5XXError" (see
 * web/src/features/apigateway/monitor-cards.ts).
 */
import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { MonitorPanel } from "@/features/monitoring/components/monitor-panel"
import { DEFAULT_CHART_RANGE, type ChartRangeToken } from "@/features/monitoring/types"
import { Select } from "@/components/ui/select"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { apigwKeys, httpApiMetricsQueryOptions } from "@/features/apigateway/data"
import { HTTP_API_MONITOR_CARDS } from "@/features/apigateway/monitor-cards"
import type { HttpStage } from "@/types"

interface Props {
  apiId: string
  /** The Stages tab's own query result — reused rather than fetched again. */
  stages: HttpStage[]
}

export function HttpApiMonitorTab({ apiId, stages }: Props) {
  const qc = useQueryClient()
  const [range, setRange] = useState<ChartRangeToken>(DEFAULT_CHART_RANGE)
  // "" means "All stages" (no &stage= param — series aggregate across every stage).
  const [stage, setStage] = useState("")
  const [refreshMs, setRefreshMs] = useState<number | false>(30_000)

  const metricsQuery = useQuery(
    httpApiMetricsQueryOptions(apiId, range, stage || undefined, refreshMs),
  )

  return (
    <MonitorPanel
      range={range}
      onRangeChange={setRange}
      isLoading={metricsQuery.isLoading}
      isFetching={metricsQuery.isFetching}
      error={metricsQuery.error}
      data={metricsQuery.data}
      cards={HTTP_API_MONITOR_CARDS}
      refreshIntervalMs={refreshMs}
      onRefreshIntervalChange={setRefreshMs}
      onRefresh={() =>
        void qc.invalidateQueries({
          queryKey: apigwKeys.httpMetrics(apiId, range, stage || undefined),
        })
      }
      extraControls={
        <label className={cn(fieldLabel, "flex items-center gap-2 text-fg-muted")}>
          Stage
          <Select value={stage} onChange={(e) => setStage(e.target.value)}>
            <option value="">All stages</option>
            {stages.map((s) => (
              <option key={s.stageName} value={s.stageName}>
                {s.stageName}
              </option>
            ))}
          </Select>
        </label>
      }
    />
  )
}
