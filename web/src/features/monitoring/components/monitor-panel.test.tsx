import { describe, it, expect, vi } from "vitest"
import { render, screen } from "@/test/render"
import { MonitorPanel, type MonitorCardConfig } from "./monitor-panel"
import type { MonitorResponse } from "@/types"

const CARDS: MonitorCardConfig[] = [
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
    series: [{ metric: "Duration", statistic: "Average", label: "Average" }],
  },
]

function baseProps() {
  return {
    range: "1h" as const,
    onRangeChange: vi.fn(),
    isLoading: false,
    cards: CARDS,
  }
}

describe("MonitorPanel", () => {
  it("shows a loading skeleton while the query is in flight", () => {
    render(<MonitorPanel {...baseProps()} isLoading />)
    // The shared QueryListState loading skeleton renders row/card placeholders
    // rather than any card heading yet.
    expect(screen.queryByText("Invocations & Errors")).not.toBeInTheDocument()
  })

  it("shows an error state and never renders cards when the query failed", () => {
    render(<MonitorPanel {...baseProps()} error={new Error("boom")} />)
    expect(screen.getByText("Unable to load metrics")).toBeInTheDocument()
    expect(screen.getByText("boom")).toBeInTheDocument()
    expect(screen.queryByText("Invocations & Errors")).not.toBeInTheDocument()
  })

  it("shows the disabled-collection state when enabled is false, not an empty chart", () => {
    const data: MonitorResponse = { enabled: false, range: "1h", series: [] }
    render(<MonitorPanel {...baseProps()} data={data} />)
    expect(screen.getByText(/collection is disabled/i)).toBeInTheDocument()
    expect(screen.queryByText("Invocations & Errors")).not.toBeInTheDocument()
  })

  it("renders every configured card with 'No metric data in this range' when series are empty", () => {
    const data: MonitorResponse = {
      enabled: true,
      range: "1h",
      periodSeconds: 60,
      series: [
        { metric: "Invocations", statistic: "Sum", unit: "Count", points: [] },
        { metric: "Errors", statistic: "Sum", unit: "Count", points: [] },
        { metric: "Duration", statistic: "Average", unit: "Milliseconds", points: [] },
      ],
    }
    render(<MonitorPanel {...baseProps()} data={data} />)
    expect(screen.getByText("Invocations & Errors")).toBeInTheDocument()
    expect(screen.getByText("Duration")).toBeInTheDocument()
    expect(screen.getAllByText("No metric data in this range.")).toHaveLength(2)
  })

  it("renders a card's chart and legend when its series have points", () => {
    const now = new Date().toISOString()
    const data: MonitorResponse = {
      enabled: true,
      range: "1h",
      periodSeconds: 60,
      series: [
        {
          metric: "Invocations",
          statistic: "Sum",
          unit: "Count",
          points: [{ timestamp: now, value: 3 }],
        },
        { metric: "Errors", statistic: "Sum", unit: "Count", points: [] },
        { metric: "Duration", statistic: "Average", unit: "Milliseconds", points: [] },
      ],
    }
    render(<MonitorPanel {...baseProps()} data={data} />)
    // The Invocations & Errors card now has real data (only 1 of its 2 series
    // is empty), so it renders the chart+legend, not the empty-state text.
    expect(screen.getByText("Invocations")).toBeInTheDocument()
    expect(screen.getByText("Errors")).toBeInTheDocument()
    // Duration's only series is still empty.
    expect(screen.getByText("No metric data in this range.")).toBeInTheDocument()
  })

  it("shows the retention disclaimer text for the selected range", () => {
    const data: MonitorResponse = { enabled: true, range: "30d", periodSeconds: 3600, series: [] }
    render(<MonitorPanel {...baseProps()} range="30d" data={data} />)
    expect(screen.getByText(/up to 30 days at 1-hour resolution/)).toBeInTheDocument()
  })
})
