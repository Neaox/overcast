/**
 * Reusable Monitor tab/section body (docs/plans/service-metrics-platform.md
 * phase 3 "Web UI plan"): a range selector, one card per catalogue group,
 * loading/error states via the shared QueryListState, and a visible local
 * -retention disclaimer. Lambda's Monitor tab and SQS's Monitor section both
 * render this against their own MonitorCardConfig list — the catalogue
 * differs, the chrome does not (plan: "Build one reusable
 * MetricChart/MetricCard component... rather than duplicating fetch/
 * aggregation/chart code").
 */
import { useEffect, useMemo, useState, type ReactNode } from "react"
import { Maximize2, RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { QueryListState } from "@/components/ui/primitives"
import { Select } from "@/components/ui/select"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import {
  CHART_RANGE_OPTIONS,
  CHART_RANGE_SPAN_MS,
  type ChartRangeToken,
} from "@/features/monitoring/types"
import { MetricLineChart, type ChartSeriesInput } from "./metric-line-chart"
import type { MonitorResponse } from "@/types"

export interface MonitorCardConfig {
  /** Card heading, e.g. "Invocations" or "Duration". */
  title: string
  /** AWS unit shared by every series on this card — cards never mix units on one axis. */
  unit: string
  series: { metric: string; statistic: string; label: string }[]
}

export interface MonitorPanelProps {
  range: ChartRangeToken
  onRangeChange: (range: ChartRangeToken) => void
  isLoading: boolean
  isFetching?: boolean
  error?: unknown
  data?: MonitorResponse
  cards: MonitorCardConfig[]
  onRefresh?: () => void
  /**
   * Extra controls rendered beside the range select in the panel's own
   * header row — e.g. API Gateway's stage picker (#1307). MonitorPanel stays
   * service-agnostic (it never learns what a "stage" is); the caller supplies
   * whatever toolbar control it needs and this slot keeps it visually part of
   * the same row rather than a floating element above/below the panel.
   */
  extraControls?: ReactNode
}

function retentionDisclaimer(range: ChartRangeToken): string {
  switch (range) {
    case "1h":
    case "6h":
    case "24h":
      return "up to 24 hours at 1-minute resolution"
    case "7d":
      return "up to 7 days at 5-minute resolution"
    case "30d":
      return "up to 30 days at 1-hour resolution"
  }
}

export function MonitorPanel({
  range,
  onRangeChange,
  isLoading,
  isFetching,
  error,
  data,
  cards,
  onRefresh,
  extraControls,
}: MonitorPanelProps) {
  // "Now" is component state (lazily initialized, refreshed on the same
  // cadence the metrics query itself polls at) rather than a bare Date.now()
  // call in the render body, which React's purity rule flags as an impure
  // render — see web/src/features/map/lambda-invocations-drawer.tsx's
  // identical nowMs pattern.
  const [nowMs, setNowMs] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNowMs(Date.now()), 30_000)
    return () => clearInterval(id)
  }, [])

  const rangeEndMs = nowMs
  const rangeStartMs = rangeEndMs - CHART_RANGE_SPAN_MS[range]
  const periodSeconds = data?.periodSeconds || 60

  // Recomputed only when the card catalogue or the fetched series actually
  // change — not on every render. Without this memo, the 30s background
  // refetch's isFetching flip (and the panel's own 30s nowMs tick) would
  // re-run this find() per series per card on every tick even though neither
  // `cards` (a caller constant) nor `data` (unchanged between polls that
  // return the same payload) moved.
  const cardsWithSeries = useMemo(
    () =>
      cards.map((card) => ({
        card,
        chartSeries: card.series.map((sk) => {
          const found = data?.series.find(
            (s) => s.metric === sk.metric && s.statistic === sk.statistic,
          )
          return {
            key: `${sk.metric}/${sk.statistic}`,
            label: sk.label,
            unit: card.unit,
            statistic: sk.statistic,
            points: found?.points ?? [],
          } satisfies ChartSeriesInput
        }),
      })),
    [cards, data],
  )

  // Which card is open in the enlarged-chart dialog, by title (titles are the
  // cards' render keys already). The dialog reuses the same memoized series,
  // so it live-updates on the same 30s poll as the card behind it.
  const [expandedTitle, setExpandedTitle] = useState<string | null>(null)
  const expanded = cardsWithSeries.find(({ card }) => card.title === expandedTitle)

  // The expanded view's drag-to-zoom window. Client-side only: the fetched
  // range's points are re-plotted against the narrower window, so zooming is
  // instant and never refetches. Cleared when the dialog closes or another
  // card opens — a zoom is an inspection of one chart, not sticky state.
  const [zoom, setZoom] = useState<{ startMs: number; endMs: number } | null>(null)
  const openExpanded = (title: string | null) => {
    setExpandedTitle(title)
    setZoom(null)
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-3">
          <label className={cn(fieldLabel, "flex items-center gap-2 text-fg-muted")}>
            Time range
            <Select
              value={range}
              onChange={(e) => onRangeChange(e.target.value as ChartRangeToken)}
            >
              {CHART_RANGE_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </Select>
          </label>
          {extraControls}
        </div>
        {onRefresh && (
          <Button size="sm" variant="ghost" onClick={onRefresh} disabled={isFetching}>
            <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
            Refresh
          </Button>
        )}
      </div>

      <QueryListState
        isLoading={isLoading}
        isEmpty={!!error}
        error={error}
        errorTitle="Unable to load metrics"
      />

      {!isLoading &&
        !error &&
        (data && !data.enabled ? (
          <div className="rounded-lg border border-dashed border-border bg-bg-muted/40 p-6 text-center text-sm text-fg-muted">
            Service-metric collection is disabled (
            <code className="font-mono text-xs">OVERCAST_SERVICE_METRICS</code>). Enable it to see
            automatic Monitor charts here.
          </div>
        ) : (
          <>
            <div className="grid gap-4 md:grid-cols-2">
              {cardsWithSeries.map(({ card, chartSeries }) => (
                <div
                  key={card.title}
                  className="flex flex-col gap-2 rounded-xl border border-border bg-bg-elevated p-4"
                >
                  <div className="flex items-center justify-between gap-2">
                    <h3 className="font-mono text-sm font-semibold text-fg">{card.title}</h3>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="h-6 w-6 p-0 text-fg-subtle"
                      aria-label={`Expand ${card.title}`}
                      onClick={() => openExpanded(card.title)}
                    >
                      <Maximize2 aria-hidden className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                  <MetricLineChart
                    series={chartSeries}
                    rangeStartMs={rangeStartMs}
                    rangeEndMs={rangeEndMs}
                    periodSeconds={periodSeconds}
                  />
                </div>
              ))}
            </div>
            <p className="text-xs text-fg-muted">
              Local emulator data only ({retentionDisclaimer(range)}) — not the real AWS CloudWatch
              console.
            </p>
          </>
        ))}

      {/* Enlarged view of one card — same series objects, same live poll, on a
          full-viewport-width dialog (the shared dialog's size variants stop at
          560px, sized for forms rather than charts) with drag-to-zoom. */}
      <Dialog open={expanded != null} onOpenChange={(open) => !open && openExpanded(null)}>
        {expanded && (
          <DialogContent className="max-w-[calc(100vw-3rem)]">
            <DialogHeader>
              <DialogTitle>{expanded.card.title}</DialogTitle>
              <DialogDescription>
                {zoom
                  ? "Zoomed — double-click the chart or press Reset to zoom back out."
                  : "Drag across the chart to zoom into a time window."}
              </DialogDescription>
            </DialogHeader>
            {zoom && (
              <Button
                size="sm"
                variant="ghost"
                className="absolute top-3 right-12"
                onClick={() => setZoom(null)}
              >
                Reset zoom
              </Button>
            )}
            <MetricLineChart
              series={expanded.chartSeries}
              rangeStartMs={zoom?.startMs ?? rangeStartMs}
              rangeEndMs={zoom?.endMs ?? rangeEndMs}
              periodSeconds={periodSeconds}
              height="min(62vh, 680px)"
              onBrushSelect={(startMs, endMs) => setZoom({ startMs, endMs })}
              onBrushReset={() => setZoom(null)}
            />
          </DialogContent>
        )}
      </Dialog>
    </div>
  )
}
