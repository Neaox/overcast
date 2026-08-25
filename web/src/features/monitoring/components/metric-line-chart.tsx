/**
 * Reusable multi-series line chart for the Monitor tab/section
 * (docs/plans/service-metrics-platform.md phase 3). Follows the repo's
 * dataviz conventions: fixed categorical color order (the `cat` ramp's
 * `text-cat-1`…`text-cat-5` utilities, never a generated hue), thin 2px lines, a legend
 * that doubles as each series' whole-range summary readout, a hover
 * crosshair + tooltip, a three-value y-axis scale, and a single shared
 * y-axis (never dual-axis — see this file's card usage, which only ever
 * groups series sharing one unit).
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
  /**
   * The CloudWatch statistic behind this series ("Sum", "Average", ...).
   * Drives the legend's at-a-glance summary — a Sum series summarizes as the
   * range total, an Average as the mean, a Maximum as the peak. Optional so
   * a caller without one still gets a chart; the summary then falls back to
   * the latest value.
   */
  statistic?: string
  points: ChartPoint[]
}

/** The legend's one-number summary of a whole series, per its statistic. */
function summarizeSeries(points: ChartPoint[], statistic: string | undefined): number | null {
  if (points.length === 0) return null
  const values = points.map((p) => p.value)
  switch (statistic) {
    case "Sum":
    case "SampleCount":
      return values.reduce((a, b) => a + b, 0)
    case "Average":
      return values.reduce((a, b) => a + b, 0) / values.length
    case "Maximum":
      return Math.max(...values)
    case "Minimum":
      return Math.min(...values)
    default:
      return values[values.length - 1]
  }
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
  // Whole numbers stay whole — "80 requests", never "80.0"; only a genuinely
  // fractional value under 100 keeps one decimal of precision.
  const rounded =
    Math.abs(value) >= 100 || Number.isInteger(value)
      ? Math.round(value).toString()
      : value.toFixed(1)
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
  /** Pixel height, or any CSS height ("min(62vh, 680px)") for enlarged views. */
  height?: number | string
  /** Forces the y-axis floor to 0 even when every value is 0 — the AWS console convention for an idle metric. */
  yMin?: number
  /**
   * Enables drag-to-zoom: the user drags a time window across the plot and
   * this fires with its [startMs, endMs]. The caller zooms by re-rendering
   * with those as rangeStartMs/rangeEndMs — client-side on already-fetched
   * points, no refetch. Left off for the small cards, where a drag is more
   * likely a slipped click than an intent.
   */
  onBrushSelect?: (startMs: number, endMs: number) => void
  /** Double-click resets a zoom; only meaningful alongside onBrushSelect. */
  onBrushReset?: () => void
}

