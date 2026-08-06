import { useMemo, useState } from "react"
import { nsToHuman } from "@/features/debug-traces/utils"
import type { TraceHop } from "@/types"

interface SequenceDiagramProps {
  hops: TraceHop[]
  entryService: string
  onSelectHop?: (hopId: string) => void
  selectedHopId?: string | null
}

const colWidth = 150
const colGap = 40
const headerHeight = 36
const topPad = 10

/**
 * SVG sequence diagram showing services as vertical lifelines and hops as
 * arrows between them, ordered top-to-bottom by time.
 */
export function SequenceDiagram({
  hops,
  entryService,
  onSelectHop,
  selectedHopId,
}: SequenceDiagramProps) {
  const [hideNoisy, setHideNoisy] = useState(true)

  const { services, arrows } = useMemo(() => {
    const svcSet = new Set<string>([entryService])
    for (const h of hops) {
      svcSet.add(h.callerService)
      svcSet.add(h.service)
    }
    const svcList = Array.from(svcSet)
    const svcIdx = new Map(svcList.map((s, i) => [s, i]))

    const filtered = hideNoisy ? hops.filter((h) => !h.noisy) : hops
    const arrs = filtered.map((hop, i) => {
      const fromIdx = svcIdx.get(hop.callerService) ?? 0
      const toIdx = svcIdx.get(hop.service) ?? 0

      let color = "#22c55e"
      if (hop.responseStatus >= 500 || hop.error) color = "#ef4444"
      else if (hop.responseStatus >= 400) color = "#f59e0b"

      return { ...hop, fromIdx, toIdx, color, row: i }
    })

    return { services: svcList, arrows: arrs }
  }, [hops, entryService, hideNoisy])

  if (hops.length === 0) {
    return <div className="text-center text-fg-muted py-8 text-sm">No hops to display.</div>
  }

  const svcCount = services.length
  const svgWidth = svcCount * colWidth + (svcCount - 1) * colGap + 40
  const rowHeight = 48
  const svgHeight = headerHeight + arrows.length * rowHeight + 20

  const xPos = (i: number) => 20 + i * (colWidth + colGap)
  const noisyCount = hops.filter((h) => h.noisy).length

  return (
    <div>
      {noisyCount > 0 && (
        <label className="flex items-center gap-2 text-xs text-fg-muted mb-2 cursor-pointer select-none">
          <input
            type="checkbox"
            checked={!hideNoisy}
            onChange={(e) => setHideNoisy(!e.target.checked)}
            className="rounded"
          />
          Show {noisyCount} poll/noisy hops
        </label>
      )}
      <div className="overflow-x-auto rounded-lg border border-border">
        <svg width={Math.max(svgWidth, 400)} height={svgHeight} className="text-xs">
          {/* Lifelines */}
          {services.map((svc, i) => {
            const x = xPos(i) + colWidth / 2
            return (
              <g key={svc}>
                <rect
                  x={xPos(i)}
                  y={0}
                  width={colWidth}
                  height={headerHeight}
                  rx={4}
                  fill="var(--color-bg-elevated)"
                />
                <text
                  x={x}
                  y={headerHeight / 2 + 4}
                  textAnchor="middle"
                  fill="var(--color-accent)"
                  fontSize={11}
                  fontFamily="monospace"
                  fontWeight={svc === entryService ? "bold" : "normal"}
                >
                  {svc}
                </text>
                <line
                  x1={x}
                  y1={headerHeight}
                  x2={x}
                  y2={svgHeight - 10}
                  stroke="var(--color-border)"
                  strokeWidth={1}
                  strokeDasharray="4 4"
                />
              </g>
            )
          })}

          {/* Arrows */}
          {arrows.map((a) => {
            const y = headerHeight + topPad + a.row * rowHeight
            const fromX = xPos(a.fromIdx) + colWidth / 2
            const toX = xPos(a.toIdx) + colWidth / 2
            const selected = selectedHopId === a.id

            return (
              <g
                key={a.id}
                onClick={() => onSelectHop?.(a.id)}
                className="cursor-pointer"
                opacity={selectedHopId && !selected ? 0.3 : 1}
              >
                <line
                  x1={fromX}
                  y1={y}
                  x2={toX}
                  y2={y}
                  stroke={a.color}
                  strokeWidth={selected ? 2 : 1}
                />
                {/* Arrowhead */}
                <polygon
                  points={
                    toX > fromX
                      ? `${toX - 6},${y - 4} ${toX},${y} ${toX - 6},${y + 4}`
                      : `${toX + 6},${y - 4} ${toX},${y} ${toX + 6},${y + 4}`
                  }
                  fill={a.color}
                />
                <text
                  x={(fromX + toX) / 2}
                  y={y - 6}
                  textAnchor="middle"
                  fill={a.color}
                  fontSize={10}
                  fontFamily="monospace"
                >
                  {a.operation}
                </text>
                <text
                  x={(fromX + toX) / 2}
                  y={y + 14}
                  textAnchor="middle"
                  fill="var(--color-fg-muted)"
                  fontSize={9}
                  fontFamily="monospace"
                >
                  {nsToHuman(a.duration)}
                </text>
              </g>
            )
          })}
        </svg>
      </div>
    </div>
  )
}
