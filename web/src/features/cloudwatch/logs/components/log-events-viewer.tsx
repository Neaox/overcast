import { useState, useMemo, useRef, useCallback, useEffect, useLayoutEffect } from "react"
import { useInfiniteQuery } from "@tanstack/react-query"
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
import { logsFilterInfiniteQueryOptions } from "@/features/cloudwatch/logs/data"
import {
  TimeRangeFilter,
  type TimeRange,
} from "@/features/cloudwatch/logs/components/time-range-filter"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"
import { describeLogEvent, formatLogTime, logLevelRowClass } from "@/lib/log-format"
import { LogMessage } from "@/components/logs/log-message"
import type { FilteredLogEvent } from "@/types/logs"
import {
  compareLogEvents,
  compileFilterHighlighter,
  dropTailedDuplicates,
  logEventKey,
  mergeSortedEvents,
} from "@/features/cloudwatch/logs/tail"
import { useLogTailBuffer } from "@/features/cloudwatch/logs/use-log-tail-buffer"

// â”€â”€ Row height estimation â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

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

/** Stable empty fallback â€” a fresh `[]` would re-run every memo keyed on it. */
const NO_EVENTS: FilteredLogEvent[] = []

// â”€â”€ Main component â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

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
  // Clearing the buffer hides everything on screen without stopping the tail.
  // The live events are simply dropped; the fetched ones are still in the
  // query cache, so they are held back by timestamp instead â€” a marker that
  // survives the refetches that would otherwise put them straight back.
  const [clearedThrough, setClearedThrough] = useState<number | null>(null)

  const parentRef = useRef<HTMLDivElement>(null)
  const pinnedToLatestRef = useRef(true)
  const previousEventCountRef = useRef(0)

  // Paged: FilterLogEvents caps a page at 10,000 events, so one page read as
  // "the results" silently truncated everything past the cap. Pages advance
  // forward through the time window; newer events arrive by fetching more.
  const { data, isLoading, isFetching, refetch, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useInfiniteQuery({
      ...logsFilterInfiniteQueryOptions(groupName, {
        filterPattern: activeFilter || undefined,
        startTime: timeRange.startTime,
        endTime: timeRange.endTime,
        ...(streamName ? { logStreamNames: [streamName] } : {}),
      }),
    })

  // The buffer resets itself when the group, stream or filter changes; the
  // time range is not part of the session's identity, so a range change has to
  // reset by hand along with the cleared-through cut.
  const tail = useLogTailBuffer({
    enabled: tailMode,
    groupIdentifier: groupName,
    streamName,
    filterPattern: activeFilter,
  })
  const clearTail = tail.clear
  useEffect(() => {
    clearTail()
    setClearedThrough(null)
  }, [clearTail, groupName, streamName, activeFilter, timeRange.startTime, timeRange.endTime])

  const fetchedEvents = useMemo(
    () => (data ? data.pages.flatMap((page) => page.events) : NO_EVENTS),
    [data],
  )
  const visibleFetched = useMemo(
    () =>
      clearedThrough == null
        ? fetchedEvents
        : fetchedEvents.filter((evt) => (evt.timestamp ?? 0) > clearedThrough),
    [fetchedEvents, clearedThrough],
  )
  const clearedCount = fetchedEvents.length - visibleFetched.length

  // A refetch while tailing re-reads events the open session has already
  // pushed, so the two sources are reconciled rather than concatenated.
  // Clearing the tail buffer on every refetch would do it too, but at the cost
  // of dropping whatever arrived after the refetch's snapshot was taken.
  //
  // Reconciled against the same list it is concatenated with, so what is on
  // screen is what was counted. Clear empties the tail buffer as it hides the
  // fetched rows, so the two lists never disagree about an event it hid.
  const visibleTailed = useMemo(
    () => dropTailedDuplicates(visibleFetched, tail.events),
    [visibleFetched, tail.events],
  )

  // Ascending is the canonical order and the other direction is its mirror:
  // the fetched page sorts once per fetch, the tail buffer maintains its own
  // order, and combining them is a linear merge â€” never a fresh sort of
  // everything per batch of live events, which is what this replaced.
  const ascendingFetched = useMemo(
    () => [...visibleFetched].sort(compareLogEvents),
    [visibleFetched],
  )
  const ascending = useMemo(
    () => mergeSortedEvents(ascendingFetched, visibleTailed),
    [ascendingFetched, visibleTailed],
  )
  const events = useMemo(
    () => (sortAsc ? ascending : [...ascending].reverse()),
    [ascending, sortAsc],
  )

  // Row metadata is derived per event and cached on the event itself, so a
  // tail that has accumulated history never re-reads it: only the event that
  // just arrived is new work. See `describeLogEvent`.
  const virtualizer = useVirtualizer({
    count: events.length,
    getScrollElement: () => parentRef.current,
    estimateSize: (index) => estimateRowHeight(describeLogEvent(events[index]).plain, formatted),
    overscan: 15,
    // Keyed by event, not by index: a sort-order flip or a newly fetched page
    // shifts every index, and index keys would remount every row with it.
    getItemKey: (index) => logEventKey(events[index]),
  })

  // Fetch the following page as the user nears the edge where it will attach —
  // the bottom under Oldest-first, the top under Newest-first. The fetched
  // pages advance toward newer events either way.
  //
  // Keyed on the page token rather than on `hasNextPage`: the boolean holds
  // steady from one page to the next, and when a response lands fast enough
  // that no `isFetchingNextPage` render ever commits, an effect keyed on the
  // booleans sees nothing change and the chain stalls. The token is different
  // for every page, so each arrival re-arms the effect.
  const virtualItems = virtualizer.getVirtualItems()
  const nearNewestEdge = sortAsc
    ? (virtualItems.at(-1)?.index ?? 0) >= events.length - 25
    : (virtualItems[0]?.index ?? Number.MAX_SAFE_INTEGER) <= 25
  const nextPageToken = data?.pages.at(-1)?.nextToken
  useEffect(() => {
    if (!nextPageToken || isFetchingNextPage || !nearNewestEdge) return
    void fetchNextPage()
  }, [nextPageToken, isFetchingNextPage, nearNewestEdge, fetchNextPage])

  /** Compiled once per filter, not once per row per scroll frame. */
  const filterMatcher = useMemo(() => compileFilterHighlighter(activeFilter), [activeFilter])

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
   * clock â€” the emulator stamps the events, and its clock is the only one that
   * decides which side of the line a record falls on.
   */
  const clearBuffer = useCallback(() => {
    setClearedThrough(
      events.reduce((newest, evt) => Math.max(newest, evt.timestamp ?? 0), clearedThrough ?? 0),
    )
    clearTail()
    pinnedToLatestRef.current = true
    setShowScrollBottom(false)
  }, [events, clearedThrough, clearTail])

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
            ? "Waiting for new events â€” anything from before the clear is hidden."
            : clearedCount > 0
              ? "Turn on Tail to watch for new events, or show the earlier ones again."
              : "Turn on Tail to watch for new events.",
        }
      : activeFilter
        ? { title: "No matching events", description: "Try a different filter pattern." }
        : { title: "No log events", description: "This stream has no events yet." }

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
              // Refresh means "reload this window of logs" â€” honouring a cut
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
            // A log filter is never a credential field. Password managers scan
            // the page's inputs on every DOM change, and this view mutates a
            // lot; opting out keeps their field analysis off the hot path.
            data-1p-ignore
            data-lpignore="true"
            className="h-7 border-0 bg-transparent px-1 shadow-none focus-visible:ring-0"
            placeholder='Filter â€” e.g. ERROR, "request failed", ERROR timeout'
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
              {events.length}
              {hasNextPage ? "+" : ""} result{events.length !== 1 || hasNextPage ? "s" : ""}
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
            title="Clear the events on screen â€” the tail keeps running, so only newer events appear"
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
          <span
            className="ml-1 font-mono text-[10px] text-fg-muted tabular-nums"
            // "+" while a nextToken remains: the server caps a page at 10,000
            // events, and a bare count would read as the whole result set.
            title={
              hasNextPage
                ? "More events are available — scroll to the newest to load them"
                : undefined
            }
          >
            {events.length.toLocaleString()}
            {hasNextPage ? "+" : ""} event{events.length !== 1 || hasNextPage ? "s" : ""}
          </span>
          {tail.overflowed > 0 && (
            // The AWS console shows "% displayed" when Live Tail samples; the
            // same honesty here â€” a capped buffer must say it dropped, not
            // quietly show a window and let it read as everything.
            <span
              className="font-mono text-[10px] text-yellow-600 tabular-nums dark:text-yellow-400"
              title="The live tail buffer is full, so the oldest events were dropped from view. Narrow the filter to keep up with the stream."
            >
              {tail.overflowed.toLocaleString()} dropped
            </span>
          )}
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
                const meta = describeLogEvent(evt)
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
                          // message â€” over the level badge first.
                          <div
                            className="w-44 shrink-0 truncate px-1 pt-1.5 font-mono text-[10px] text-fg-muted"
                            title={evt.logStreamName}
                          >
                            {evt.logStreamName}
                          </div>
                        )}
                        <div className="min-w-0 flex-1 px-1 py-1.5">
                          <LogMessage
                            message={evt.message ?? ""}
                            summary={meta.summary}
                            formatted={formatted}
                            syntaxHighlight={syntaxHighlight}
                            wrapLines={wrapLines}
                            filterMatcher={filterMatcher}
                            level={meta.level}
                          />
                        </div>
                      </>
                    ) : (
                      <div className="min-w-0 flex-1 px-2 py-1.5">
                        <LogMessage
                          prefix={`${formatLogTime(evt.timestamp)}${evt.logStreamName ? ` ${evt.logStreamName}` : ""}`}
                          message={evt.message ?? ""}
                          summary={meta.summary}
                          formatted={formatted}
                          syntaxHighlight={syntaxHighlight}
                          wrapLines={wrapLines}
                          filterMatcher={filterMatcher}
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

// â”€â”€ Log message cell â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// `LogMessage` and `LevelBadge` live in @/components/logs/log-message — the
// same pipeline renders the generic LogViewer's rows and the log-group search
// results, and copies of it had already started to drift once before.
