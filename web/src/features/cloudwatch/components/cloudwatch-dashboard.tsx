import { useEffect, useMemo, useState } from "react"
import { Link } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import type { Datapoint, Metric, Statistic } from "@aws-sdk/client-cloudwatch"
import { Activity, AlertTriangle, RefreshCw, ScrollText } from "lucide-react"
import {
  alarmsForMetric,
  cloudwatchAlarmsQueryOptions,
  cloudwatchMetricsQueryOptions,
  cloudwatchMetricStatisticsQueryOptions,
  metricIdentity,
} from "@/features/cloudwatch/metrics/data"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { QueryListState, PageHeader } from "@/components/ui/primitives"
import { Select } from "@/components/ui/select"
import { cn } from "@/lib/utils"

const STAT_OPTIONS: Statistic[] = ["Average", "Sum", "SampleCount", "Minimum", "Maximum"]

const RANGE_OPTIONS = [
  { hours: 1, label: "Last 1 hour", period: 60 },
  { hours: 6, label: "Last 6 hours", period: 300 },
  { hours: 24, label: "Last 24 hours", period: 900 },
] as const

function formatTimestamp(value?: Date): string {
  if (!value) return "—"
  return value.toLocaleString()
}

function formatDimensionList(metric: Metric): string {
  if (!metric.Dimensions?.length) return "No dimensions"
  return metric.Dimensions.map((dimension) => `${dimension.Name}=${dimension.Value}`).join(", ")
}

function readDatapointValue(datapoint: Datapoint, stat: Statistic): number | undefined {
  switch (stat) {
    case "Sum":
      return datapoint.Sum
    case "SampleCount":
      return datapoint.SampleCount
    case "Minimum":
      return datapoint.Minimum
    case "Maximum":
      return datapoint.Maximum
    case "Average":
    default:
      return datapoint.Average
  }
}

function formatDatapointValue(datapoint: Datapoint, stat: Statistic): string {
  const value = readDatapointValue(datapoint, stat)
  if (value == null) return "—"
  const unit = datapoint.Unit ? ` ${datapoint.Unit}` : ""
  return `${value.toFixed(2)}${unit}`
}

function alarmVariant(state: string | undefined): "success" | "warning" | "danger" | "default" {
  switch (state) {
    case "OK":
      return "success"
    case "ALARM":
      return "danger"
    case "INSUFFICIENT_DATA":
      return "warning"
    default:
      return "default"
  }
}

function MetricChart({ datapoints, stat }: { datapoints: Datapoint[]; stat: Statistic }) {
  const points = useMemo(() => {
    const values = datapoints
      .map((datapoint) => readDatapointValue(datapoint, stat))
      .filter((value): value is number => value != null)
    if (values.length === 0) return ""

    const min = Math.min(...values)
    const max = Math.max(...values)
    const span = max - min || 1

    return datapoints
      .map((datapoint, index) => {
        const rawValue = readDatapointValue(datapoint, stat) ?? min
        const x = datapoints.length === 1 ? 0 : (index / (datapoints.length - 1)) * 100
        const y = 100 - ((rawValue - min) / span) * 100
        return `${x},${y}`
      })
      .join(" ")
  }, [datapoints, stat])

  if (!points) {
    return (
      <div className="flex h-48 items-center justify-center rounded-lg border border-dashed border-border bg-bg-muted/40 text-sm text-fg-muted">
        No datapoints in the selected time range.
      </div>
    )
  }

  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-4">
      <svg viewBox="0 0 100 100" preserveAspectRatio="none" className="h-48 w-full">
        <polyline
          fill="none"
          points={points}
          stroke="currentColor"
          strokeWidth="2"
          className="text-accent"
        />
      </svg>
    </div>
  )
}

