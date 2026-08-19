/**
 * The one way a log line's message renders.
 *
 * Every surface that shows CloudWatch log events — the stream viewer, the
 * generic `LogViewer`, the log-group search results — needs the same pipeline:
 * ANSI colour, the level badge, a Lambda system log record's summary line, the
 * pretty/highlighted JSON modes, and filter-match marks. Each had grown its own
 * partial copy, and copies drift ([log-format.ts](../../lib/log-format.ts)
 * tells that story for the helpers); this is the component-level counterpart.
 */

import { memo, useMemo } from "react"
import { cn } from "@/lib/utils"
import { stripAnsi } from "@/lib/ansi"
import { useScrollSettled } from "@/hooks/use-scroll-settled"
import {
  highlightJSON,
  logLevelBadgeClass,
  stringifyJSON,
  tryParseJSON,
  type LogLevel,
} from "@/lib/log-format"
import { AnsiText } from "./ansi-text"

/**
 * Wrap classes for a message `<pre>`, which is a flex item.
 *
 * The `min-w-0` is the wrap mode's load-bearing half: flex items default to
 * `min-width: auto`, and `overflow-wrap: break-word` does not lower a line's
 * min-content width — so a single-token error line (a Lambda "Uncaught error"
 * dump with no whitespace) forced the row, and with it the whole scroller,
 * to the token's full width: a ~42,000px horizontal scrollbar over a 2,700px
 * viewport, with the header row (outside the scroller, deliberately) refusing
 * to follow. Letting the item shrink is what gives break-word something to
 * break against. No-wrap mode keeps `min-width: auto` on purpose — there the
 * wide scroller IS the feature.
 */
function messageWrapClass(wrapLines: boolean): string {
  return wrapLines ? "min-w-0 wrap-break-word whitespace-pre-wrap" : "whitespace-pre"
}

/** Highlight a filter's matches in a message string, using a pre-compiled matcher. */
function highlightMatches(message: string, matcher: RegExp): React.ReactNode {
  const parts = message.split(matcher)
  if (parts.length === 1) return message
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
export function LevelBadge({ level }: { level: LogLevel }) {
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

/**
 * Memoised on purpose: the virtualizer flush-syncs a render on every scroll
 * event, so without this every toolbar keystroke and every scroll tick re-ran
 * the whole message pipeline for every visible row. Each prop is a primitive or
 * a value the caller holds stable across renders (`filterMatcher` is compiled
 * per filter), so a row that has not changed re-renders to the identical output
 * and touches no DOM.
 */
export const LogMessage = memo(function LogMessage({
  prefix,
  message,
  summary = null,
  formatted,
  syntaxHighlight,
  wrapLines,
  filterMatcher,
  level,
  hideLevel = false,
  collapsed = false,
  scrolling = false,
  sizeClassName = "text-[11px]",
}: {
  prefix?: string
  message: string
  /** A Lambda system log record's summary line, when the message is one. */
  summary?: string | null
  formatted: boolean
  syntaxHighlight: boolean
  wrapLines: boolean
  /** Pre-compiled filter matcher, or null when nothing is filtered. */
  filterMatcher: RegExp | null
  level: LogLevel | null
  hideLevel?: boolean
  /**
   * Render as one truncated line — the AWS console's collapsed row. The badge
   * stays, ANSI still colours the line, a platform record still shows its
   * summary; Format/Syntax belong to the expanded rendering and are skipped
   * (along with their JSON parse — collapsed rows are the cheap ones).
   */
  collapsed?: boolean
  /**
   * The owning virtualizer is mid-scroll. A row mounted now defers its
   * syntax-highlight markup — hundreds of spans per JSON document — and
   * renders the same text plain until the scroll settles; the text is
   * identical, so the swap changes colour, never layout. See
   * `useScrollSettled` for why.
   */
  scrolling?: boolean
  /** Font-size class; the surfaces render at different densities. */
  sizeClassName?: string
}) {
  const settled = useScrollSettled(scrolling)
  const jsonText = useMemo(() => {
    if (collapsed) return null
    if (!formatted && !syntaxHighlight) return null
    // A colourised JSON line is still JSON; the escape sequences around it are
    // not, so they come off before the parse attempt.
    const json = tryParseJSON(stripAnsi(message))
    if (!json) return null
    return stringifyJSON(json, formatted)
  }, [collapsed, formatted, message, syntaxHighlight])
  // A system log record would otherwise render as a JSON blob among the
  // function's own output, so the summary is what shows until Format is ticked
  // — which is the toggle that means "show me the document".
  const asSummary = summary != null && (!formatted || collapsed)
  const withPrefix = (text: string) => `${prefix ? `${prefix} ` : ""}${text}`
  const displayText = asSummary
    ? withPrefix(summary)
    : formatted && jsonText
      ? jsonText
      : withPrefix(message)
  const showSyntax = !asSummary && syntaxHighlight && jsonText

  if (collapsed) {
    // `truncate` is what enforces the single line: nowrap turns any embedded
    // newline into a space and the overflow into an ellipsis.
    return (
      <div className="flex min-w-0 items-center gap-1.5">
        {level && !hideLevel && <LevelBadge level={level} />}
        <div className={cn("min-w-0 flex-1 truncate font-mono leading-relaxed", sizeClassName)}>
          <AnsiText
            text={displayText}
            renderText={
              filterMatcher ? (chunk) => highlightMatches(chunk, filterMatcher) : undefined
            }
          />
        </div>
      </div>
    )
  }

  if (showSyntax) {
    return (
      <div className="flex items-start gap-1.5">
        {level && !hideLevel && <LevelBadge level={level} />}
        {prefix && !formatted && (
          <span
            className={cn(
              "shrink-0 pt-0.5 font-mono leading-relaxed text-fg-muted tabular-nums",
              sizeClassName,
            )}
          >
            {prefix}
          </span>
        )}
        {settled ? (
          <pre
            className={cn("font-mono leading-relaxed", sizeClassName, messageWrapClass(wrapLines))}
            dangerouslySetInnerHTML={{ __html: highlightJSON(jsonText) }}
          />
        ) : (
          // Same element, same classes, same text — the un-highlighted stand-in
          // occupies exactly the pixels the highlighted version will, so the
          // hydration swap never moves a measured row.
          <pre className={cn("font-mono leading-relaxed", sizeClassName, messageWrapClass(wrapLines))}>
            {jsonText}
          </pre>
        )}
      </div>
    )
  }

  // Plain message — with optional filter highlighting
  return (
    <div className="flex items-start gap-1.5">
      {level && !hideLevel && <LevelBadge level={level} />}
      <pre
        className={cn("font-mono leading-relaxed text-fg", sizeClassName, messageWrapClass(wrapLines))}
      >
        <AnsiText
          text={displayText}
          renderText={filterMatcher ? (chunk) => highlightMatches(chunk, filterMatcher) : undefined}
        />
      </pre>
    </div>
  )
})
