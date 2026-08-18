import { memo, useState, useRef } from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { useScrollTrigger } from "@/hooks/use-scroll-trigger"
import { cn } from "@/lib/utils"
import { describeLogEvent, formatLogTime, logLevelRowClass } from "@/lib/log-format"
import { LogMessage } from "./log-message"

export interface LogViewerEvent {
  timestamp?: number
  message?: string
  ingestionTime?: number
  logStreamName?: string
}

interface LogViewerProps {
  events: LogViewerEvent[]
  loading?: boolean
  error?: string | null
  emptyMessage?: string
  hasMore?: boolean
  isFetchingMore?: boolean
  onLoadMore?: () => void
  defaultMode?: "table" | "plain"
  showModeToggle?: boolean
  className?: string
}

export function LogViewer({
  events,
  loading = false,
  error,
  emptyMessage = "No log events found",
  hasMore = false,
  isFetchingMore = false,
  onLoadMore,
  defaultMode = "plain",
  showModeToggle = true,
  className,
}: LogViewerProps) {
  const [mode, setMode] = useState<"table" | "plain">(defaultMode)
  const [formatted, setFormatted] = useState(false)
  const parentRef = useRef<HTMLDivElement>(null)

  const canLoadMore = Boolean(hasMore && onLoadMore)
  const sentinelRef = useScrollTrigger({
    onTrigger: () => {
      if (!onLoadMore || isFetchingMore || !hasMore) return
      onLoadMore()
    },
    enabled: canLoadMore && !isFetchingMore,
    direction: "down",
    rootMargin: "120px",
  })

  // Row content is derived per row, not per list: this used to map every event
  // on any change and — with Format ticked — `JSON.parse` all of them, so a
  // 10,000-event page paid 10,000 parses to show the ~30 rows on screen.
  // `describeLogEvent` caches the cheap part per event; the parse is row-local.
  const virtualizer = useVirtualizer({
    count: events.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 32,
    overscan: 15,
  })

  return (
    <div className={cn("flex min-h-0 flex-1 flex-col", className)}>
      {showModeToggle && (
        <div className="mb-2 flex items-center justify-end gap-1.5">
          <button
            type="button"
            onClick={() => setMode("plain")}
            className={cn(
              "rounded border px-2 py-1 font-mono text-[10px] font-medium uppercase",
              mode === "plain"
                ? "border-accent/50 bg-accent/15 text-fg"
                : "border-border text-fg-muted hover:bg-fg-muted/10",
            )}
          >
            Plain
          </button>
          <button
            type="button"
            onClick={() => setMode("table")}
            className={cn(
              "rounded border px-2 py-1 font-mono text-[10px] font-medium uppercase",
              mode === "table"
                ? "border-accent/50 bg-accent/15 text-fg"
                : "border-border text-fg-muted hover:bg-fg-muted/10",
            )}
          >
            Table
          </button>
          <label className="flex cursor-pointer items-center gap-1 rounded border border-border px-2 py-1 font-mono text-[10px] font-medium text-fg-muted uppercase select-none hover:bg-fg-muted/10">
            <input
              type="checkbox"
              checked={formatted}
              onChange={(e) => setFormatted(e.target.checked)}
              className="h-3 w-3 accent-accent"
            />
            Format
          </label>
        </div>
      )}

      <div ref={parentRef} className="min-h-0 flex-1 overflow-auto rounded bg-bg-elevated p-2">
        {loading && events.length === 0 && (
          <div className="py-4 text-center text-[10px] text-fg-muted">Loading logs...</div>
        )}

        {!loading && error && (
          <div className="py-4 text-center text-[10px] text-red-400">{error}</div>
        )}

        {!loading && !error && events.length === 0 && (
          <div className="py-4 text-center text-[10px] text-fg-muted">{emptyMessage}</div>
        )}

        {events.length > 0 && (
          <div
            style={{
              height: `${virtualizer.getTotalSize()}px`,
              width: "100%",
              position: "relative",
            }}
          >
            {virtualizer.getVirtualItems().map((virtualRow) => (
              <div
                key={virtualRow.key}
                data-index={virtualRow.index}
                ref={virtualizer.measureElement}
                className="absolute top-0 left-0 w-full"
                style={{ transform: `translateY(${virtualRow.start}px)` }}
              >
                <LogViewerRow event={events[virtualRow.index]} mode={mode} formatted={formatted} />
              </div>
            ))}
          </div>
        )}

        {isFetchingMore && (
          <div className="pt-2 text-center text-[10px] text-fg-muted">Loading more...</div>
        )}

        {!isFetchingMore && canLoadMore && (
          <div ref={sentinelRef} className="h-3" aria-hidden="true" />
        )}

        {!isFetchingMore && !hasMore && events.length > 0 && (
          <div className="pt-2 text-center text-[10px] text-fg-muted">End of logs</div>
        )}
      </div>
    </div>
  )
}

/**
 * One log line.
 *
 * Memoised and derived row-locally: the virtualizer re-renders its visible rows
 * on every scroll frame, so anything computed here is computed at 60 Hz unless
 * the row can bail out. `event` comes from a query cache that hands out stable
 * objects, and the remaining props are booleans, so an unchanged row re-renders
 * to identical output and leaves the DOM alone.
 */
const LogViewerRow = memo(function LogViewerRow({
  event,
  mode,
  formatted,
}: {
  event: LogViewerEvent
  mode: "table" | "plain"
  formatted: boolean
}) {
  const { level, summary } = describeLogEvent(event)

  // The shared message pipeline, with this surface's choices: pretty JSON and
  // its highlighting arrive together behind the one Format toggle, the level
  // shows as the row tint below rather than as a badge, and nothing here
  // filters, so there is no matcher.
  const body = (
    <LogMessage
      message={String(event.message ?? "")}
      summary={summary}
      formatted={formatted}
      syntaxHighlight={formatted}
      wrapLines
      filterMatcher={null}
      level={level}
      hideLevel
      sizeClassName="text-[10px]"
    />
  )

  return (
    <div
      className={cn(
        "flex border-l-2 border-l-transparent",
        mode === "table"
          ? "border-b border-border-muted"
          : "gap-2 py-0.5 font-mono text-[10px] text-fg-subtle",
        level && logLevelRowClass[level],
      )}
    >
      {mode === "plain" ? (
        <>
          <span className="shrink-0 font-mono text-fg-muted tabular-nums">
            {formatLogTime(event.timestamp)}
          </span>
          {body}
        </>
      ) : (
        <>
          <div className="w-20 shrink-0 py-1 pr-2 font-mono text-[10px] text-fg-muted tabular-nums">
            {formatLogTime(event.timestamp)}
          </div>
          <div className="min-w-0 flex-1 py-1 pr-2">{body}</div>
          <div className="w-20 shrink-0 py-1 font-mono text-[10px] text-fg-muted tabular-nums">
            {formatLogTime(event.ingestionTime)}
          </div>
        </>
      )}
    </div>
  )
})