export function CloudwatchDashboard() {
  const [selectedMetricId, setSelectedMetricId] = useState<string>()
  const [selectedStat, setSelectedStat] = useState<Statistic>("Average")
  const [rangeHours, setRangeHours] = useState<number>(1)
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const metricsQuery = useQuery(cloudwatchMetricsQueryOptions())
  const alarmsQuery = useQuery(cloudwatchAlarmsQueryOptions())

  const metrics = metricsQuery.data ?? []

  useEffect(() => {
    if (!metrics.length) {
      setSelectedMetricId(undefined)
      return
    }

    const hasSelectedMetric = metrics.some((metric) => metricIdentity(metric) === selectedMetricId)
    if (!hasSelectedMetric) {
      setSelectedMetricId(metricIdentity(metrics[0]))
    }
  }, [metrics, selectedMetricId])

  const selectedMetric = useMemo(
    () => metrics.find((metric) => metricIdentity(metric) === selectedMetricId) ?? metrics[0],
    [metrics, selectedMetricId],
  )

  const rangeConfig =
    RANGE_OPTIONS.find((option) => option.hours === rangeHours) ?? RANGE_OPTIONS[0]

  const statisticsQuery = useQuery(
    cloudwatchMetricStatisticsQueryOptions({
      namespace: selectedMetric?.Namespace ?? "",
      metricName: selectedMetric?.MetricName ?? "",
      dimensions: selectedMetric?.Dimensions ?? [],
      stat: selectedStat,
      period: rangeConfig.period,
      rangeHours: rangeConfig.hours,
    }),
  )

  const selectedAlarms = useMemo(
    () => alarmsForMetric(selectedMetric ?? {}, alarmsQuery.data ?? []),
    [alarmsQuery.data, selectedMetric],
  )

  const refetchAll = () => {
    void metricsQuery.refetch()
    void alarmsQuery.refetch()
    void statisticsQuery.refetch()
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="CloudWatch Metrics"
        description="Browse published metrics, inspect aggregated datapoints, and review matching alarms."
        actions={
          <>
            <ServiceDocsButton
              service="cloudwatch"
              label="CloudWatch"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button
              size="sm"
              variant="ghost"
              onClick={refetchAll}
              disabled={metricsQuery.isFetching}
            >
              <RefreshCw
                className={cn("mr-1.5 h-3.5 w-3.5", metricsQuery.isFetching && "animate-spin")}
              />
              Refresh
            </Button>
            <Button size="sm" variant="ghost" asChild>
              <Link to="/cloudwatch/logs">
                <ScrollText className="mr-1.5 h-3.5 w-3.5" />
                Open Logs
              </Link>
            </Button>
          </>
        }
      />

      <div className="grid gap-4 lg:grid-cols-[320px_minmax(0,1fr)]">
        <section className="rounded-xl border border-border bg-bg-elevated p-4">
          <div className="mb-3 flex items-center justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold text-fg">Metrics</h2>
              <p className="text-sm text-fg-muted">
                {metrics.length} metric{metrics.length === 1 ? "" : "s"} discovered
              </p>
            </div>
          </div>

          <QueryListState
            isLoading={metricsQuery.isLoading}
            isEmpty={metrics.length === 0}
            error={metricsQuery.error}
            emptyIcon={<Activity className="h-10 w-10" />}
            emptyTitle="No metrics published yet"
            emptyDescription="Publish CloudWatch datapoints and they will appear here."
            errorTitle="Unable to load metrics"
          />

          {metrics.length > 0 && (
            <div className="space-y-2">
              {metrics.map((metric) => {
                const identity = metricIdentity(metric)
                const isSelected = identity === metricIdentity(selectedMetric ?? metric)
                return (
                  <button
                    key={identity}
                    type="button"
                    onClick={() => setSelectedMetricId(identity)}
                    className={cn(
                      "w-full rounded-lg border px-3 py-2 text-left transition-colors",
                      isSelected
                        ? "border-accent bg-accent-muted/30"
                        : "border-border bg-bg hover:border-accent/50 hover:bg-bg-muted/40",
                    )}
                  >
                    <div className="text-sm font-medium text-fg">{metric.MetricName}</div>
                    <div className="text-xs text-fg-muted">{metric.Namespace}</div>
                    <div className="mt-1 text-xs text-fg-muted">{formatDimensionList(metric)}</div>
                  </button>
                )
              })}
            </div>
          )}
        </section>

        <section className="space-y-4 rounded-xl border border-border bg-bg-elevated p-4">
          {selectedMetric ? (
            <>
              <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                <div className="space-y-1">
                  <div className="flex items-center gap-2">
                    <h2 className="text-lg font-semibold text-fg">{selectedMetric.MetricName}</h2>
                    <Badge variant="outline">{selectedMetric.Namespace}</Badge>
                  </div>
                  <p className="text-sm text-fg-muted">{formatDimensionList(selectedMetric)}</p>
                </div>
                <div className="grid gap-2 sm:grid-cols-2">
                  <label className="space-y-1 text-xs font-medium tracking-wide text-fg-muted uppercase">
                    Statistic
                    <Select
                      value={selectedStat}
                      onChange={(event) => setSelectedStat(event.target.value as Statistic)}
                    >
                      {STAT_OPTIONS.map((stat) => (
                        <option key={stat} value={stat}>
                          {stat}
                        </option>
                      ))}
                    </Select>
                  </label>
                  <label className="space-y-1 text-xs font-medium tracking-wide text-fg-muted uppercase">
                    Time Range
                    <Select
                      value={String(rangeHours)}
                      onChange={(event) => setRangeHours(Number(event.target.value))}
                    >
                      {RANGE_OPTIONS.map((option) => (
                        <option key={option.hours} value={option.hours}>
                          {option.label}
                        </option>
                      ))}
                    </Select>
                  </label>
                </div>
              </div>

              <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
                <div className="space-y-4">
                  <MetricChart
                    datapoints={statisticsQuery.data?.datapoints ?? []}
                    stat={selectedStat}
                  />

                  <div className="rounded-lg border border-border bg-bg p-4">
                    <div className="mb-3 flex items-center justify-between gap-2">
                      <h3 className="text-sm font-semibold text-fg">Recent datapoints</h3>
                      <span className="text-xs text-fg-muted">
                        {statisticsQuery.data?.datapoints.length ?? 0} returned
                      </span>
                    </div>
                    <QueryListState
                      isLoading={statisticsQuery.isLoading}
                      isEmpty={(statisticsQuery.data?.datapoints.length ?? 0) === 0}
                      error={statisticsQuery.error}
                      emptyTitle="No datapoints in range"
                      emptyDescription="Try a wider time range or publish more metric data."
                      errorTitle="Unable to load metric statistics"
                    />

                    {(statisticsQuery.data?.datapoints.length ?? 0) > 0 && (
                      <div className="overflow-x-auto">
                        <table className="w-full text-sm">
                          <thead>
                            <tr className="border-b border-border text-left text-xs tracking-wide text-fg-muted uppercase">
                              <th className="py-2 pr-3 font-medium">Timestamp</th>
                              <th className="py-2 pr-3 font-medium">{selectedStat}</th>
                              <th className="py-2 font-medium">Unit</th>
                            </tr>
                          </thead>
                          <tbody>
                            {statisticsQuery.data?.datapoints.map((datapoint, index) => (
                              <tr
                                key={`${datapoint.Timestamp?.toISOString() ?? index}`}
                                className="border-b border-border/60 last:border-0"
                              >
                                <td className="py-2 pr-3 text-fg-muted">
                                  {formatTimestamp(datapoint.Timestamp)}
                                </td>
                                <td className="py-2 pr-3 font-medium text-fg">
                                  {formatDatapointValue(datapoint, selectedStat)}
                                </td>
                                <td className="py-2 text-fg-muted">{datapoint.Unit ?? "—"}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    )}
                  </div>
                </div>

                <div className="rounded-lg border border-border bg-bg p-4">
                  <div className="mb-3 flex items-center justify-between gap-2">
                    <h3 className="text-sm font-semibold text-fg">Alarms</h3>
                    <span className="text-xs text-fg-muted">{selectedAlarms.length} matching</span>
                  </div>

                  <QueryListState
                    isLoading={alarmsQuery.isLoading}
                    isEmpty={selectedAlarms.length === 0}
                    error={alarmsQuery.error}
                    emptyIcon={<AlertTriangle className="h-10 w-10" />}
                    emptyTitle="No alarms target this metric"
                    emptyDescription="Alarm state will appear here when a CloudWatch alarm is configured for the selected metric."
                    errorTitle="Unable to load alarms"
                  />

                  {selectedAlarms.length > 0 && (
                    <div className="space-y-3">
                      {selectedAlarms.map((alarm) => (
                        <div
                          key={alarm.AlarmArn ?? alarm.AlarmName}
                          className="rounded-lg border border-border bg-bg-elevated p-3"
                        >
                          <div className="mb-2 flex items-start justify-between gap-3">
                            <div>
                              <div className="text-sm font-medium text-fg">{alarm.AlarmName}</div>
                              {alarm.AlarmDescription && (
                                <div className="mt-1 text-xs text-fg-muted">
                                  {alarm.AlarmDescription}
                                </div>
                              )}
                            </div>
                            <Badge variant={alarmVariant(alarm.StateValue)}>
                              {alarm.StateValue ?? "UNKNOWN"}
                            </Badge>
                          </div>

                          <dl className="grid gap-2 text-sm sm:grid-cols-2">
                            <div>
                              <dt className="text-xs tracking-wide text-fg-muted uppercase">
                                Threshold
                              </dt>
                              <dd className="text-fg">{alarm.Threshold ?? "—"}</dd>
                            </div>
                            <div>
                              <dt className="text-xs tracking-wide text-fg-muted uppercase">
                                Comparison
                              </dt>
                              <dd className="text-fg">{alarm.ComparisonOperator ?? "—"}</dd>
                            </div>
                            <div>
                              <dt className="text-xs tracking-wide text-fg-muted uppercase">
                                Evaluation Periods
                              </dt>
                              <dd className="text-fg">{alarm.EvaluationPeriods ?? "—"}</dd>
                            </div>
                            <div>
                              <dt className="text-xs tracking-wide text-fg-muted uppercase">
                                Last Transition
                              </dt>
                              <dd className="text-fg">
                                {formatTimestamp(alarm.StateUpdatedTimestamp)}
                              </dd>
                            </div>
                          </dl>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            </>
          ) : (
            <div className="flex min-h-64 items-center justify-center text-sm text-fg-muted">
              Select a metric to inspect datapoints and alarms.
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
