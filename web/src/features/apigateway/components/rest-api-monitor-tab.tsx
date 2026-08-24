/**
 * REST API Monitor tab (#1307): the same MonitorPanel chrome every other
 * service's Monitor tab/section uses (docs/plans/service-metrics-platform.md
 * phase 3), plus a stage picker. Stage selection is local UI state kept
 * here rather than in MonitorPanel — MonitorPanel stays service-agnostic and
 * has no concept of a "stage" — and it only ever changes which query
 * MonitorPanel is fed, never the panel's own range or its mount identity.
 */
import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { MonitorPanel } from "@/features/monitoring/components/monitor-panel"
import { DEFAULT_CHART_RANGE, type ChartRangeToken } from "@/features/monitoring/types"
import { Select } from "@/components/ui/select"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { apigwKeys, restApiMetricsQueryOptions } from "@/features/apigateway/data"
import { REST_API_MONITOR_CARDS } from "@/features/apigateway/monitor-cards"
import type { ApiStage } from "@/types"

interface Props {
  apiId: string
  /** The Stages tab's own query result — reused rather than fetched again. */
  stages: ApiStage[]
}

export function RestApiMonitorTab({ apiId, stages }: Props) {
  const qc = useQueryClient()
  const [range, setRange] = useState<ChartRangeToken>(DEFAULT_CHART_RANGE)
  // "" means "All stages" (no &stage= param — series aggregate across every stage).
  const [stage, setStage] = useState("")

  const metricsQuery = useQuery(restApiMetricsQueryOptions(apiId, range, stage || undefined))

  return (
    <MonitorPanel
      range={range}
      onRangeChange={setRange}
      isLoading={metricsQuery.isLoading}
      isFetching={metricsQuery.isFetching}
      error={metricsQuery.error}
      data={metricsQuery.data}
      cards={REST_API_MONITOR_CARDS}
      onRefresh={() =>
        void qc.invalidateQueries({
          queryKey: apigwKeys.restMetrics(apiId, range, stage || undefined),
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
