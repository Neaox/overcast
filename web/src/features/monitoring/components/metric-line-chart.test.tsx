import { describe, it, expect } from "vitest"
import { render, screen } from "@/test/render"
import { MetricLineChart, type ChartSeriesInput } from "./metric-line-chart"

const rangeStartMs = Date.parse("2026-08-22T10:00:00Z")
const rangeEndMs = Date.parse("2026-08-22T11:00:00Z")

describe("MetricLineChart", () => {
  it("shows 'No metric data in this range' when every series is empty", () => {
    const series: ChartSeriesInput[] = [
      { key: "Invocations/Sum", label: "Invocations", unit: "Count", points: [] },
    ]
    render(
      <MetricLineChart
        series={series}
        rangeStartMs={rangeStartMs}
        rangeEndMs={rangeEndMs}
        periodSeconds={60}
      />,
    )
    expect(screen.getByText("No metric data in this range.")).toBeInTheDocument()
  })

  it("renders one line and no legend for a single series with data", () => {
    const series: ChartSeriesInput[] = [
      {
        key: "Invocations/Sum",
        label: "Invocations",
        unit: "Count",
        points: [
          { timestamp: "2026-08-22T10:10:00Z", value: 3 },
          { timestamp: "2026-08-22T10:11:00Z", value: 5 },
        ],
      },
    ]
    const { container } = render(
      <MetricLineChart
        series={series}
        rangeStartMs={rangeStartMs}
        rangeEndMs={rangeEndMs}
        periodSeconds={60}
      />,
    )
    expect(screen.queryByText("No metric data in this range.")).not.toBeInTheDocument()
    expect(container.querySelectorAll("polyline")).toHaveLength(1)
    // A single series never gets a legend box — the card title already names it.
    expect(screen.queryByText("Invocations")).not.toBeInTheDocument()
  })

  it("renders a legend entry per series once there are 2 or more", () => {
    const series: ChartSeriesInput[] = [
      {
        key: "Invocations/Sum",
        label: "Invocations",
        unit: "Count",
        points: [{ timestamp: "2026-08-22T10:10:00Z", value: 3 }],
      },
      { key: "Errors/Sum", label: "Errors", unit: "Count", points: [] },
    ]
    render(
      <MetricLineChart
        series={series}
        rangeStartMs={rangeStartMs}
        rangeEndMs={rangeEndMs}
        periodSeconds={60}
      />,
    )
    expect(screen.getByText("Invocations")).toBeInTheDocument()
    expect(screen.getByText("Errors")).toBeInTheDocument()
  })

  it("splits a series into separate polyline segments across a data gap", () => {
    // Two points 40 minutes apart with a 60s period: the gap threshold is
    // 1.5x the period (90s), so this must never be joined by one line.
    const series: ChartSeriesInput[] = [
      {
        key: "Invocations/Sum",
        label: "Invocations",
        unit: "Count",
        points: [
          { timestamp: "2026-08-22T10:05:00Z", value: 1 },
          { timestamp: "2026-08-22T10:45:00Z", value: 2 },
        ],
      },
    ]
    const { container } = render(
      <MetricLineChart
        series={series}
        rangeStartMs={rangeStartMs}
        rangeEndMs={rangeEndMs}
        periodSeconds={60}
      />,
    )
    expect(container.querySelectorAll("polyline")).toHaveLength(2)
  })
})
