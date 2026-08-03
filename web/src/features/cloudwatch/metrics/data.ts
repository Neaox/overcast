import { queryOptions } from "@tanstack/react-query"
import type { Dimension, Metric, MetricAlarm, Statistic } from "@aws-sdk/client-cloudwatch"
import { cloudwatch } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

export const cloudwatchKeys = {
  all: () => [...endpointStore.getKeys(), "cloudwatch"] as const,
  metrics: (namespace?: string) =>
    [...cloudwatchKeys.all(), "metrics", namespace ?? "all"] as const,
  statistics: (params: {
    namespace: string
    metricName: string
    dimensions: Dimension[]
    stat: Statistic
    period: number
    rangeHours: number
  }) =>
    [
      ...cloudwatchKeys.all(),
      "statistics",
      params.namespace,
      params.metricName,
      params.dimensions,
      params.stat,
      params.period,
      params.rangeHours,
    ] as const,
  alarms: () => [...cloudwatchKeys.all(), "alarms"] as const,
  alarmHistory: (alarmName: string) =>
    [...cloudwatchKeys.all(), "alarm-history", alarmName] as const,
}

export function cloudwatchMetricsQueryOptions(namespace?: string) {
  return queryOptions({
    queryKey: cloudwatchKeys.metrics(namespace),
    queryFn: () => cloudwatch.listMetrics(namespace),
  })
}

export function cloudwatchMetricStatisticsQueryOptions(params: {
  namespace: string
  metricName: string
  dimensions: Dimension[]
  stat: Statistic
  period: number
  rangeHours: number
}) {
  return queryOptions({
    queryKey: cloudwatchKeys.statistics(params),
    queryFn: () => {
      const endTime = new Date()
      const startTime = new Date(endTime.getTime() - params.rangeHours * 60 * 60 * 1000)
      return cloudwatch.getMetricStatistics({
        namespace: params.namespace,
        metricName: params.metricName,
        dimensions: params.dimensions,
        stat: params.stat,
        period: params.period,
        startTime,
        endTime,
      })
    },
    enabled: Boolean(params.namespace && params.metricName),
  })
}

/**
 * Alarm state is evaluated by a background loop in the emulator, so this
 * refetches on an interval rather than waiting for a user action — the point
 * of the alarms view is watching state change on its own.
 */
export function cloudwatchAlarmsQueryOptions() {
  return queryOptions({
    queryKey: cloudwatchKeys.alarms(),
    queryFn: () => cloudwatch.describeAlarms(),
    refetchInterval: ALARM_POLL_INTERVAL_MS,
  })
}

export function cloudwatchAlarmHistoryQueryOptions(alarmName: string) {
  return queryOptions({
    queryKey: cloudwatchKeys.alarmHistory(alarmName),
    queryFn: () => cloudwatch.describeAlarmHistory({ alarmName }),
    enabled: Boolean(alarmName),
    refetchInterval: ALARM_POLL_INTERVAL_MS,
  })
}

/**
 * How often the alarms view re-reads state. The evaluator wakes every second
 * but only decides an alarm when its period closes, so polling faster than
 * this buys nothing and costs a request per tick.
 */
const ALARM_POLL_INTERVAL_MS = 10_000

export function metricIdentity(metric: Pick<Metric, "Namespace" | "MetricName" | "Dimensions">) {
  const dimensionPart = (metric.Dimensions ?? [])
    .map((dimension) => `${dimension.Name ?? ""}=${dimension.Value ?? ""}`)
    .join("|")
  return `${metric.Namespace ?? ""}::${metric.MetricName ?? ""}::${dimensionPart}`
}

export function alarmsForMetric(
  metric: Pick<Metric, "Namespace" | "MetricName">,
  alarms: MetricAlarm[],
) {
  return alarms.filter(
    (alarm) => alarm.Namespace === metric.Namespace && alarm.MetricName === metric.MetricName,
  )
}
