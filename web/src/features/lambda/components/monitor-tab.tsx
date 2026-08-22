import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { LogViewer } from "@/components/logs/log-viewer"
import { Spinner } from "@/components/ui/primitives"
import { logsFilterQueryOptions } from "@/features/cloudwatch/logs/data"
import { lambdaMetricsQueryOptions, lambdaKeys } from "@/features/lambda/data"
import {
  MonitorPanel,
  type MonitorCardConfig,
} from "@/features/monitoring/components/monitor-panel"
import { DEFAULT_CHART_RANGE, type ChartRangeToken } from "@/features/monitoring/types"
import type { FilteredLogEvent, LambdaFunction } from "@/types"

// The AWS/Lambda P0 pilot catalogue, grouped into cards the way AWS's own
// Lambda Monitoring tab groups them (docs/plans/service-metrics-platform.md
// "Pilot catalogue and delivery order").
const LAMBDA_MONITOR_CARDS: MonitorCardConfig[] = [
  {
    title: "Invocations & Errors",
    unit: "Count",
    series: [
      { metric: "Invocations", statistic: "Sum", label: "Invocations" },
      { metric: "Errors", statistic: "Sum", label: "Errors" },
    ],
  },
  {
    title: "Duration",
    unit: "Milliseconds",
    series: [
      { metric: "Duration", statistic: "Average", label: "Average" },
      { metric: "Duration", statistic: "Maximum", label: "Maximum" },
    ],
  },
  {
    title: "Throttles",
    unit: "Count",
    series: [{ metric: "Throttles", statistic: "Sum", label: "Throttles" }],
  },
  {
    title: "Concurrent Executions",
    unit: "None",
    series: [{ metric: "ConcurrentExecutions", statistic: "Maximum", label: "Maximum" }],
  },
]

export function MonitorTab({ fn }: { fn: LambdaFunction }) {
  const name = fn.FunctionName ?? ""
  const logGroup = fn.LoggingConfig?.LogGroup ?? ""
  const qc = useQueryClient()
  const [range, setRange] = useState<ChartRangeToken>(DEFAULT_CHART_RANGE)

  const metricsQuery = useQuery(lambdaMetricsQueryOptions(name, range))

  // The footer promises a 5-second refresh, so the query keeps it. (It said so
  // for a long time without any interval actually configured.)
  const { data, isLoading, isError } = useQuery({
    ...logsFilterQueryOptions(logGroup),
    refetchInterval: 5_000,
  })

  const events: FilteredLogEvent[] = data?.events.slice(-100) ?? []

  return (
    <div className="flex flex-col gap-6">
      {/* ── Metrics ──────────────────────────────────────────────────── */}
      <MonitorPanel
        range={range}
        onRangeChange={setRange}
        isLoading={metricsQuery.isLoading}
        isFetching={metricsQuery.isFetching}
        error={metricsQuery.error}
        data={metricsQuery.data}
        cards={LAMBDA_MONITOR_CARDS}
        onRefresh={() => void qc.invalidateQueries({ queryKey: lambdaKeys.metrics(name, range) })}
      />

      {/* ── Logs ─────────────────────────────────────────────────────── */}
      <div className="flex flex-col gap-3 border-t border-border pt-4">
        <div className="flex items-center gap-2 text-sm">
          <span className="text-fg-muted">Log group:</span>
          {logGroup ? (
            <Link
              to="/cloudwatch/logs/group"
              search={{ groupName: logGroup }}
              className="font-mono text-xs text-accent hover:underline"
            >
              {logGroup}
            </Link>
          ) : (
            <span className="font-mono text-xs text-fg-muted">—</span>
          )}
        </div>

        {!logGroup ? (
          <p className="text-sm text-fg-muted">No log group configured for this function.</p>
        ) : isLoading ? (
          <div className="flex items-center justify-center py-16">
            <Spinner className="h-5 w-5" />
          </div>
        ) : isError || events.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-md border border-border bg-bg-elevated py-16 text-center">
            <p className="text-sm text-fg-muted">No log events yet.</p>
            <p className="text-xs text-fg-muted">Invoke the function to generate logs.</p>
          </div>
        ) : (
          <div className="max-h-[60vh] rounded-md border border-border bg-bg-elevated p-3">
            <LogViewer
              events={events}
              loading={false}
              emptyMessage="No log events yet."
              defaultMode="plain"
            />
          </div>
        )}

        {events.length > 0 && (
          <p className="text-right text-xs text-fg-muted">
            Showing last {events.length} event{events.length !== 1 ? "s" : ""} · auto-refreshes
            every 5 s
          </p>
        )}
      </div>
    </div>
  )
}
