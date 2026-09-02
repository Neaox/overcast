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
 *
 * ## ResourceTable didn't fit — #1327 wave D, measured
 *
 * `ResourceTable`'s `virtualize` was built as a real candidate for this surface
 * and was measured against it rather than argued about. Both renderings were
 * compiled into one console image behind a `?tableImpl=rt` switch and driven
 * interleaved (bespoke, candidate, ×4) in one browser session against one
 * seeded data set: 10,000 events over 5 streams, filter `request`, **8,334
 * results**. Chrome 141 headless, 1440×900, both scroll panes forced to 600px
 * so rows-per-frame matched; a private browser instance, because a shared one
 * throttles a backgrounded tab's rAF to 1 Hz and destroys frame timings.
 *
 * | | bespoke | `ResourceTable virtualize` |
 * | --- | ---: | ---: |
 * | search → rows painted, median of 16 | **15.0 ms** | 18.3 ms (+22 %) |
 * | …p90 / max | 16.1 / 19.0 ms | 22.0 / 26.0 ms |
 * | scroll frame, median of 160 | **16.5 ms** | 16.8 ms |
 * | …p90 / p95 / max | 18.0 / 18.5 / 21.7 ms | 18.8 / 20.0 / 24.8 ms (+8 % p95) |
 * | frames over 16.7 ms | 72 / 160 | 82 / 160 |
 * | DOM nodes in the scroll region | **208** | 289 (+39 %) |
 * | 41,000 px of scripted scroll lands at | 41,000 px | 40,738 px |
 *
 * The regression is real but small. What decides it is the three things the
 * candidate cannot express at all:
 *
 *   1. **Level tinting.** `logLevelRowClass` paints the *row* — a red left
 *      border and a danger wash for ERROR, amber for WARN. `ResourceTable`
 *      renders every row identically and offers no per-row class hook, so an
 *      error line becomes indistinguishable from an info line. That is the
 *      affordance this surface is read by.
 *   2. **Dynamic row heights.** Rows here are measured (`measureElement`)
 *      because `wrapLines` makes a long line several lines tall.
 *      `ResourceTable` takes a single `estimateRowHeight` and never measures,
 *      so a wrapped result set gets a scrollbar that lies about its own length.
 *   3. **Scroll fidelity.** With the spacer-row technique inside a real
 *      `<table>`, a scripted 41,000 px scroll lands 262 px short and does so
 *      reproducibly — the spacer heights change under the browser's scroll
 *      anchoring. A log search is a surface people scrub through.
 *
 * Two smaller mismatches, noted for whoever revisits this: `rowKey` must return
 * a string while the log surfaces key by `stableRowKey`'s number, and
 * `TableCell`'s padding makes each row ~13 px taller, which costs two visible
 * results per screen. If `ResourceTable` ever grows a per-row class hook and
 * dynamic measurement, this is worth measuring again.
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
      <div className="flex border-b border-border bg-bg-elevated px-1 py-1.5 text-2xs font-medium text-fg-muted">
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
                <div className="w-40 shrink-0 px-2 py-1.5 font-mono text-2xs whitespace-nowrap text-fg-muted">
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
                  className="w-44 shrink-0 truncate px-1 py-1.5 font-mono text-2xs text-fg-muted"
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
