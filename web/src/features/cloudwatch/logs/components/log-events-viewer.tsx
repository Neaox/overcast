import { useState, useMemo, useRef, useCallback, useEffect } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link, useNavigate } from "@tanstack/react-router"
import { useVirtualizer } from "@tanstack/react-virtual"
import {
  ArrowDown,
  ArrowDownUp,
  ArrowLeft,
  Check,
  Copy,
  FileText,
  RefreshCw,
  Search,
  X,
  Zap,
} from "lucide-react"
import { logsFilterQueryOptions } from "@/features/cloudwatch/logs/data"
import {
  TimeRangeFilter,
  type TimeRange,
} from "@/features/cloudwatch/logs/components/time-range-filter"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { useEventStream } from "@/hooks/use-event-stream"
import { EventType } from "@/services/event-types"
import { cn } from "@/lib/utils"
import Prism from "@/lib/prism"
import type { FilteredLogEvent } from "@/types/logs"

interface LogEventsWrittenPayload {
  logGroupName: string
  logStreamName: string
  events: Array<{ timestamp: number; message: string }>
}

// ── Helpers ────────────────────────────────────────────────────────────────

function formatTimestampCompact(ts?: number): string {
  if (ts == null) return "—"
  const d = new Date(ts)
  const hh = String(d.getHours()).padStart(2, "0")
  const mm = String(d.getMinutes()).padStart(2, "0")
  const ss = String(d.getSeconds()).padStart(2, "0")
  const ms = String(d.getMilliseconds()).padStart(3, "0")
  return `${hh}:${mm}:${ss}.${ms}`
}

/** Parse a filter pattern into individual search terms. */
function parseFilterTerms(pattern: string): string[] {
  const terms: string[] = []
  let remaining = pattern.trim()
  while (remaining) {
    if (remaining[0] === '"') {
      const end = remaining.indexOf('"', 1)
      if (end >= 0) {
        terms.push(remaining.substring(1, end))
        remaining = remaining.substring(end + 1).trim()
      } else {
        terms.push(remaining.substring(1))
        remaining = ""
      }
    } else {
      const idx = remaining.search(/[\s\t]/)
      if (idx >= 0) {
        terms.push(remaining.substring(0, idx))
        remaining = remaining.substring(idx).trim()
      } else {
        terms.push(remaining)
        remaining = ""
      }
    }
  }
  return terms
}

/** Highlight matching filter terms in a message string. */
function highlightMatches(message: string, filterPattern: string): React.ReactNode {
  if (!filterPattern) return message
  const terms = parseFilterTerms(filterPattern)
  if (terms.length === 0) return message
  const escaped = terms.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
  const regex = new RegExp(`(${escaped.join("|")})`, "gi")
  const parts = message.split(regex)
  return parts.map((part, i) =>
    regex.test(part) ? (
      <mark key={i} className="rounded-sm bg-yellow-400/30 px-0.5 text-inherit">
        {part}
      </mark>
    ) : (
      part
    ),
  )
}

/** Try to detect and extract the log level from a message. */
function detectLogLevel(msg: string): "error" | "warn" | "info" | "debug" | null {
  // Check structured JSON logs first
  const levelMatch = msg.match(/"level"\s*:\s*"(\w+)"/i)
  if (levelMatch) {
    const l = levelMatch[1].toLowerCase()
    if (l === "error" || l === "fatal" || l === "critical") return "error"
    if (l === "warn" || l === "warning") return "warn"
    if (l === "info") return "info"
    if (l === "debug" || l === "trace") return "debug"
  }
  // Check common text patterns (only at word boundaries near the start)
  const prefix = msg.slice(0, 80).toUpperCase()
  if (/\bERROR\b|\bFATAL\b|\bCRITICAL\b/.test(prefix)) return "error"
  if (/\bWARN(ING)?\b/.test(prefix)) return "warn"
  if (/\bDEBUG\b|\bTRACE\b/.test(prefix)) return "debug"
  return null
}

