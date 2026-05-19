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

export function cloudwatchAlarmsQueryOptions() {
  return queryOptions({
    queryKey: cloudwatchKeys.alarms(),
    queryFn: () => cloudwatch.describeAlarms(),
  })
}

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