export function MetricLineChart({
  series,
  rangeStartMs,
  rangeEndMs,
  periodSeconds,
  height = 160,
  yMin = 0,
  onBrushSelect,
  onBrushReset,
}: MetricLineChartProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [hoverFraction, setHoverFraction] = useState<number | null>(null)
  // In-progress drag selection, as x fractions of the plot width.
  const [brush, setBrush] = useState<{ start: number; end: number } | null>(null)

  const gapThresholdMs = Math.max(periodSeconds, 1) * 1000 * 1.5

  // Everything downstream — scale, segments, legend summaries, hover — reads
  // only the points inside the displayed window (one gap-threshold of slack so
  // a boundary bucket is not dropped). For the normal card view the fetch
  // already returns only in-range points, so this is a no-op; when the
  // expanded view zooms, it is what makes the y-axis and the legend readouts
  // describe the visible slice rather than the whole fetched range.
  const visibleSeries = useMemo(
    () =>
      series.map((s) => ({
        ...s,
        points: s.points.filter((p) => {
          const t = new Date(p.timestamp).getTime()
          return t >= rangeStartMs - gapThresholdMs && t <= rangeEndMs + gapThresholdMs
        }),
      })),
    [series, rangeStartMs, rangeEndMs, gapThresholdMs],
  )

  const hasAnyPoints = visibleSeries.some((s) => s.points.length > 0)

  const { minV, maxV } = useMemo(() => {
    const values = visibleSeries.flatMap((s) => s.points.map((p) => p.value))
    if (values.length === 0) return { minV: yMin, maxV: yMin + 1 }
    const lo = Math.min(yMin, ...values)
    const hi = Math.max(...values)
    return { minV: lo, maxV: hi === lo ? hi + 1 : hi * 1.08 }
  }, [visibleSeries, yMin])

  const segmentsBySeries = useMemo(
    () =>
      visibleSeries.map((s) =>
        buildSegments(s.points, rangeStartMs, rangeEndMs, minV, maxV, gapThresholdMs),
      ),
    [visibleSeries, rangeStartMs, rangeEndMs, minV, maxV, gapThresholdMs],
  )

  const fractionFromEvent = (clientX: number): number | null => {
    const rect = containerRef.current?.getBoundingClientRect()
    if (!rect || rect.width === 0) return null
    return Math.min(1, Math.max(0, (clientX - rect.left) / rect.width))
  }

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
      ? visibleSeries.map((s) => {
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
        className={cn(
          "relative rounded-lg border border-border bg-bg-elevated p-3",
          onBrushSelect && "cursor-crosshair select-none",
        )}
        style={{ height }}
        onMouseDown={(e) => {
          if (!onBrushSelect || e.button !== 0) return
          const f = fractionFromEvent(e.clientX)
          if (f != null) setBrush({ start: f, end: f })
        }}
        onMouseMove={(e) => {
          const f = fractionFromEvent(e.clientX)
          if (f == null) return
          setHoverFraction(f)
          if (brush) setBrush({ start: brush.start, end: f })
        }}
        onMouseUp={() => {
          if (!brush || !onBrushSelect) return
          const [lo, hi] = [Math.min(brush.start, brush.end), Math.max(brush.start, brush.end)]
          setBrush(null)
          const span = rangeEndMs - rangeStartMs
          // A selection narrower than one bucket is a slipped click, not a zoom.
          if ((hi - lo) * span < Math.max(periodSeconds, 1) * 1000) return
          onBrushSelect(rangeStartMs + lo * span, rangeStartMs + hi * span)
        }}
        onDoubleClick={() => onBrushReset?.()}
        onMouseLeave={() => {
          setHoverFraction(null)
          setBrush(null)
        }}
      >
        {/* The in-progress drag selection. */}
        {brush && (
          <div
            className="pointer-events-none absolute inset-y-0 z-[1] border-x border-accent bg-accent-muted/40"
            style={{
              left: `${Math.min(brush.start, brush.end) * 100}%`,
              width: `${Math.abs(brush.end - brush.start) * 100}%`,
            }}
          />
        )}
        {/* Y-axis readout, rendered as HTML over the plot rather than as SVG
            <text> — the viewBox's non-uniform scale would stretch glyphs.
            Values sit exactly on the gridlines (plus the ceiling and floor)
            so a line's height reads against a labeled reference, not an
            unlabeled stripe. */}
        {[0, 25, 50, 75, 100].map((pct) => (
          <span
            key={pct}
            className="pointer-events-none absolute left-1.5 z-[1] font-mono text-[9px] leading-none text-fg-subtle"
            style={{
              top: `calc(0.75rem + ${pct / 100} * (100% - 1.5rem))`,
              transform:
                pct === 0 ? "none" : pct === 100 ? "translateY(-100%)" : "translateY(-50%)",
            }}
          >
            {formatValue(maxV - (pct / 100) * (maxV - minV), series[0]?.unit ?? "None")}
          </span>
        ))}
        <svg
          viewBox={`0 0 ${CHART_WIDTH} ${CHART_HEIGHT}`}
          preserveAspectRatio="none"
          className="h-full w-full"
          role="img"
          aria-label={`${series.map((s) => s.label).join(", ")} over time`}
        >
          {/* Recessive gridlines — horizontal at the labeled 25/50/75%
              values, vertical at the quarter marks of the time window. */}
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
          {[25, 50, 75].map((pct) => (
            <line
              key={`x-${pct}`}
              x1={pct}
              x2={pct}
              y1={0}
              y2={CHART_HEIGHT}
              className="stroke-border/60"
              strokeWidth={0.4}
              vectorEffect="non-scaling-stroke"
            />
          ))}

          {segmentsBySeries.map((segments, seriesIdx) =>
            segments.map((seg, segIdx) => {
              // A one-point segment (an isolated bucket, common right after
              // the first burst of traffic on a 1-minute-resolution chart)
              // has no line to draw — a single-point polyline paints nothing,
              // leaving a chart that looks empty despite real data (#1307).
              // Duplicating the point makes it a zero-length segment, which
              // the round linecap renders as a dot; non-scaling-stroke keeps
              // it a screen-space circle despite the viewBox's non-uniform
              // scale (an SVG <circle> here would stretch into an ellipse).
              const isDot = seg.points.length === 1
              const points = isDot
                ? `${seg.points[0].x},${seg.points[0].y} ${seg.points[0].x},${seg.points[0].y}`
                : seg.points.map((p) => `${p.x},${p.y}`).join(" ")
              return (
                <polyline
                  key={`${seriesIdx}-${segIdx}`}
                  fill="none"
                  points={points}
                  stroke="currentColor"
                  strokeWidth={isDot ? 4 : 1.4}
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  vectorEffect="non-scaling-stroke"
                  className={seriesColorClass(seriesIdx)}
                />
              )
            }),
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

      {/* X-axis readout: the window's start, quarter marks, and end. Kept
          outside the plot so labels never sit under data; the middle three
          line up with the vertical gridlines above. */}
      <div className="flex justify-between font-mono text-[9px] leading-none text-fg-subtle">
        {[0, 25, 50, 75, 100].map((pct) => (
          <span key={pct}>
            {formatTimestamp(
              rangeStartMs + (pct / 100) * (rangeEndMs - rangeStartMs),
              rangeEndMs - rangeStartMs,
            )}
          </span>
        ))}
      </div>

      {/* The legend doubles as the at-a-glance readout: each entry carries its
          series' whole-range summary (total for Sum, mean for Average, peak
          for Maximum — summarizeSeries) so the headline number is readable
          without hovering. That is why a single-series chart renders it too,
          where it used to be omitted as redundant with the card title. */}
      <div className="flex flex-wrap gap-x-4 gap-y-1">
        {visibleSeries.map((s, idx) => {
          const summary = summarizeSeries(s.points, s.statistic)
          return (
            <div key={s.key} className="flex items-center gap-1.5 text-xs text-fg-muted">
              <span
                className={cn("h-2 w-2 shrink-0 rounded-full bg-current", seriesColorClass(idx))}
              />
              {s.label}
              {summary != null && (
                <span className="font-mono text-fg">{formatValue(summary, s.unit)}</span>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