const levelColors = {
  error: "border-l-red-500/60 bg-red-500/5",
  warn: "border-l-yellow-500/60 bg-yellow-500/5",
  info: "",
  debug: "border-l-fg-muted/30 bg-fg-muted/3",
}

const levelBadge = {
  error: "bg-red-500/15 text-red-400",
  warn: "bg-yellow-500/15 text-yellow-400",
  info: "bg-sky-500/15 text-sky-400",
  debug: "bg-fg-muted/15 text-fg-muted",
}

/** Try to parse a message as structured JSON. Returns null if not JSON. */
function tryParseJSON(msg: string): object | null {
  const trimmed = msg.trim()
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return null
  try {
    return JSON.parse(trimmed) as object
  } catch {
    return null
  }
}

function stringifyJSON(obj: object, pretty: boolean): string {
  return JSON.stringify(obj, null, pretty ? 2 : 0)
}

function highlightJSON(text: string): string {
  return Prism.highlight(text, Prism.languages.json, "json")
}

// ── Row height estimation ──────────────────────────────────────────────────

/** Estimate the row height for a log event based on message length and format state. */
function estimateRowHeight(msg: string, formatted: boolean): number {
  const baseHeight = 36 // padding + timestamp line
  if (formatted && (msg.trim().startsWith("{") || msg.trim().startsWith("["))) {
    return baseHeight + Math.ceil(msg.length / 48) * 18
  }
  // Plain: wrap estimation based on ~120 chars per line at typical widths
  const lines = Math.max(1, Math.ceil(msg.length / 120))
  return baseHeight + (lines - 1) * 18
}

function sortEvents(events: FilteredLogEvent[], asc: boolean): FilteredLogEvent[] {
  return [...events].sort((a, b) => {
    const timeDelta = (a.timestamp ?? 0) - (b.timestamp ?? 0)
    if (timeDelta !== 0) return asc ? timeDelta : -timeDelta
    const ingestDelta = (a.ingestionTime ?? 0) - (b.ingestionTime ?? 0)
    if (ingestDelta !== 0) return asc ? ingestDelta : -ingestDelta
    return (a.logStreamName ?? "").localeCompare(b.logStreamName ?? "") * (asc ? 1 : -1)
  })
}

function messageMatchesFilter(message: string, filterPattern: string): boolean {
  const terms = parseFilterTerms(filterPattern).map((term) => term.toLowerCase())
  if (terms.length === 0) return true
  const haystack = message.toLowerCase()
  return terms.every((term) => haystack.includes(term))
}

// ── Copy button ────────────────────────────────────────────────────────────

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(text).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }, [text])
  return (
    <button
      type="button"
      onClick={handleCopy}
      className="rounded p-0.5 text-fg-muted/40 opacity-0 transition-opacity group-hover/row:opacity-100 hover:text-fg-muted"
      title="Copy message"
    >
      {copied ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
    </button>
  )
}

// ── Main component ─────────────────────────────────────────────────────────

interface Props {
  groupName: string
  streamName?: string
}

