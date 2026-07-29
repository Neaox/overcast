/**
 * event-stream.protocol — message types shared between the SharedWorker
 * and tab-side client.
 *
 * Kept in a separate module so both sides import the same types without
 * pulling worker globals into the main bundle or vice-versa.
 */

import type { StreamEvent } from "@/types"

// ─── Tab → Worker ──────────────────────────────────────────────────────────

export type TabMessage = { type: "subscribe"; url: string } | { type: "clear" }

// ─── Worker → Tab ──────────────────────────────────────────────────────────

/**
 * Events are delivered in batches rather than one message per SSE frame.
 * The server replays its whole history buffer on connect — up to
 * internal/events.HistoryCapacity events — and a message per frame would
 * mean that many postMessages, cache writes and query-invalidation passes
 * for a single page load. One batch per flush window keeps a fresh load
 * proportional to the number of flushes instead.
 */
export type WorkerMessage =
  | { type: "init"; events: StreamEvent[]; connected: boolean }
  | { type: "events"; events: StreamEvent[] }
  | { type: "status"; connected: boolean }
  | { type: "cleared" }

// ─── Resuming ──────────────────────────────────────────────────────────────

/**
 * Adds a resume point to the stream URL, so the server replays only what
 * this client is missing instead of its whole history buffer.
 *
 * A browser sends the last id it saw as the `Last-Event-ID` header on the
 * reconnects it manages itself — but only for *that* EventSource. When the
 * connection fails hard (`readyState === CLOSED`) both the worker and the
 * fallback construct a brand-new EventSource, which starts with no memory of
 * the id, and there is no API for setting a request header on one. The
 * server therefore accepts the same value as a query parameter; see the
 * Last-Event-ID handling in internal/router/events.go.
 *
 * Returns `url` unchanged when there is nothing to resume from.
 */
export function withResumePoint(url: string, lastEventId: string | null): string {
  if (!lastEventId) return url
  const resolved = new URL(url, self.location.href)
  resolved.searchParams.set("last_event_id", lastEventId)
  return resolved.pathname + resolved.search
}
