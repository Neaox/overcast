/**
 * Reusable multi-series line chart for the Monitor tab/section
 * (docs/plans/service-metrics-platform.md phase 3). Follows the repo's
 * dataviz conventions: fixed categorical color order (the `cat` ramp's
 * `text-cat-1`…`text-cat-5` utilities, never a generated hue), thin 2px lines, a legend
 * whenever there is more than one series, a hover crosshair + tooltip, and a
 * single shared y-axis (never dual-axis — see this file's card usage, which
 * only ever groups series sharing one unit).
 *
 * A gap in the data (a period with no observation — "a missing metric means
 * no observation was emitted, not a synthetic zero") is never bridged by a
 * straight line: points are split into contiguous segments wherever the time
 * between two consecutive points exceeds 1.5x the requested period, and each
 * segment is drawn as its own polyline.
 */
import { useMemo, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import type { ChartPoint } from "@/types"
import { seriesColorClass } from "@/features/monitoring/palette"

export interface ChartSeriesInput {
  key: string
  label: string
  unit: string
  points: ChartPoint[]
}

interface Segment {
  points: { x: number; y: number; value: number; t: number }[]
}

const CHART_WIDTH = 100
const CHART_HEIGHT = 100

function buildSegments(
  points: ChartPoint[],
  rangeStartMs: number,
  rangeEndMs: number,
  minV: number,
  maxV: number,
  gapThresholdMs: number,
): Segment[] {
  const span = rangeEndMs - rangeStartMs || 1
  const valueSpan = maxV - minV || 1
  const sorted = [...points]
    .map((p) => ({ t: new Date(p.timestamp).getTime(), value: p.value }))
    .sort((a, b) => a.t - b.t)

  const segments: Segment[] = []
  let current: Segment["points"] = []
  let prevT: number | null = null

  for (const p of sorted) {
    if (prevT != null && p.t - prevT > gapThresholdMs) {
      if (current.length > 0) segments.push({ points: current })
      current = []
    }
    const x = ((p.t - rangeStartMs) / span) * CHART_WIDTH
    const y = CHART_HEIGHT - ((p.value - minV) / valueSpan) * CHART_HEIGHT
    current.push({ x, y, value: p.value, t: p.t })
    prevT = p.t
  }
  if (current.length > 0) segments.push({ points: current })
  return segments
}

function formatValue(value: number, unit: string): string {
  const rounded = Math.abs(value) >= 100 ? Math.round(value).toString() : value.toFixed(1)
  if (unit === "Count" || unit === "None") return rounded
  if (unit === "Milliseconds") return `${rounded} ms`
  if (unit === "Seconds") return `${rounded} s`
  if (unit === "Bytes") return `${rounded} B`
  return `${rounded} ${unit}`
}

function formatTimestamp(ms: number, spanMs: number): string {
  const d = new Date(ms)
  // A span under a day shows time-of-day; a longer span shows the date too,
  // since "14:32" is ambiguous across a 30-day chart but noise on a 1h one.
  if (spanMs <= 24 * 60 * 60 * 1000) {
    return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
  }
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  })
}

export interface MetricLineChartProps {
  series: ChartSeriesInput[]
  rangeStartMs: number
  rangeEndMs: number
  periodSeconds: number
  height?: number
  /** Forces the y-axis floor to 0 even when every value is 0 — the AWS console convention for an idle metric. */
  yMin?: number
}

