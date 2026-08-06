import { useMemo } from "react"
import type { TraceHop } from "@/types"

interface WaterfallProps {
  hops: TraceHop[]
  totalDuration: number
  onSelectHop?: (hopId: string) => void
  selectedHopId?: string | null
}

const barHeight = 20
const barGap = 6
const labelWidth = 120
const rightPadding = 40

/**
 * SVG waterfall timeline showing each hop as a horizontal bar scaled to the
 * request's total duration. Green for 2xx, amber for 4xx, red for 5xx/error.
 * Click a bar to select the hop.
 */
export function Waterfall({ hops, totalDuration, onSelectHop, selectedHopId }: WaterfallProps) {
  const width = 700
  const chartWidth = width - labelWidth - rightPadding
  const totalHeight = hops.length * (barHeight + barGap)

  const bars = useMemo(() => {
    if (totalDuration <= 0) return []
    return hops.map((hop, i) => {
      const left = 0
      const widthPx = Math.max(2, (hop.duration / totalDuration) * chartWidth)
      const y = i * (barHeight + barGap)

      let color = "#22c55e"
      if (hop.responseStatus >= 500 || hop.error) color = "#ef4444"
      else if (hop.responseStatus >= 400) color = "#f59e0b"

      return { ...hop, left, widthPx, y, color }
    })
  }, [hops, totalDuration, chartWidth])

  if (totalDuration <= 0 || hops.length === 0) {
    return <div className="text-center text-fg-muted py-8 text-sm">No timing data available.</div>
  }

  return (
    <svg
      width={width}
      height={Math.max(totalHeight, 40)}
      className="text-xs"
    >
      {/* Time ruler */}
      {[0, 0.25, 0.5, 0.75, 1].map((pct) => {
        const x = labelWidth + pct * chartWidth
        return (
          <g key={pct}>
            <line x1={x} y1={0} x2={x} y2={totalHeight} stroke="var(--color-border)" strokeWidth={0.5} />
            <text x={x} y={totalHeight + 14} textAnchor="middle" fill="var(--color-fg-muted)" fontSize={10}>
              {msLabel(totalDuration * pct)}
            </text>
          </g>
        )
      })}

      {bars.map((bar) => (
        <g
          key={bar.id}
          onClick={() => onSelectHop?.(bar.id)}
          className="cursor-pointer"
          opacity={selectedHopId && selectedHopId !== bar.id ? 0.4 : 1}
        >
          {/* Background bar (full duration context) */}
          <rect
            x={labelWidth}
            y={bar.y}
            width={chartWidth}
            height={barHeight}
            rx={3}
            fill="var(--color-bg-elevated)"
            stroke="var(--color-border)"
            strokeWidth={0.5}
          />
          {/* Foreground bar (hop duration) */}
          <rect
            x={labelWidth + bar.left}
            y={bar.y}
            width={bar.widthPx}
            height={barHeight}
            rx={3}
            fill={bar.color}
            opacity={0.7}
          />
          {/* Label */}
          <text
            x={labelWidth - 4}
            y={bar.y + barHeight / 2 + 1}
            textAnchor="end"
            fill="var(--color-fg)"
            fontSize={11}
            fontFamily="monospace"
          >
            {bar.service}.{bar.operation}
          </text>
          {/* Duration text */}
          <text
            x={labelWidth + bar.left + bar.widthPx + 4}
            y={bar.y + barHeight / 2 + 1}
            fill="var(--color-fg-muted)"
            fontSize={10}
            fontFamily="monospace"
          >
            {msLabel(bar.duration)}
          </text>
        </g>
      ))}
    </svg>
  )
}

function msLabel(ns: number): string {
  if (ns < 1_000_000) return `${(ns / 1000).toFixed(0)}µs`
  if (ns < 1_000_000_000) return `${(ns / 1_000_000).toFixed(1)}ms`
  return `${(ns / 1_000_000_000).toFixed(2)}s`
}
