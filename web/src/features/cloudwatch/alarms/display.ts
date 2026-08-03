/**
 * Presentation helpers for CloudWatch alarm state.
 *
 * Kept out of the component file so they can be unit-tested directly (and so
 * the component file only exports components, which is what Fast Refresh
 * needs).
 */

import type { MetricAlarm } from "@aws-sdk/client-cloudwatch"

/** Maps an alarm state to the badge tone that reads as that state. */
export function alarmVariant(
  state: string | undefined,
): "success" | "warning" | "danger" | "default" {
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

const COMPARISON_SYMBOLS: Record<string, string> = {
  GreaterThanThreshold: ">",
  GreaterThanOrEqualToThreshold: ">=",
  LessThanThreshold: "<",
  LessThanOrEqualToThreshold: "<=",
}

/**
 * One line describing what the evaluator is actually checking — the statistic,
 * the comparison against the threshold, and the "M out of N periods" rule.
 */
export function comparisonSummary(alarm: MetricAlarm): string {
  const operator =
    COMPARISON_SYMBOLS[alarm.ComparisonOperator ?? ""] ?? (alarm.ComparisonOperator || "?")
  const stat = alarm.Statistic ?? alarm.ExtendedStatistic ?? "Average"
  const periods = alarm.EvaluationPeriods ?? 1
  const datapoints = alarm.DatapointsToAlarm ?? periods
  const threshold = alarm.Threshold ?? "?"
  return `${stat}(${alarm.MetricName ?? "?"}) ${operator} ${threshold} for ${datapoints} of ${periods} × ${alarm.Period ?? 60}s`
}

/** Renders an alarm's dimension set, or says it has none. */
export function formatAlarmDimensions(alarm: MetricAlarm): string {
  if (!alarm.Dimensions?.length) return "No dimensions"
  return alarm.Dimensions.map((dimension) => `${dimension.Name}=${dimension.Value}`).join(", ")
}