export function LogEventsViewer({ groupName, streamName }: Props) {
  const navigate = useNavigate()
  const [filterInput, setFilterInput] = useState("")
  const [activeFilter, setActiveFilter] = useState("")
  const [timeRange, setTimeRange] = useState<TimeRange>({})
  const [displayMode, setDisplayMode] = useState<"table" | "plain">("table")
  const [formatted, setFormatted] = useState(false)
  const [syntaxHighlight, setSyntaxHighlight] = useState(true)
  const [wrapLines, setWrapLines] = useState(true)
  const [tailMode, setTailMode] = useState(false)
  const [sortAsc, setSortAsc] = useState(true)
  const [tailEvents, setTailEvents] = useState<FilteredLogEvent[]>([])

  const parentRef = useRef<HTMLDivElement>(null)
  const processedEventCount = useRef(0)

  const { data, dataUpdatedAt, isLoading, isFetching, refetch } = useQuery({
    ...logsFilterQueryOptions(groupName, {
      filterPattern: activeFilter || undefined,
      startTime: timeRange.startTime,
      endTime: timeRange.endTime,
      ...(streamName ? { logStreamNames: [streamName] } : {}),
    }),
  })

  const { events: streamEvents } = useEventStream({ source: "logs" })

  useEffect(() => {
    setTailEvents([])
  }, [groupName, streamName, activeFilter, timeRange.startTime, timeRange.endTime])

  useEffect(() => {
    setTailEvents([])
  }, [dataUpdatedAt])

  useEffect(() => {
    if (!tailMode) {
      processedEventCount.current = streamEvents.length
      return
    }
    const newEvents = streamEvents.slice(processedEventCount.current)
    processedEventCount.current = streamEvents.length
    if (newEvents.length === 0) return

    const incoming: FilteredLogEvent[] = []
    for (const streamEvent of newEvents) {
      if (streamEvent.type !== EventType.logs.LogEventsWritten) continue
      const payload = streamEvent.payload as LogEventsWrittenPayload
      if (payload.logGroupName !== groupName) continue
      if (streamName && payload.logStreamName !== streamName) continue
      for (const event of payload.events) {
        if (timeRange.startTime != null && event.timestamp < timeRange.startTime) continue
        if (timeRange.endTime != null && event.timestamp > timeRange.endTime) continue
        if (!messageMatchesFilter(event.message, activeFilter)) continue
        incoming.push({
          timestamp: event.timestamp,
          ingestionTime: event.timestamp,
          logStreamName: payload.logStreamName,
          message: event.message,
        })
      }
    }
    if (incoming.length > 0) {
      setTailEvents((prev) => [...prev, ...incoming])
    }
  }, [
    activeFilter,
    groupName,
    streamEvents,
    streamName,
    tailMode,
    timeRange.endTime,
    timeRange.startTime,
  ])

  const events = useMemo(
    () => sortEvents([...(data?.events ?? []), ...tailEvents], sortAsc),
    [data, sortAsc, tailEvents],
  )

  // Pre-compute cheap row metadata once per data change. JSON parsing/highlighting stays row-local.
  const rowMeta = useMemo(
    () =>
      events.map((evt) => {
        const msg = evt.message ?? ""
        const level = detectLogLevel(msg)
        return { msg, level }
      }),
    [events],
  )

  const virtualizer = useVirtualizer({
    count: events.length,
    getScrollElement: () => parentRef.current,
    estimateSize: (index) => estimateRowHeight(rowMeta[index]?.msg ?? "", formatted),
    overscan: 15,
  })

  // Scroll-to-bottom
  const [showScrollBottom, setShowScrollBottom] = useState(false)
  const handleScrollCheck = useCallback(() => {
    const el = parentRef.current
    if (!el) return
    const nearLatest = sortAsc
      ? el.scrollHeight - el.scrollTop - el.clientHeight < 80
      : el.scrollTop < 80
    setShowScrollBottom(!nearLatest && events.length > 20)
  }, [events.length, sortAsc])

  const scrollToBottom = useCallback(() => {
    virtualizer.scrollToIndex(sortAsc ? events.length - 1 : 0, { align: sortAsc ? "end" : "start" })
    setShowScrollBottom(false)
  }, [virtualizer, events.length, sortAsc])

  const handleSearch = () => setActiveFilter(filterInput)
  const handleClear = () => {
    setFilterInput("")
    setActiveFilter("")
  }

  // Keyboard shortcut: Ctrl/Cmd+F focuses filter, Escape clears
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "f") {
        e.preventDefault()
        const input = document.querySelector<HTMLInputElement>("[data-log-filter]")
        input?.focus()
      }
    }
    document.addEventListener("keydown", handler)
    return () => document.removeEventListener("keydown", handler)
  }, [])

  const title = streamName ?? groupName
  const description = streamName ? (
    <>
      Log group:{" "}
      <Link
        to="/cloudwatch/logs/$groupName"
        params={{ groupName }}
        className="font-mono text-accent hover:underline"
      >
        {groupName}
      </Link>
    </>
  ) : (
    "All streams in this log group"
  )
  const virtualItems = virtualizer.getVirtualItems()
  const scrollOffset = virtualizer.scrollOffset ?? 0
  const viewportHeight = parentRef.current?.clientHeight ?? 0
  const highlightStart = Math.max(0, scrollOffset - viewportHeight)
  const highlightEnd = scrollOffset + viewportHeight * 2

  return (
    <div className="flex h-full w-full flex-col gap-3">
      <PageHeader
        title={title}
        description={description}
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                navigate({
                  to: "/cloudwatch/logs/group",
                  search: { groupName },
                })
              }
            >
              <ArrowLeft className="mr-1.5 h-3.5 w-3.5" />
              {streamName ? "Back to Streams" : "Back to Group"}
            </Button>
            <Button variant="ghost" size="sm" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
          </div>
        }
      />

      {/* Toolbar: filter + toggles */}
      <div className="flex flex-wrap items-center gap-2">
        {/* Filter bar */}
        <div className="flex min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-bg-muted px-3 py-2">
          <Search className="h-4 w-4 shrink-0 text-fg-muted" />
          <Input
            data-log-filter
            className="h-7 border-0 bg-transparent px-1 shadow-none focus-visible:ring-0"
            placeholder='Filter — e.g. ERROR, "request failed", ERROR timeout'
            value={filterInput}
            onChange={(e) => setFilterInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") handleSearch()
              if (e.key === "Escape") handleClear()
            }}
          />
          {(filterInput || activeFilter) && (
            <Button size="sm" variant="ghost" onClick={handleClear} className="h-7 px-2">
              <X className="h-3.5 w-3.5" />
            </Button>
          )}
          <div className="mx-0.5 h-5 w-px bg-border" />
          <TimeRangeFilter value={timeRange} onChange={setTimeRange} />
          <Button
            size="sm"
            onClick={handleSearch}
            disabled={isFetching || !filterInput.trim()}
            className="h-7"
          >
            {isFetching ? <Spinner className="mr-1.5 h-3.5 w-3.5" /> : null}
            Search
          </Button>
          {activeFilter && (
            <span className="ml-1 shrink-0 text-xs text-fg-muted">
              {events.length} result{events.length !== 1 ? "s" : ""}
            </span>
          )}
        </div>

        {/* View toggles */}
        <div className="flex flex-wrap items-center gap-1.5">
          <Button
            type="button"
            size="sm"
            variant={displayMode === "table" ? "default" : "ghost"}
            onClick={() => setDisplayMode("table")}
            className="h-7 px-2 text-[10px] uppercase"
          >
            Table
          </Button>
          <Button
            type="button"
            size="sm"
            variant={displayMode === "plain" ? "default" : "ghost"}
            onClick={() => setDisplayMode("plain")}
            className="h-7 px-2 text-[10px] uppercase"
          >
            Plaintext
          </Button>
          <label className="flex cursor-pointer items-center gap-1.5 rounded border border-border px-2 py-1.5 text-[10px] font-medium text-fg-muted uppercase select-none hover:bg-fg-muted/10">
            <input
              type="checkbox"
              checked={formatted}
              onChange={(e) => setFormatted(e.target.checked)}
              className="h-3 w-3 accent-accent"
            />
            Format
          </label>
          <label className="flex cursor-pointer items-center gap-1.5 rounded border border-border px-2 py-1.5 text-[10px] font-medium text-fg-muted uppercase select-none hover:bg-fg-muted/10">
            <input
              type="checkbox"
              checked={syntaxHighlight}
              onChange={(e) => setSyntaxHighlight(e.target.checked)}
              className="h-3 w-3 accent-accent"
            />
            Syntax
          </label>
          <label className="flex cursor-pointer items-center gap-1.5 rounded border border-border px-2 py-1.5 text-[10px] font-medium text-fg-muted uppercase select-none hover:bg-fg-muted/10">
            <input
              type="checkbox"
              checked={wrapLines}
              onChange={(e) => setWrapLines(e.target.checked)}
              className="h-3 w-3 accent-accent"
            />
            Wrap
          </label>
          <Button
            type="button"
            size="sm"
            variant={tailMode ? "default" : "ghost"}
            onClick={() => setTailMode((v) => !v)}
            className="h-7 px-2 text-[10px] uppercase"
            title="Live tail refreshes the current filtered view"
          >
            <Zap className="mr-1 h-3 w-3" />
            Tail
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => setSortAsc((v) => !v)}
            className="h-7 px-2 text-[10px] uppercase"
          >
            <ArrowDownUp className="mr-1 h-3 w-3" />
            {sortAsc ? "Oldest" : "Newest"}
          </Button>
          <span className="ml-1 text-[10px] text-fg-muted tabular-nums">
            {events.length.toLocaleString()} event{events.length !== 1 ? "s" : ""}
          </span>
        </div>
      </div>

      {/* Log content */}
      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : events.length === 0 ? (
        <EmptyState
          icon={<FileText className="h-10 w-10" />}
          title={activeFilter ? "No matching events" : "No log events"}
          description={
            activeFilter ? "Try a different filter pattern." : "This stream has no events yet."
          }
        />
      ) : (
        <div className="relative min-h-0 flex-1">
          {displayMode === "table" && (
            <div className="flex border-b border-border bg-bg-elevated px-1 py-1.5 text-[10px] font-medium text-fg-muted">
              <div className="w-10 shrink-0 px-1 text-center">#</div>
              <div className="w-20 shrink-0 px-1">Time</div>
              {!streamName && <div className="w-44 shrink-0 px-1">Stream</div>}
              <div className="min-w-0 flex-1 px-1">Message</div>
            </div>
          )}

          {/* Virtualized rows */}
          <div
            ref={parentRef}
            className="min-h-0 flex-1 overflow-auto"
            onScroll={handleScrollCheck}
            style={{ height: "calc(100vh - 280px)" }}
          >
            <div
              style={{
                height: `${virtualizer.getTotalSize()}px`,
                width: "100%",
                position: "relative",
              }}
            >
              {virtualItems.map((virtualRow) => {
                const evt = events[virtualRow.index]
                const meta = rowMeta[virtualRow.index]
                const enableSyntax =
                  syntaxHighlight &&
                  virtualRow.end >= highlightStart &&
                  virtualRow.start <= highlightEnd
                return (
                  <div
                    key={virtualRow.key}
                    data-index={virtualRow.index}
                    ref={virtualizer.measureElement}
                    className={cn(
                      "group/row absolute top-0 left-0 flex w-full border-b border-l-2 border-border-muted border-l-transparent",
                      meta.level && levelColors[meta.level],
                    )}
                    style={{
                      transform: `translateY(${virtualRow.start}px)`,
                    }}
                  >
                    {displayMode === "table" ? (
                      <>
                        <div className="flex w-10 shrink-0 items-start justify-center pt-1.5 text-[9px] text-fg-muted/40 tabular-nums select-none">
                          {virtualRow.index + 1}
                        </div>
                        <div className="flex w-20 shrink-0 items-start px-1 pt-1.5 font-mono text-[10px] text-fg-muted tabular-nums">
                          {formatTimestampCompact(evt.timestamp ?? undefined)}
                        </div>
                        {!streamName && (
                          <div className="flex w-44 shrink-0 items-start px-1 pt-1.5 font-mono text-[10px] text-fg-muted">
                            {evt.logStreamName}
                          </div>
                        )}
                        <div className="min-w-0 flex-1 px-1 py-1.5">
                          <LogMessage
                            message={meta.msg}
                            formatted={formatted}
                            syntaxHighlight={enableSyntax}
                            wrapLines={wrapLines}
                            filterPattern={activeFilter}
                            level={meta.level}
                          />
                        </div>
                      </>
                    ) : (
                      <div className="min-w-0 flex-1 px-2 py-1.5">
                        <LogMessage
                          prefix={`${formatTimestampCompact(evt.timestamp ?? undefined)}${evt.logStreamName ? ` ${evt.logStreamName}` : ""}`}
                          message={meta.msg}
                          formatted={formatted}
                          syntaxHighlight={enableSyntax}
                          wrapLines={wrapLines}
                          filterPattern={activeFilter}
                          level={meta.level}
                          hideLevel
                        />
                      </div>
                    )}
                    {/* Actions */}
                    <div className="flex w-8 shrink-0 items-start justify-center pt-1.5">
                      <CopyButton text={meta.msg} />
                    </div>
                  </div>
                )
              })}
            </div>
          </div>

          {/* Scroll to bottom FAB */}
          {showScrollBottom && (
            <button
              type="button"
              onClick={scrollToBottom}
              className="absolute right-4 bottom-4 z-10 flex items-center gap-1 rounded-full border border-border bg-bg-elevated px-3 py-1.5 text-[10px] font-medium text-fg-muted shadow-lg hover:bg-bg-subtle"
            >
              <ArrowDown className="h-3 w-3" />
              Latest
            </button>
          )}
        </div>
      )}
    </div>
  )
}

