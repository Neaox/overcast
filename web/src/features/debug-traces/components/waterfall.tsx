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

const barHeight = 24
const barGap = 4
const labelWidth = 140
const chartPad = 12

export function Waterfall({ hops, totalDuration, startTime, onSelectHop, selectedHopId }: WaterfallProps) {
  const totalHeight = hops.length * (barHeight + barGap) + 24

  const bars = useMemo(() => {
    if (totalDuration <= 0) return []
    const startNs = startTime ? new Date(startTime).getTime() * 1_000_000 : 0
    return hops.map((hop, i) => {
      const hopStartNs = new Date(hop.timestamp).getTime() * 1_000_000
      const offsetNs = startNs > 0 ? hopStartNs - startNs : 0
      return {
        ...hop,
        offsetNs,
        y: i * (barHeight + barGap),
      }
    })
  }, [hops, totalDuration, startTime])

  if (totalDuration <= 0 || hops.length === 0) {
    return <div className="text-center text-fg-muted py-8 text-sm">No timing data available.</div>
  }

  const maxOffset = Math.max(...bars.map((b) => b.offsetNs + b.duration), 1)

  function color(hop: TraceHop): string {
    if (hop.responseStatus >= 500 || hop.error) return "#ef4444"
    if (hop.responseStatus >= 400) return "#f59e0b"
    return "#22c55e"
  }

  return (
    <svg className="w-full text-xs" viewBox={`0 0 ${labelWidth + 600} ${totalHeight}`} preserveAspectRatio="xMidYMin meet" role="img" aria-label={`Waterfall chart showing ${hops.length} service calls`}>
      <title>Waterfall — {hops.length} calls over {nsToHuman(totalDuration)}</title>
      <defs>
        <linearGradient id="wf-bg" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0%" stopColor="var(--color-bg-elevated)" />
          <stop offset="100%" stopColor="var(--color-bg)" />
        </linearGradient>
      </defs>
      {/* Time axis */}
      {[0, 0.25, 0.5, 0.75, 1].map((pct) => {
        const x = labelWidth + chartPad + pct * (600 - chartPad * 2)
        return (
          <g key={pct}>
            <line x1={x} y1={0} x2={x} y2={totalHeight - 20} stroke="var(--color-border)" strokeWidth={0.5} />
            <text x={x} y={totalHeight - 4} textAnchor="middle" fill="var(--color-fg-muted)" fontSize={9}>{nsToHuman(maxOffset * pct)}</text>
          </g>
        )
      })}
      {bars.map((bar) => {
        const chartW = 600 - chartPad * 2
        const left = labelWidth + chartPad + Math.max(0, (bar.offsetNs / maxOffset) * chartW)
        const w = Math.max(3, (bar.duration / maxOffset) * chartW)
        const c = color(bar)
        const sel = selectedHopId === bar.id
        return (
          <g key={bar.id} onClick={() => onSelectHop?.(bar.id)} className="cursor-pointer" opacity={selectedHopId && !sel ? 0.35 : 1}>
            <title>{bar.service}.{bar.operation} — {nsToHuman(bar.duration)}</title>
            <rect x={labelWidth} y={bar.y} width={600} height={barHeight} rx={4} fill="url(#wf-bg)" stroke={sel ? c : "var(--color-border)"} strokeWidth={sel ? 1.5 : 0.5} />
            <rect x={left} y={bar.y + 3} width={w} height={barHeight - 6} rx={3} fill={c} opacity={sel ? 1 : 0.75} />
            <text x={labelWidth - 4} y={bar.y + barHeight / 2 + 1} textAnchor="end" fill="var(--color-fg)" fontSize={11} fontFamily="monospace">{bar.service}.{bar.operation}</text>
            <text x={left + w + 6} y={bar.y + barHeight / 2 + 1} fill="var(--color-fg-muted)" fontSize={10} fontFamily="monospace">{nsToHuman(bar.duration)}</text>
          </g>
        )
      })}
    </svg>
  )
}
