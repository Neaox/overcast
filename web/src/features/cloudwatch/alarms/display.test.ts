import type { MetricAlarm } from "@aws-sdk/client-cloudwatch"
import { alarmVariant, comparisonSummary, formatAlarmDimensions } from "./display"

describe("alarmVariant", () => {
  it("gives each alarm state a tone that reads as that state", () => {
    expect(alarmVariant("OK")).toBe("success")
    expect(alarmVariant("ALARM")).toBe("danger")
    expect(alarmVariant("INSUFFICIENT_DATA")).toBe("warning")
    expect(alarmVariant(undefined)).toBe("default")
  })
})

describe("comparisonSummary", () => {
  it("describes what the evaluator is checking, including the M-of-N rule", () => {
    // Given: an alarm that needs 2 of the last 3 periods to breach
    const alarm: MetricAlarm = {
      MetricName: "Latency",
      Statistic: "Average",
      ComparisonOperator: "GreaterThanThreshold",
      Threshold: 100,
      EvaluationPeriods: 3,
      DatapointsToAlarm: 2,
      Period: 60,
    }

    // When/Then: the summary names the statistic, comparison and rule
    expect(comparisonSummary(alarm)).toBe("Average(Latency) > 100 for 2 of 3 × 60s")
  })

  it("defaults DatapointsToAlarm to EvaluationPeriods, as the evaluator does", () => {
    // Given: an alarm with no DatapointsToAlarm
    const alarm: MetricAlarm = {
      MetricName: "Errors",
      Statistic: "Sum",
      ComparisonOperator: "GreaterThanOrEqualToThreshold",
      Threshold: 1,
      EvaluationPeriods: 2,
      Period: 300,
    }

    // When/Then: it reads as "2 of 2"
    expect(comparisonSummary(alarm)).toBe("Sum(Errors) >= 1 for 2 of 2 × 300s")
  })
})

describe("formatAlarmDimensions", () => {
  it("joins the dimension set", () => {
    expect(
      formatAlarmDimensions({ Dimensions: [{ Name: "Service", Value: "api" }] }),
    ).toBe("Service=api")
  })

  it("says so when an alarm has none", () => {
    expect(formatAlarmDimensions({})).toBe("No dimensions")
  })
})
