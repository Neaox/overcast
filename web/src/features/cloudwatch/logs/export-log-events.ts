/**
 * Builders for exporting loaded log events, mirroring the AWS console's
 * search-results CSV shape: `timestamp,logStreamName,message`, timestamps in
 * ISO 8601, the message as the raw stored bytes — never the ANSI-stripped or
 * summarised rendering. Exporting must not lose what the stream holds.
 *
 * Pure functions over the event list the viewer displays, in display order:
 * one string build for the whole file, which at the 10k-events-per-page scale
 * is a single join, not a hot path.
 */
import type { FilteredLogEvent } from "@/types/logs"

/** RFC 4180: quote a field carrying a quote, comma, CR or LF; double quotes. */
function csvField(value: string): string {
  return /[",\r\n]/.test(value) ? `"${value.replaceAll('"', '""')}"` : value
}

export function buildLogEventsCsv(events: readonly FilteredLogEvent[]): string {
  const lines = ["timestamp,logStreamName,message"]
  for (const evt of events) {
    lines.push(
      [
        // Empty rather than an invented 1970 epoch for a missing timestamp.
        evt.timestamp ? new Date(evt.timestamp).toISOString() : "",
        csvField(evt.logStreamName ?? ""),
        csvField(evt.message ?? ""),
      ].join(","),
    )
  }
  return lines.join("\r\n") + "\r\n"
}

export function buildLogEventsJson(events: readonly FilteredLogEvent[]): string {
  return JSON.stringify(
    events.map((evt) => ({
      timestamp: evt.timestamp,
      ingestionTime: evt.ingestionTime,
      logStreamName: evt.logStreamName,
      message: evt.message,
    })),
    null,
    2,
  )
}