export function MetricLineChart({
  series,
  rangeStartMs,
  rangeEndMs,
  periodSeconds,
  height = 160,
  yMin = 0,
}: MetricLineChartProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [hoverFraction, setHoverFraction] = useState<number | null>(null)

  const hasAnyPoints = series.some((s) => s.points.length > 0)

  const { minV, maxV } = useMemo(() => {
    const values = series.flatMap((s) => s.points.map((p) => p.value))
    if (values.length === 0) return { minV: yMin, maxV: yMin + 1 }
    const lo = Math.min(yMin, ...values)
    const hi = Math.max(...values)
    return { minV: lo, maxV: hi === lo ? hi + 1 : hi * 1.08 }
  }, [series, yMin])

  const gapThresholdMs = Math.max(periodSeconds, 1) * 1000 * 1.5

  const segmentsBySeries = useMemo(
    () =>
      series.map((s) =>
        buildSegments(s.points, rangeStartMs, rangeEndMs, minV, maxV, gapThresholdMs),
      ),
    [series, rangeStartMs, rangeEndMs, minV, maxV, gapThresholdMs],
  )

  if (!hasAnyPoints) {
    return (
      <div
        className="flex items-center justify-center rounded-lg border border-dashed border-border bg-bg-muted/40 text-sm text-fg-muted"
        style={{ height }}
      >
        No metric data in this range.
      </div>
    )
  }

  const hoverMs =
    hoverFraction != null ? rangeStartMs + hoverFraction * (rangeEndMs - rangeStartMs) : null

  // Nearest point per series to the hovered time, for the tooltip — never
  // interpolated: a series with no point near the cursor simply has none.
  const hoverReadouts =
    hoverMs != null
      ? series.map((s) => {
          let nearest: ChartPoint | undefined
          let nearestDelta = Infinity
          for (const p of s.points) {
            const t = new Date(p.timestamp).getTime()
            const delta = Math.abs(t - hoverMs)
            if (delta < nearestDelta && delta <= gapThresholdMs) {
              nearest = p
              nearestDelta = delta
            }
          }
          return { series: s, point: nearest }
        })
      : []

  return (
    <div className="flex flex-col gap-2">
      <div
        ref={containerRef}
        className="relative rounded-lg border border-border bg-bg-elevated p-3"
        style={{ height }}
        onMouseMove={(e) => {
          const rect = containerRef.current?.getBoundingClientRect()
          if (!rect || rect.width === 0) return
          const fraction = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width))
          setHoverFraction(fraction)
        }}
        onMouseLeave={() => setHoverFraction(null)}
      >
        <svg
          viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
          preserveAspectRatio="none"
          className="h-full w-full"
          role="img"
          aria-label={`${series.map((s) => s.label).join(", ")} over time`}
        >
          {/* Recessive gridlines — 25/50/75%. */}
          {[25, 50, 75].map((pct) => (
            <line
              key={pct}
              x1={0}
              x2={CHART_WIDTH}
              y1={pct}
              y2={pct}
              className="stroke-border"
              strokeWidth={0.4}
              vectorEffect="non-scaling-stroke"
            />
          ))}

          {segmentsBySeries.map((segments, seriesIdx) =>
            segments.map((seg, segIdx) => (
              <polyline
                key={`${seriesIdx}-${segIdx}`}
                fill="none"
                points={seg.points.map((p) => `${p.x},${p.y}`).join(" ")}
                stroke="currentColor"
                strokeWidth={1.4}
                strokeLinecap="round"
                strokeLinejoin="round"
                vectorEffect="non-scaling-stroke"
                className={seriesColorClass(seriesIdx)}
              />
            )),
          )}

          {hoverFraction != null && (
            <line
              x1={hoverFraction * CHART_WIDTH}
              x2={hoverFraction * CHART_WIDTH}
              y1={0}
              y2={CHART_HEIGHT}
              className="stroke-fg-muted"
              strokeWidth={0.4}
              vectorEffect="non-scaling-stroke"
            />
          )}
        </svg>

        {hoverMs != null && (
          <div
            className="pointer-events-none absolute top-1 z-10 max-w-[220px] rounded-md border border-border bg-bg-elevated px-2 py-1.5 text-xs shadow-md"
            style={{
              left: `${Math.min(85, Math.max(2, hoverFraction! * 100))}%`,
            }}
          >
            <div className="mb-1 font-mono text-fg-muted">
              {formatTimestamp(hoverMs, rangeEndMs - rangeStartMs)}
            </div>
            {hoverReadouts.map(({ series: s, point }, idx) => (
              <div key={s.key} className="flex items-center gap-1.5">
                <span
                  className={cn("h-2 w-2 shrink-0 rounded-full bg-current", seriesColorClass(idx))}
                />
                <span className="text-fg-muted">{s.label}:</span>
                <span className="font-mono text-fg">
                  {point ? formatValue(point.value, s.unit) : "no data"}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>

      {series.length > 1 && (
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          {series.map((s, idx) => (
            <div key={s.key} className="flex items-center gap-1.5 text-xs text-fg-muted">
              <span
                className={cn("h-2 w-2 shrink-0 rounded-full bg-current", seriesColorClass(idx))}
              />
              {s.label}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
