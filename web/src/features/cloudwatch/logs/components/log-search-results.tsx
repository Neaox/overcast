/**
 * Cross-stream search results for the log-group detail page.
 *
 * This was the last log surface rendering events as an unbounded `<Table>` —
 * one DOM row per result, up to a full 10,000-event FilterLogEvents page, with
 * none of the level tinting, ANSI colour or match highlighting the other
 * surfaces have. It now renders through the shared row pipeline inside its own
 * virtualized scroll region: the page keeps its layout, the results keep their
 * click-through-to-stream behaviour, and a big result set costs ~30 rows of
 * DOM instead of ten thousand.
 */

import { useMemo, useRef } from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { formatLogDate, describeLogEvent, logLevelRowClass } from "@/lib/log-format"
import { LogMessage } from "@/components/logs/log-message"
import { cn } from "@/lib/utils"
import type { FilteredLogEvent } from "@/types/logs"
import {
  compileFilterHighlighter,
  logEventKey,
  logEventSignature,
} from "@/features/cloudwatch/logs/tail"

/** Where a clicked result should take the user: this event, in its stream. */
export interface LogSearchHit {
  streamName: string
  timestamp: number
  /** Picks the event out of its millisecond once the stream view loads. */
  signature: string
}

interface Props {
  events: FilteredLogEvent[]
  /** The active filter, for marking its matches inside each message. */
  filterPattern: string
  onOpenEvent: (hit: LogSearchHit) => void
}

export function LogSearchResults({ events, filterPattern, onOpenEvent }: Props) {
  const parentRef = useRef<HTMLDivElement>(null)

  const virtualizer = useVirtualizer({
    count: events.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 32,
    overscan: 10,
    getItemKey: (index) => logEventKey(events[index]),
  })

  /** Compiled once per filter, not once per row per scroll frame. */
  const filterMatcher = useMemo(() => compileFilterHighlighter(filterPattern), [filterPattern])

  return (
    <div className="overflow-hidden rounded-md border border-border">
      <div className="flex border-b border-border bg-bg-elevated px-1 py-1.5 text-[10px] font-medium text-fg-muted">
        <div className="w-40 shrink-0 px-2">Timestamp</div>
        <div className="min-w-0 flex-1 px-1">Message</div>
        {/* On the right, as the AWS console places it: the message is what a
            search is read by, the stream is where a hit takes you. */}
        <div className="w-44 shrink-0 px-1">Stream</div>
      </div>
      <div ref={parentRef} className="max-h-[60vh] overflow-auto">
        <div
          style={{ height: `${virtualizer.getTotalSize()}px`, width: "100%", position: "relative" }}
        >
          {virtualizer.getVirtualItems().map((virtualRow) => {
            const evt = events[virtualRow.index]
            const meta = describeLogEvent(evt)
            return (
              <div
                key={virtualRow.key}
                data-index={virtualRow.index}
                ref={virtualizer.measureElement}
                className={cn(
                  "absolute top-0 left-0 flex w-full cursor-pointer border-b border-l-2 border-border-muted border-l-transparent hover:bg-fg-muted/5",
                  meta.level && logLevelRowClass[meta.level],
                )}
                style={{ transform: `translateY(${virtualRow.start}px)` }}
                onClick={() =>
                  onOpenEvent({
                    streamName: evt.logStreamName ?? "",
                    timestamp: evt.timestamp ?? 0,
                    signature: logEventSignature(evt),
                  })
                }
              >
                <div className="w-40 shrink-0 px-2 py-1.5 font-mono text-[10px] whitespace-nowrap text-fg-muted">
                  {formatLogDate(evt.timestamp)}
                </div>
                <div className="min-w-0 flex-1 px-1 py-1.5">
                  <LogMessage
                    message={evt.message ?? ""}
                    summary={meta.summary}
                    formatted={false}
                    syntaxHighlight={false}
                    wrapLines
                    filterMatcher={filterMatcher}
                    level={meta.level}
                  />
                </div>
                <div
                  className="w-44 shrink-0 truncate px-1 py-1.5 font-mono text-[10px] text-fg-muted"
                  title={evt.logStreamName}
                >
                  {evt.logStreamName}
                </div>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
