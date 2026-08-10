import { useState, useMemo, useRef, useCallback, useEffect, useLayoutEffect } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link, useNavigate } from "@tanstack/react-router"
import { useVirtualizer } from "@tanstack/react-virtual"
import {
  ArrowDown,
  ArrowDownUp,
  ArrowLeft,
  Eraser,
  FileText,
  RefreshCw,
  Search,
  Undo2,
  X,
  Zap,
} from "lucide-react"
import { CopyButton } from "@/components/ui/copy-button"
import { logsFilterQueryOptions } from "@/features/cloudwatch/logs/data"
import {
  TimeRangeFilter,
  type TimeRange,
} from "@/features/cloudwatch/logs/components/time-range-filter"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"
import { stripAnsi } from "@/lib/ansi"
import {
  detectLogLevel,
  formatLogTime,
  formatPlatformRecord,
  highlightJSON,
  logLevelBadgeClass,
  logLevelRowClass,
  parsePlatformRecord,
  stringifyJSON,
  tryParseJSON,
  type LogLevel,
} from "@/lib/log-format"
import { AnsiText } from "@/components/logs/ansi-text"
import type { FilteredLogEvent } from "@/types/logs"
import { parseLogFilterTerms, tailLogEvents } from "@/features/cloudwatch/logs/tail"

// ── Helpers ────────────────────────────────────────────────────────────────

