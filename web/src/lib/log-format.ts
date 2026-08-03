/**
 * Shared vocabulary for rendering log events.
 *
 * Four surfaces show CloudWatch log events — the generic `LogViewer` (Lambda's
 * monitor tab and the map's invocations drawer), the CloudWatch Logs stream
 * viewer, the log group detail page, and the map's stream peek — and each had
 * grown its own copy of the same four helpers. Copies drift: the two level
 * detectors were identical, but the two row-tint maps had already diverged over
 * whether a debug row gets a background, and the timestamp formatter existed
 * three times under three names.
 *
 * A message goes through `stripAnsi` (see [ansi.ts](./ansi.ts)) before any of
 * these read it: a colourised line starts with an escape sequence, not with
 * `{` or `ERROR`.
 */
import Prism from "@/lib/prism"

export type LogLevel = "error" | "warn" | "info" | "debug"

/** Clock time of a log event, to the millisecond: `14:22:07.481`. */
export function formatLogTime(ts?: number | null): string {
  if (ts == null || ts === 0) return "—"
  const d = new Date(ts)
  const hh = String(d.getHours()).padStart(2, "0")
  const mm = String(d.getMinutes()).padStart(2, "0")
  const ss = String(d.getSeconds()).padStart(2, "0")
  const ms = String(d.getMilliseconds()).padStart(3, "0")
  return `${hh}:${mm}:${ss}.${ms}`
}

/**
 * Full date and time, for a log group or stream's own metadata — creation,
 * last event, retention — where the day matters and the millisecond does not.
 */
export function formatLogDate(ts?: number | null): string {
  if (!ts) return "—"
  return new Date(ts).toLocaleString()
}

/**
 * Guesses a log event's level from its text, for tinting the row.
 *
 * A structured `"level"` field wins, because a line that carries one means it.
 * Otherwise only the first 80 characters are considered: `ERROR` appearing in
 * the body of a message is usually the *word*, not the severity — a request
 * that logged "retrying after ERROR response" is not itself an error.
 */
export function detectLogLevel(msg: string): LogLevel | null {
  const levelMatch = /"level"\s*:\s*"(\w+)"/i.exec(msg)
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

/**
 * Row tint by level. Info is deliberately blank: it is the common case, and a
 * tint on every second row is noise rather than signal.
 */
export const logLevelRowClass: Record<LogLevel, string> = {
  error: "border-l-red-500/60 bg-red-500/5",
  warn: "border-l-yellow-500/60 bg-yellow-500/5",
  info: "",
  debug: "border-l-fg-muted/30 bg-fg-muted/3",
}

/** Badge tint by level, for the viewers that label the row as well as tint it. */
export const logLevelBadgeClass: Record<LogLevel, string> = {
  error: "bg-red-500/15 text-red-400",
  warn: "bg-yellow-500/15 text-yellow-400",
  info: "bg-sky-500/15 text-sky-400",
  debug: "bg-fg-muted/15 text-fg-muted",
}

/**
 * Parses a message that is entirely one JSON document, for the viewers'
 * pretty-print and syntax-highlight modes. Anything else — a plain line, a
 * prefixed line, a truncated document — is not JSON for this purpose and comes
 * back null, which the callers render as text.
 */
export function tryParseJSON(msg: string): object | null {
  const trimmed = msg.trim()
  if (!trimmed.startsWith("{") && !trimmed.startsWith("[")) return null
  try {
    return JSON.parse(trimmed) as object
  } catch {
    return null
  }
}

/** Serialises a parsed message back out, indented or on one line. */
export function stringifyJSON(obj: object, pretty: boolean): string {
  return JSON.stringify(obj, null, pretty ? 2 : 0)
}

/** PrismJS-highlighted JSON, as HTML. */
export function highlightJSON(text: string): string {
  return Prism.highlight(text, Prism.languages.json, "json")
}