// ── Log message cell ───────────────────────────────────────────────────────

function LogMessage({
  prefix,
  message,
  formatted,
  syntaxHighlight,
  wrapLines,
  filterPattern,
  level,
  hideLevel = false,
}: {
  prefix?: string
  message: string
  formatted: boolean
  syntaxHighlight: boolean
  wrapLines: boolean
  filterPattern: string
  level: "error" | "warn" | "info" | "debug" | null
  hideLevel?: boolean
}) {
  const jsonText = useMemo(() => {
    if (!formatted && !syntaxHighlight) return null
    const json = tryParseJSON(message)
    if (!json) return null
    return stringifyJSON(json, formatted)
  }, [formatted, message, syntaxHighlight])
  const displayText = formatted && jsonText ? jsonText : `${prefix ? `${prefix} ` : ""}${message}`
  const showSyntax = syntaxHighlight && jsonText

  if (showSyntax) {
    return (
      <div className="flex items-start gap-1.5">
        {level && !hideLevel && (
          <span
            className={cn(
              "mt-0.5 shrink-0 rounded px-1 py-0.5 text-[8px] font-bold uppercase",
              levelBadge[level],
            )}
          >
            {level}
          </span>
        )}
        {prefix && !formatted && (
          <span className="shrink-0 pt-0.5 font-mono text-[11px] leading-relaxed text-fg-muted tabular-nums">
            {prefix}
          </span>
        )}
        <pre
          className={cn(
            "font-mono text-[11px] leading-relaxed",
            wrapLines ? "wrap-break-word whitespace-pre-wrap" : "whitespace-pre",
          )}
          dangerouslySetInnerHTML={{ __html: highlightJSON(jsonText) }}
        />
      </div>
    )
  }

  // Plain message — with optional filter highlighting
  return (
    <div className="flex items-start gap-1.5">
      {level && formatted && !hideLevel && (
        <span
          className={cn(
            "mt-0.5 shrink-0 rounded px-1 py-0.5 text-[8px] font-bold uppercase",
            levelBadge[level],
          )}
        >
          {level}
        </span>
      )}
      <pre
        className={cn(
          "font-mono text-[11px] leading-relaxed text-fg",
          wrapLines ? "wrap-break-word whitespace-pre-wrap" : "whitespace-pre",
        )}
      >
        {highlightMatches(displayText, filterPattern)}
      </pre>
    </div>
  )
}
