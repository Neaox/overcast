import { useMemo, useState, useRef, useCallback } from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { useScrollTrigger } from "@/hooks/use-scroll-trigger"
import { cn } from "@/lib/utils"
import Prism from "@/lib/prism"

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

function formatTimestamp(ts?: number): string {
  if (ts == null) return "-"
  const d = new Date(ts)
  const hh = String(d.getHours()).padStart(2, "0")
  const mm = String(d.getMinutes()).padStart(2, "0")
  const ss = String(d.getSeconds()).padStart(2, "0")
  const ms = String(d.getMilliseconds()).padStart(3, "0")
  return `${hh}:${mm}:${ss}.${ms}`
}

/** Detect log level from message content. */
function detectLogLevel(msg: string): "error" | "warn" | "info" | "debug" | null {
  const levelMatch = msg.match(/"level"\s*:\s*"(\w+)"/i)
  if (levelMatch) {
    const l = levelMatch[1].toLowerCase()
    if (l === "error" || l === "fatal" || l === "critical") return "error"
    if (l === "warn" || l === "warning") return "warn"
    if (l === "info") return "info"
    if (l === "debug" || l === "trace") return "debug"
  }
  const prefix = msg.slice(0, 80).toUpperCase()
  if (/\bERROR\b|\bFATAL\b|\bCRITICAL\b/.test(prefix)) return "error"
  if (/\bWARN(ING)?\b/.test(prefix)) return "warn"
  if (/\bDEBUG\b|\bTRACE\b/.test(prefix)) return "debug"
  return null
}

const levelRowColors: Record<string, string> = {
  error: "border-l-red-500/60 bg-red-500/5",
  warn: "border-l-yellow-500/60 bg-yellow-500/5",
  debug: "border-l-fg-muted/30",
}

/** Try to parse structured JSON from a log message. */
function tryParseJSON(msg: string): object | null {
  const trimmed = msg.trim()
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return null
  try {
    return JSON.parse(trimmed) as object
  } catch {
    return null
  }
}

/** Format and highlight JSON with PrismJS. */
function formatJSON(obj: object): string {
  const formatted = JSON.stringify(obj, null, 2)
  return Prism.highlight(formatted, Prism.languages.json, "json")
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
  const isScrollingRef = useRef(false)
  const scrollTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined)

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
        return {
          timestamp: event.timestamp,
          ingestionTime: event.ingestionTime,
          logStreamName: event.logStreamName,
          message: msg,
          level: detectLogLevel(msg),
          json: formatted ? tryParseJSON(msg) : null,
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

  const handleScroll = useCallback(() => {
    isScrollingRef.current = true
    if (scrollTimerRef.current) clearTimeout(scrollTimerRef.current)
    scrollTimerRef.current = setTimeout(() => {
      isScrollingRef.current = false
    }, 150)
  }, [])

  return (
    <div className={cn("flex min-h-0 flex-1 flex-col", className)}>
      {showModeToggle && (
        <div className="mb-2 flex items-center justify-end gap-1.5">
          <button
            type="button"
            onClick={() => setMode("plain")}
            className={cn(
              "rounded border px-2 py-1 text-[10px] font-medium uppercase",
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
              "rounded border px-2 py-1 text-[10px] font-medium uppercase",
              mode === "table"
                ? "border-accent/50 bg-accent/15 text-fg"
                : "border-border text-fg-muted hover:bg-fg-muted/10",
            )}
          >
            Table
          </button>
          <label className="flex cursor-pointer items-center gap-1 rounded border border-border px-2 py-1 text-[10px] font-medium text-fg-muted uppercase select-none hover:bg-fg-muted/10">
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

      <div
        ref={parentRef}
        className="min-h-0 flex-1 overflow-auto rounded bg-bg-elevated p-2"
        onScroll={handleScroll}
      >
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
              if (!event) return null

              const showHighlighted = formatted && event.json != null && !isScrollingRef.current

              return (
                <div
                  key={virtualRow.key}
                  data-index={virtualRow.index}
                  ref={virtualizer.measureElement}
                  className={cn(
                    "absolute top-0 left-0 w-full border-l-2 border-l-transparent",
                    event.level && levelRowColors[event.level],
                  )}
                  style={{ transform: `translateY(${virtualRow.start}px)` }}
                >
                  {mode === "plain" ? (
                    <div className="flex gap-2 py-0.5 font-mono text-[10px] text-fg-subtle">
                      <span className="shrink-0 text-fg-muted tabular-nums">
                        {formatTimestamp(event.timestamp)}
                      </span>
                      {showHighlighted ? (
                        <pre
                          className="min-w-0 leading-relaxed wrap-break-word whitespace-pre-wrap text-fg"
                          dangerouslySetInnerHTML={{ __html: formatJSON(event.json!) }}
                        />
                      ) : (
                        <span className="min-w-0 wrap-break-word whitespace-pre-wrap text-fg">
                          {event.message}
                        </span>
                      )}
                    </div>
                  ) : (
                    <div className="flex border-b border-border-muted">
                      <div className="w-20 shrink-0 py-1 pr-2 font-mono text-[10px] text-fg-muted tabular-nums">
                        {formatTimestamp(event.timestamp)}
                      </div>
                      <div className="min-w-0 flex-1 py-1 pr-2">
                        {showHighlighted ? (
                          <pre
                            className="font-mono text-[10px] leading-relaxed wrap-break-word whitespace-pre-wrap text-fg"
                            dangerouslySetInnerHTML={{ __html: formatJSON(event.json!) }}
                          />
                        ) : (
                          <pre className="font-mono text-[10px] leading-relaxed wrap-break-word whitespace-pre-wrap text-fg">
                            {event.message}
                          </pre>
                        )}
                      </div>
                      <div className="w-20 shrink-0 py-1 font-mono text-[10px] text-fg-muted tabular-nums">
                        {formatTimestamp(event.ingestionTime)}
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
