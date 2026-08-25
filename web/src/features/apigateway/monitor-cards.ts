/**
 * API Gateway Monitor tab card catalogue (#1307), following the same
 * MonitorPanel chrome every other service's Monitor tab uses
 * (docs/plans/service-metrics-platform.md phase 3).
 *
 * REST (v1) and HTTP (v2) APIs share the exact same catalogue shape and
 * differ in exactly one place: CloudWatch's own client-error metric names —
 * REST reports "4XXError"/"5XXError", HTTP genuinely uses lowercase
 * "4xx"/"5xx". Rather than hand-duplicate three near-identical card configs
 * per API type, the catalogue is built once from those two names.
 */
import type { MonitorCardConfig } from "@/features/monitoring/components/monitor-panel"

export interface ApiGatewayErrorMetricNames {
  /** "4XXError" for REST, "4xx" for HTTP. */
  clientError: string
  /** "5XXError" for REST, "5xx" for HTTP. */
  serverError: string
}

export function buildApiGatewayMonitorCards({
  clientError,
  serverError,
}: ApiGatewayErrorMetricNames): MonitorCardConfig[] {
  return [
    {
      title: "Requests & Errors",
      unit: "Count",
      series: [
        { metric: "Count", statistic: "Sum", label: "Requests" },
        { metric: clientError, statistic: "Sum", label: "4XX" },
        { metric: serverError, statistic: "Sum", label: "5XX" },
      ],
    },
    {
      title: "Latency",
      unit: "Milliseconds",
      series: [
        { metric: "Latency", statistic: "Average", label: "Average" },
        { metric: "Latency", statistic: "Maximum", label: "Maximum" },
      ],
    },
    {
      title: "Integration latency",
      unit: "Milliseconds",
      series: [
        { metric: "IntegrationLatency", statistic: "Average", label: "Average" },
        { metric: "IntegrationLatency", statistic: "Maximum", label: "Maximum" },
      ],
    },
  ]
}

// Built once at module load — never recreated per render (both Monitor tab
// components below import these as plain constants).
export const REST_API_MONITOR_CARDS: MonitorCardConfig[] = buildApiGatewayMonitorCards({
  clientError: "4XXError",
  serverError: "5XXError",
})

export const HTTP_API_MONITOR_CARDS: MonitorCardConfig[] = buildApiGatewayMonitorCards({
  clientError: "4xx",
  serverError: "5xx",
})