/** Highlight matching filter terms in a message string. */
function highlightMatches(message: string, filterPattern: string): React.ReactNode {
  if (!filterPattern) return message
  const terms = parseLogFilterTerms(filterPattern)
  if (terms.length === 0) return message
  const escaped = terms.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
  const parts = message.split(new RegExp(`(${escaped.join("|")})`, "gi"))
  // `split` with a capturing group interleaves the captures, so the matches are
  // exactly the odd indices. Re-testing each part against the pattern would
  // read a global regex's `lastIndex` between calls and skip every other match.
  return parts.map((part, i) =>
    i % 2 === 1 ? (
      <mark key={i} className="rounded-sm bg-yellow-400/30 px-0.5 text-inherit">
        {part}
      </mark>
    ) : (
      part
    ),
  )
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

/** Stable empty fallback — a fresh `[]` would re-run every memo keyed on it. */
const NO_EVENTS: FilteredLogEvent[] = []

function sortEvents(events: FilteredLogEvent[], asc: boolean): FilteredLogEvent[] {
  return [...events].sort((a, b) => {
    const timeDelta = (a.timestamp ?? 0) - (b.timestamp ?? 0)
    if (timeDelta !== 0) return asc ? timeDelta : -timeDelta
    const ingestDelta = (a.ingestionTime ?? 0) - (b.ingestionTime ?? 0)
    if (ingestDelta !== 0) return asc ? ingestDelta : -ingestDelta
    return (a.logStreamName ?? "").localeCompare(b.logStreamName ?? "") * (asc ? 1 : -1)
  })
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
  // Clearing the buffer hides everything on screen without stopping the tail.
  // The live events are simply dropped; the fetched ones are still in the
  // query cache, so they are held back by timestamp instead — a marker that
  // survives the refetches that would otherwise put them straight back.
  const [clearedThrough, setClearedThrough] = useState<number | null>(null)

  const parentRef = useRef<HTMLDivElement>(null)
  const pinnedToLatestRef = useRef(true)
  const previousEventCountRef = useRef(0)

  const { data, dataUpdatedAt, isLoading, isFetching, refetch } = useQuery({
    ...logsFilterQueryOptions(groupName, {
      filterPattern: activeFilter || undefined,
      startTime: timeRange.startTime,
      endTime: timeRange.endTime,
      ...(streamName ? { logStreamNames: [streamName] } : {}),
    }),
  })

  useEffect(() => {
    setTailEvents([])
    setClearedThrough(null)
  }, [groupName, streamName, activeFilter, timeRange.startTime, timeRange.endTime])

  useEffect(() => {
    setTailEvents([])
  }, [dataUpdatedAt])

  useEffect(() => {
    if (!tailMode) return

    const controller = new AbortController()
    void (async () => {
      for await (const event of tailLogEvents({
        groupIdentifier: groupName,
        streamName,
        filterPattern: activeFilter,
        signal: controller.signal,
      })) {
        setTailEvents((prev) => [...prev, event])
      }
    })()

    return () => controller.abort()
  }, [activeFilter, groupName, streamName, tailMode])

  const fetchedEvents = data?.events ?? NO_EVENTS
  const visibleFetched = useMemo(
    () =>
      clearedThrough == null
        ? fetchedEvents
        : fetchedEvents.filter((evt) => (evt.timestamp ?? 0) > clearedThrough),
    [fetchedEvents, clearedThrough],
  )
  const clearedCount = fetchedEvents.length - visibleFetched.length

  const events = useMemo(
    () => sortEvents([...visibleFetched, ...tailEvents], sortAsc),
    [visibleFetched, sortAsc, tailEvents],
  )

  // Pre-compute cheap row metadata once per data change. JSON parsing/highlighting stays row-local.
  const rowMeta = useMemo(
    () =>
      events.map((evt) => {
        const msg = evt.message ?? ""
        // `plain` is the message without its escape sequences — what the level
        // detector reads, what the row height is estimated from, and what the
        // copy button puts on the clipboard.
        const plain = stripAnsi(msg)
        // A Lambda system log record reads as the START / END / REPORT line it
        // replaced; ticking Format swaps in the record itself.
        const platform = parsePlatformRecord(plain)
        return {
          msg,
          plain,
          level: detectLogLevel(plain),
          summary: platform ? formatPlatformRecord(platform) : null,
        }
      }),
    [events],
  )

  const virtualizer = useVirtualizer({
    count: events.length,
    getScrollElement: () => parentRef.current,
    estimateSize: (index) => estimateRowHeight(rowMeta[index]?.plain ?? "", formatted),
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
    pinnedToLatestRef.current = nearLatest
    setShowScrollBottom(!nearLatest && events.length > 20)
  }, [events.length, sortAsc])

  const scrollToBottom = useCallback(() => {
    virtualizer.scrollToIndex(sortAsc ? events.length - 1 : 0, { align: sortAsc ? "end" : "start" })
    pinnedToLatestRef.current = true
    setShowScrollBottom(false)
  }, [virtualizer, events.length, sortAsc])

  /**
   * Empty the view, leaving the tail running so only events from here on show.
   *
   * The cut is taken from the newest timestamp on screen rather than the wall
   * clock — the emulator stamps the events, and its clock is the only one that
   * decides which side of the line a record falls on.
   */
  const clearBuffer = useCallback(() => {
    setClearedThrough(
      events.reduce((newest, evt) => Math.max(newest, evt.timestamp ?? 0), clearedThrough ?? 0),
    )
    setTailEvents([])
    pinnedToLatestRef.current = true
    setShowScrollBottom(false)
  }, [events, clearedThrough])

  const restoreCleared = useCallback(() => setClearedThrough(null), [])

  useLayoutEffect(() => {
    if (events.length <= previousEventCountRef.current) {
      previousEventCountRef.current = events.length
      return
    }

    previousEventCountRef.current = events.length
    if (!tailMode || !pinnedToLatestRef.current) return
    virtualizer.scrollToIndex(sortAsc ? events.length - 1 : 0, { align: sortAsc ? "end" : "start" })
  }, [events.length, sortAsc, tailMode, virtualizer])

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
  // An emptied buffer is not an empty log group, and saying so would read as a
  // bug in whatever the user is debugging.
  const emptyState =
    clearedThrough !== null
      ? {
          title: "Buffer cleared",
          description: tailMode
            ? "Waiting for new events — anything from before the clear is hidden."
            : clearedCount > 0
              ? "Turn on Tail to watch for new events, or show the earlier ones again."
              : "Turn on Tail to watch for new events.",
        }
      : activeFilter
        ? { title: "No matching events", description: "Try a different filter pattern." }
        : { title: "No log events", description: "This stream has no events yet." }

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
            <Button
              variant="ghost"
              size="sm"
              // Refresh means "reload this window of logs" — honouring a cut
              // taken before it would hand back an empty screen.
              onClick={() => {
                restoreCleared()
                void refetch()
              }}
              disabled={isFetching}
            >
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
          <label className="flex cursor-pointer items-center gap-1.5 rounded border border-border px-2 py-1.5 font-mono text-[10px] font-medium text-fg-muted uppercase select-none hover:bg-fg-muted/10">
            <input
              type="checkbox"
              checked={formatted}
              onChange={(e) => setFormatted(e.target.checked)}
              className="h-3 w-3 accent-accent"
            />
            Format
          </label>
          <label className="flex cursor-pointer items-center gap-1.5 rounded border border-border px-2 py-1.5 font-mono text-[10px] font-medium text-fg-muted uppercase select-none hover:bg-fg-muted/10">
            <input
              type="checkbox"
              checked={syntaxHighlight}
              onChange={(e) => setSyntaxHighlight(e.target.checked)}
              className="h-3 w-3 accent-accent"
            />
            Syntax
          </label>
          <label className="flex cursor-pointer items-center gap-1.5 rounded border border-border px-2 py-1.5 font-mono text-[10px] font-medium text-fg-muted uppercase select-none hover:bg-fg-muted/10">
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
            onClick={clearBuffer}
            disabled={events.length === 0}
            className="h-7 px-2 text-[10px] uppercase"
            title="Clear the events on screen — the tail keeps running, so only newer events appear"
          >
            <Eraser className="mr-1 h-3 w-3" />
            Clear
          </Button>
          {clearedCount > 0 && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={restoreCleared}
              className="h-7 px-2 text-[10px] uppercase"
              title="Bring the cleared events back"
            >
              <Undo2 className="mr-1 h-3 w-3" />
              Show {clearedCount.toLocaleString()} earlier
            </Button>
          )}
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
          <span className="ml-1 font-mono text-[10px] text-fg-muted tabular-nums">
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
          title={emptyState.title}
          description={emptyState.description}
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
                      meta.level && logLevelRowClass[meta.level],
                    )}
                    style={{
                      transform: `translateY(${virtualRow.start}px)`,
                    }}
                  >
                    {displayMode === "table" ? (
                      <>
                        <div className="flex w-10 shrink-0 items-start justify-center pt-1.5 font-mono text-[9px] text-fg-muted/40 tabular-nums select-none">
                          {virtualRow.index + 1}
                        </div>
                        <div className="flex w-20 shrink-0 items-start px-1 pt-1.5 font-mono text-[10px] text-fg-muted tabular-nums">
                          {formatLogTime(evt.timestamp)}
                        </div>
                        {!streamName && (
                          // Truncated, not just narrow: a Lambda stream name is
                          // wider than `w-44` at this size and the column has no
                          // overflow of its own, so it used to spill across the
                          // message — over the level badge first.
                          <div
                            className="w-44 shrink-0 truncate px-1 pt-1.5 font-mono text-[10px] text-fg-muted"
                            title={evt.logStreamName}
                          >
                            {evt.logStreamName}
                          </div>
                        )}
                        <div className="min-w-0 flex-1 px-1 py-1.5">
                          <LogMessage
                            message={meta.msg}
                            summary={meta.summary}
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
                          prefix={`${formatLogTime(evt.timestamp)}${evt.logStreamName ? ` ${evt.logStreamName}` : ""}`}
                          message={meta.msg}
                          summary={meta.summary}
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
                      <CopyButton
                        value={meta.plain}
                        noun="log message"
                        tone="inline"
                        className="p-0.5 text-fg-muted/40 opacity-0 transition-opacity group-hover/row:opacity-100 hover:text-fg-muted"
                      />
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
              className="absolute right-4 bottom-4 z-10 flex items-center gap-1 rounded-full border border-border bg-bg-elevated px-3 py-1.5 font-mono text-[10px] font-medium text-fg-muted shadow-lg transition-colors hover:border-accent hover:text-accent"
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

/**
 * The chip that names a row's level.
 *
 * Every row the tint applies to gets one, whatever the message looks like. The
 * badge used to render only for a syntax-highlighted document, or for a plain
 * line once Format was ticked — so a `console.warn` from a Node runtime, whose
 * level AWS writes as a tab-separated column
 *
 *   2026-08-10T02:34:39.674Z\t<request id>\tWARN\tCannot push rates…
 *
 * arrived tinted but unlabelled, and the label was the part that read at a
 * glance. Nothing about that line is less worth labelling than a Powertools
 * document carrying the same level in a `"level"` field.
 */
function LevelBadge({ level }: { level: LogLevel }) {
  return (
    <span
      className={cn(
        "mt-0.5 shrink-0 rounded px-1 py-0.5 font-mono text-[8px] font-bold uppercase",
        logLevelBadgeClass[level],
      )}
    >
      {level}
    </span>
  )
}

function LogMessage({
  prefix,
  message,
  summary = null,
  formatted,
  syntaxHighlight,
  wrapLines,
  filterPattern,
  level,
  hideLevel = false,
}: {
  prefix?: string
  message: string
  /** A Lambda system log record's summary line, when the message is one. */
  summary?: string | null
  formatted: boolean
  syntaxHighlight: boolean
  wrapLines: boolean
  filterPattern: string
  level: LogLevel | null
  hideLevel?: boolean
}) {
  const jsonText = useMemo(() => {
    if (!formatted && !syntaxHighlight) return null
    // A colourised JSON line is still JSON; the escape sequences around it are
    // not, so they come off before the parse attempt.
    const json = tryParseJSON(stripAnsi(message))
    if (!json) return null
    return stringifyJSON(json, formatted)
  }, [formatted, message, syntaxHighlight])
  // A system log record would otherwise render as a JSON blob among the
  // function's own output, so the summary is what shows until Format is ticked
  // — which is the toggle that means "show me the document".
  const asSummary = summary != null && !formatted
  const withPrefix = (text: string) => `${prefix ? `${prefix} ` : ""}${text}`
  const displayText = asSummary
    ? withPrefix(summary)
    : formatted && jsonText
      ? jsonText
      : withPrefix(message)
  const showSyntax = !asSummary && syntaxHighlight && jsonText

  if (showSyntax) {
    return (
      <div className="flex items-start gap-1.5">
        {level && !hideLevel && <LevelBadge level={level} />}
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
      {level && !hideLevel && <LevelBadge level={level} />}
      <pre
        className={cn(
          "font-mono text-[11px] leading-relaxed text-fg",
          wrapLines ? "wrap-break-word whitespace-pre-wrap" : "whitespace-pre",
        )}
      >
        <AnsiText
          text={displayText}
          renderText={(chunk) => highlightMatches(chunk, filterPattern)}
        />
      </pre>
    </div>
  )
}
