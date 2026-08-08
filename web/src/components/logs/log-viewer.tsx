import { useMemo, useState, useRef } from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { useScrollTrigger } from "@/hooks/use-scroll-trigger"
import { cn } from "@/lib/utils"
import { stripAnsi } from "@/lib/ansi"
import {
  detectLogLevel,
  formatLogTime,
  formatPlatformRecord,
  highlightJSON,
  logLevelRowClass,
  parsePlatformRecord,
  stringifyJSON,
  tryParseJSON,
} from "@/lib/log-format"
import { AnsiText } from "./ansi-text"

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

  const normalizedEvents = useMemo(
    () =>
      events.map((event) => {
        const msg = String(event.message ?? "")
        // Level detection and JSON parsing read the message as it *reads*: a
        // colourised line starts with an escape sequence, not with `{`.
        const plain = stripAnsi(msg)
        // A Lambda system log record reads as the START / END / REPORT line it
        // replaced; ticking Format swaps in the record itself.
        const platform = parsePlatformRecord(plain)
        return {
          timestamp: event.timestamp,
          ingestionTime: event.ingestionTime,
          logStreamName: event.logStreamName,
          message: (platform && formatPlatformRecord(platform)) ?? msg,
          level: detectLogLevel(plain),
          json: formatted ? tryParseJSON(plain) : null,
        }
      }),
    [events, formatted],
  )

  const virtualizer = useVirtualizer({
    count: normalizedEvents.length,
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
        {loading && normalizedEvents.length === 0 && (
          <div className="py-4 text-center text-[10px] text-fg-muted">Loading logs...</div>
        )}

        {!loading && error && (
          <div className="py-4 text-center text-[10px] text-red-400">{error}</div>
        )}

        {!loading && !error && normalizedEvents.length === 0 && (
          <div className="py-4 text-center text-[10px] text-fg-muted">{emptyMessage}</div>
        )}

        {normalizedEvents.length > 0 && (
          <div
            style={{
              height: `${virtualizer.getTotalSize()}px`,
              width: "100%",
              position: "relative",
            }}
          >
            {virtualizer.getVirtualItems().map((virtualRow) => {
              const event = normalizedEvents[virtualRow.index]

              const showHighlighted = formatted && event.json != null

              return (
                <div
                  key={virtualRow.key}
                  data-index={virtualRow.index}
                  ref={virtualizer.measureElement}
                  className={cn(
                    "absolute top-0 left-0 w-full border-l-2 border-l-transparent",
                    event.level && logLevelRowClass[event.level],
                  )}
                  style={{ transform: `translateY(${virtualRow.start}px)` }}
                >
                  {mode === "plain" ? (
                    <div className="flex gap-2 py-0.5 font-mono text-[10px] text-fg-subtle">
                      <span className="shrink-0 font-mono text-fg-muted tabular-nums">
                        {formatLogTime(event.timestamp)}
                      </span>
                      {showHighlighted ? (
                        <pre
                          className="min-w-0 leading-relaxed wrap-break-word whitespace-pre-wrap text-fg"
                          dangerouslySetInnerHTML={{
                            __html: highlightJSON(stringifyJSON(event.json!, true)),
                          }}
                        />
                      ) : (
                        <span className="min-w-0 wrap-break-word whitespace-pre-wrap text-fg">
                          <AnsiText text={event.message} />
                        </span>
                      )}
                    </div>
                  ) : (
                    <div className="flex border-b border-border-muted">
                      <div className="w-20 shrink-0 py-1 pr-2 font-mono text-[10px] text-fg-muted tabular-nums">
                        {formatLogTime(event.timestamp)}
                      </div>
                      <div className="min-w-0 flex-1 py-1 pr-2">
                        {showHighlighted ? (
                          <pre
                            className="font-mono text-[10px] leading-relaxed wrap-break-word whitespace-pre-wrap text-fg"
                            dangerouslySetInnerHTML={{
                              __html: highlightJSON(stringifyJSON(event.json!, true)),
                            }}
                          />
                        ) : (
                          <pre className="font-mono text-[10px] leading-relaxed wrap-break-word whitespace-pre-wrap text-fg">
                            <AnsiText text={event.message} />
                          </pre>
                        )}
                      </div>
                      <div className="w-20 shrink-0 py-1 font-mono text-[10px] text-fg-muted tabular-nums">
                        {formatLogTime(event.ingestionTime)}
                      </div>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}

        {isFetchingMore && (
          <div className="pt-2 text-center text-[10px] text-fg-muted">Loading more...</div>
        )}

        {!isFetchingMore && canLoadMore && (
          <div ref={sentinelRef} className="h-3" aria-hidden="true" />
        )}

        {!isFetchingMore && !hasMore && normalizedEvents.length > 0 && (
          <div className="pt-2 text-center text-[10px] text-fg-muted">End of logs</div>
        )}
      </div>
    </div>
  )
}
