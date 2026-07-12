import { useState, useMemo, useRef, useCallback, useEffect } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { useVirtualizer } from "@tanstack/react-virtual"
import { ArrowLeft, RefreshCw, FileText, Search, X, Copy, Check, ArrowDown } from "lucide-react"
import { logsFilterQueryOptions } from "@/features/cloudwatch/logs/data"
import {
  TimeRangeFilter,
  type TimeRange,
} from "@/features/cloudwatch/logs/components/time-range-filter"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"
import Prism from "@/lib/prism"

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

/** Format and syntax-highlight a JSON object using Prism. Returns HTML string. */
function formatJSON(obj: object): string {
  const formatted = JSON.stringify(obj, null, 2)
  return Prism.highlight(formatted, Prism.languages.json, "json")
}

// ── Row height estimation ──────────────────────────────────────────────────

/** Estimate the row height for a log event based on message length and format state. */
function estimateRowHeight(msg: string, formatted: boolean): number {
  const baseHeight = 36 // padding + timestamp line
  if (formatted && (msg.trim().startsWith("{") || msg.trim().startsWith("["))) {
    // Formatted JSON: count lines in formatted output
    try {
      const obj = JSON.parse(msg.trim())
      const lines = JSON.stringify(obj, null, 2).split("\n").length
      return baseHeight + lines * 18 // ~18px per line in monospace
    } catch {
      // Fall through
    }
  }
  // Plain: wrap estimation based on ~120 chars per line at typical widths
  const lines = Math.max(1, Math.ceil(msg.length / 120))
  return baseHeight + (lines - 1) * 18
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
  streamName: string
}

export function LogEventsViewer({ groupName, streamName }: Props) {
  const navigate = useNavigate()
  const [filterInput, setFilterInput] = useState("")
  const [activeFilter, setActiveFilter] = useState("")
  const [timeRange, setTimeRange] = useState<TimeRange>({})
  const [formatted, setFormatted] = useState(false)
  const [wrapLines, setWrapLines] = useState(true)

  const parentRef = useRef<HTMLDivElement>(null)
  const isScrollingRef = useRef(false)
  const scrollTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined)

  const { data, isLoading, isFetching, refetch } = useQuery(
    logsFilterQueryOptions(groupName, {
      filterPattern: activeFilter || undefined,
      startTime: timeRange.startTime,
      endTime: timeRange.endTime,
      logStreamNames: [streamName],
    }),
  )

  const events = useMemo(() => data?.events ?? [], [data])

  // Pre-compute row metadata (level detection, JSON parse) once per data change
  const rowMeta = useMemo(
    () =>
      events.map((evt) => {
        const msg = evt.message ?? ""
        const level = detectLogLevel(msg)
        const json = formatted ? tryParseJSON(msg) : null
        return { msg, level, json }
      }),
    [events, formatted],
  )

  const virtualizer = useVirtualizer({
    count: events.length,
    getScrollElement: () => parentRef.current,
    estimateSize: (index) => estimateRowHeight(rowMeta[index]?.msg ?? "", formatted),
    overscan: 15,
  })

  // Track scrolling state — defer formatting while scrolling
  const handleScroll = useCallback(() => {
    isScrollingRef.current = true
    if (scrollTimerRef.current) clearTimeout(scrollTimerRef.current)
    scrollTimerRef.current = setTimeout(() => {
      isScrollingRef.current = false
    }, 150)
  }, [])

  // Scroll-to-bottom
  const [showScrollBottom, setShowScrollBottom] = useState(false)
  const handleScrollCheck = useCallback(() => {
    handleScroll()
    const el = parentRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80
    setShowScrollBottom(!atBottom && events.length > 20)
  }, [handleScroll, events.length])

  const scrollToBottom = useCallback(() => {
    virtualizer.scrollToIndex(events.length - 1, { align: "end" })
    setShowScrollBottom(false)
  }, [virtualizer, events.length])

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

  return (
    <div className="flex h-full w-full flex-col gap-3">
      <PageHeader
        title={streamName}
        description={`Log group: ${groupName}`}
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
              Back to Streams
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
        <div className="flex items-center gap-1.5">
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
              checked={wrapLines}
              onChange={(e) => setWrapLines(e.target.checked)}
              className="h-3 w-3 accent-accent"
            />
            Wrap
          </label>
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
          {/* Sticky column headers */}
          <div className="flex border-b border-border bg-bg-elevated px-1 py-1.5 text-[10px] font-medium text-fg-muted">
            <div className="w-10 shrink-0 px-1 text-center">#</div>
            <div className="w-20 shrink-0 px-1">Time</div>
            <div className="min-w-0 flex-1 px-1">Message</div>
          </div>

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
              {virtualizer.getVirtualItems().map((virtualRow) => {
                const evt = events[virtualRow.index]
                const meta = rowMeta[virtualRow.index]
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
                    {/* Line number */}
                    <div className="flex w-10 shrink-0 items-start justify-center pt-1.5 text-[9px] text-fg-muted/40 tabular-nums select-none">
                      {virtualRow.index + 1}
                    </div>
                    {/* Timestamp */}
                    <div className="flex w-20 shrink-0 items-start px-1 pt-1.5 font-mono text-[10px] text-fg-muted tabular-nums">
                      {formatTimestampCompact(evt.timestamp ?? undefined)}
                    </div>
                    {/* Message */}
                    <div className="min-w-0 flex-1 px-1 py-1.5">
                      <LogMessage
                        message={meta.msg}
                        json={meta.json}
                        formatted={formatted}
                        wrapLines={wrapLines}
                        isScrolling={isScrollingRef}
                        filterPattern={activeFilter}
                        level={meta.level}
                      />
                    </div>
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
  message,
  json,
  formatted,
  wrapLines,
  isScrolling,
  filterPattern,
  level,
}: {
  message: string
  json: object | null
  formatted: boolean
  wrapLines: boolean
  isScrolling: React.RefObject<boolean>
  filterPattern: string
  level: "error" | "warn" | "info" | "debug" | null
}) {
  // When formatted + JSON and not scrolling, show syntax-highlighted output
  const showFormatted = formatted && json != null && !isScrolling.current

  if (showFormatted) {
    return (
      <div className="flex items-start gap-1.5">
        {level && (
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
            "font-mono text-[11px] leading-relaxed",
            wrapLines ? "wrap-break-word whitespace-pre-wrap" : "whitespace-pre",
          )}
          dangerouslySetInnerHTML={{ __html: formatJSON(json) }}
        />
      </div>
    )
  }

  // Plain message — with optional filter highlighting
  return (
    <div className="flex items-start gap-1.5">
      {level && formatted && (
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
        {highlightMatches(message, filterPattern)}
      </pre>
    </div>
  )
}
