import { describe, it, expect, vi, afterEach } from "vitest"
import { fireEvent } from "@testing-library/react"
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

  it("renders one line plus a legend readout for a single series with data", () => {
    const series: ChartSeriesInput[] = [
      {
        key: "Invocations/Sum",
        label: "Invocations",
        unit: "Count",
        statistic: "Sum",
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
    // The legend doubles as the whole-range readout — a Sum series shows its
    // total (3 + 5), which is why even a single series renders one.
    expect(screen.getByText("Invocations")).toBeInTheDocument()
    expect(screen.getByText("8")).toBeInTheDocument()
  })

  it("shows y-axis scale values so magnitude reads without hovering", () => {
    const series: ChartSeriesInput[] = [
      {
        key: "Duration/Average",
        label: "Average",
        unit: "Milliseconds",
        statistic: "Average",
        points: [
          { timestamp: "2026-08-22T10:10:00Z", value: 100 },
          { timestamp: "2026-08-22T10:11:00Z", value: 200 },
        ],
      },
    ]
    render(
      <MetricLineChart
        series={series}
        rangeStartMs={rangeStartMs}
        rangeEndMs={rangeEndMs}
        periodSeconds={60}
      />,
    )
    // Floor label (yMin defaults to 0) and the 8%-headroom ceiling (200 * 1.08).
    expect(screen.getByText("0 ms")).toBeInTheDocument()
    expect(screen.getByText("216 ms")).toBeInTheDocument()
    // Legend readout for an Average series is the mean, not the total.
    expect(screen.getByText("150 ms")).toBeInTheDocument()
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

  it("renders an isolated single-point segment as a visible dot", () => {
    // A one-point segment has no line to draw, and a single-point polyline
    // paints nothing — the point is duplicated into a zero-length segment so
    // the round linecap renders it as a dot (see the component's comment).
    const series: ChartSeriesInput[] = [
      {
        key: "Count/Sum",
        label: "Requests",
        unit: "Count",
        points: [{ timestamp: "2026-08-22T10:10:00Z", value: 3 }],
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
    const polylines = container.querySelectorAll("polyline")
    expect(polylines).toHaveLength(1)
    const coords = (polylines[0].getAttribute("points") ?? "").split(" ")
    expect(coords).toHaveLength(2)
    expect(coords[0]).toBe(coords[1])
    // Wider stroke than a line so the dot reads at a glance.
    expect(Number(polylines[0].getAttribute("stroke-width"))).toBeGreaterThan(1.4)
  })

  describe("drag-to-zoom brush", () => {
    afterEach(() => vi.restoreAllMocks())

    const series: ChartSeriesInput[] = [
      {
        key: "Count/Sum",
        label: "Requests",
        unit: "Count",
        statistic: "Sum",
        points: [
          { timestamp: "2026-08-22T10:10:00Z", value: 3 },
          { timestamp: "2026-08-22T10:40:00Z", value: 5 },
        ],
      },
    ]

    function mockPlotRect() {
      // jsdom reports zero-size rects; the brush math needs a real width.
      vi.spyOn(HTMLDivElement.prototype, "getBoundingClientRect").mockReturnValue({
        left: 0,
        width: 1000,
        top: 0,
        height: 160,
        right: 1000,
        bottom: 160,
        x: 0,
        y: 0,
        toJSON: () => ({}),
      })
    }

    it("reports the dragged time window through onBrushSelect", () => {
      mockPlotRect()
      const onBrushSelect = vi.fn()
      const { container } = render(
        <MetricLineChart
          series={series}
          rangeStartMs={rangeStartMs}
          rangeEndMs={rangeEndMs}
          periodSeconds={60}
          onBrushSelect={onBrushSelect}
        />,
      )
      const plot = container.querySelector("svg")!.parentElement!
      // Drag across 25%..50% of a 10:00→11:00 window = 10:15..10:30.
      fireEvent.mouseDown(plot, { clientX: 250, button: 0 })
      fireEvent.mouseMove(plot, { clientX: 500 })
      fireEvent.mouseUp(plot)
      expect(onBrushSelect).toHaveBeenCalledWith(
        rangeStartMs + 15 * 60 * 1000,
        rangeStartMs + 30 * 60 * 1000,
      )
    })

    it("treats a selection narrower than one bucket as a slipped click", () => {
      mockPlotRect()
      const onBrushSelect = vi.fn()
      const { container } = render(
        <MetricLineChart
          series={series}
          rangeStartMs={rangeStartMs}
          rangeEndMs={rangeEndMs}
          periodSeconds={60}
          onBrushSelect={onBrushSelect}
        />,
      )
      const plot = container.querySelector("svg")!.parentElement!
      // 0.5% of an hour is ~18s — under the 60s period, so no zoom.
      fireEvent.mouseDown(plot, { clientX: 250, button: 0 })
      fireEvent.mouseMove(plot, { clientX: 255 })
      fireEvent.mouseUp(plot)
      expect(onBrushSelect).not.toHaveBeenCalled()
    })

    it("rescales the y-axis and legend readout to the zoomed window", () => {
      // Zoomed to 10:05..10:15: only the value-3 point is visible, so the
      // summary is 3 and the ceiling is 3 * 1.08.
      render(
        <MetricLineChart
          series={series}
          rangeStartMs={Date.parse("2026-08-22T10:05:00Z")}
          rangeEndMs={Date.parse("2026-08-22T10:15:00Z")}
          periodSeconds={60}
        />,
      )
      expect(screen.getByText("3")).toBeInTheDocument()
      expect(screen.getByText("3.2")).toBeInTheDocument()
    })
  })
})
