import { useMemo } from "react"
import { nsToHuman } from "@/features/debug-traces/utils"
import type { TraceHop } from "@/types"

interface WaterfallProps {
  hops: TraceHop[]
  totalDuration: number
  startTime?: string
  onSelectHop?: (hopId: string) => void
  selectedHopId?: string | null
}

const barHeight = 20
const barGap = 6
const labelWidth = 120
const rightPadding = 40

export function Waterfall({ hops, totalDuration, startTime, onSelectHop, selectedHopId }: WaterfallProps) {
  const width = 700
  const chartWidth = width - labelWidth - rightPadding
  const totalHeight = hops.length * (barHeight + barGap)

  const bars = useMemo(() => {
    if (totalDuration <= 0) return []
    const startNs = startTime ? new Date(startTime).getTime() * 1_000_000 : 0
    return hops.map((hop, i) => {
      const hopStartNs = new Date(hop.timestamp).getTime() * 1_000_000
      const offsetNs = startNs > 0 ? hopStartNs - startNs : 0
      const left = startNs > 0 ? Math.max(0, (offsetNs / totalDuration) * chartWidth) : 0
      const widthPx = Math.max(2, (hop.duration / totalDuration) * chartWidth)
      const y = i * (barHeight + barGap)

      let color = "#22c55e"
      if (hop.responseStatus >= 500 || hop.error) color = "#ef4444"
      else if (hop.responseStatus >= 400) color = "#f59e0b"

      return { ...hop, left, widthPx, y, color }
    })
  }, [hops, totalDuration, startTime, chartWidth])

  if (totalDuration <= 0 || hops.length === 0) {
    return <div className="text-center text-fg-muted py-8 text-sm">No timing data available.</div>
  }

  return (
    <svg width={width} height={Math.max(totalHeight, 40)} className="text-xs" role="img" aria-label={`Waterfall chart showing ${hops.length} service calls`}>
      <title>Waterfall chart — {hops.length} calls over {nsToHuman(totalDuration)}</title>
      {[0, 0.25, 0.5, 0.75, 1].map((pct) => {
        const x = labelWidth + pct * chartWidth
        return (
          <g key={pct}>
            <line x1={x} y1={0} x2={x} y2={totalHeight} stroke="var(--color-border)" strokeWidth={0.5} />
            <text x={x} y={totalHeight + 14} textAnchor="middle" fill="var(--color-fg-muted)" fontSize={10}>{nsToHuman(totalDuration * pct)}</text>
          </g>
        )
      })}
      {bars.map((bar) => (
        <g key={bar.id} onClick={() => onSelectHop?.(bar.id)} className="cursor-pointer" opacity={selectedHopId && selectedHopId !== bar.id ? 0.4 : 1}>
          <rect x={labelWidth} y={bar.y} width={chartWidth} height={barHeight} rx={3} fill="var(--color-bg-elevated)" stroke="var(--color-border)" strokeWidth={0.5} />
          <rect x={labelWidth + bar.left} y={bar.y} width={bar.widthPx} height={barHeight} rx={3} fill={bar.color} opacity={0.7} />
          <text x={labelWidth - 4} y={bar.y + barHeight / 2 + 1} textAnchor="end" fill="var(--color-fg)" fontSize={11} fontFamily="monospace">{bar.service}.{bar.operation}</text>
          <text x={labelWidth + bar.left + bar.widthPx + 4} y={bar.y + barHeight / 2 + 1} fill="var(--color-fg-muted)" fontSize={10} fontFamily="monospace">{nsToHuman(bar.duration)}</text>
        </g>
      ))}
    </svg>
  )
}
