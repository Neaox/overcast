/**
 * Shared types for the web Monitor tab/section
 * (docs/plans/service-metrics-platform.md phase 3), used by every service's
 * Monitor endpoint client (lambda.getMetrics, sqs.getMetrics, ...) and the
 * reusable chart/panel components in this feature directory.
 */

/** One of the plan's five coherent range controls (see metrics.ParseChartRange on the Go side). */
export type ChartRangeToken = "1h" | "6h" | "24h" | "7d" | "30d"

export const CHART_RANGE_OPTIONS: { value: ChartRangeToken; label: string }[] = [
  { value: "1h", label: "Last 1 hour" },
  { value: "6h", label: "Last 6 hours" },
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
]

export const DEFAULT_CHART_RANGE: ChartRangeToken = "1h"

/** Milliseconds spanned by a range token, for computing the chart's x-axis domain. */
export const CHART_RANGE_SPAN_MS: Record<ChartRangeToken, number> = {
  "1h": 60 * 60 * 1000,
  "6h": 6 * 60 * 60 * 1000,
  "24h": 24 * 60 * 60 * 1000,
  "7d": 7 * 24 * 60 * 60 * 1000,
  "30d": 30 * 24 * 60 * 60 * 1000,
}
