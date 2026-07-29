/**
 * event-stream.protocol — message types shared between the SharedWorker
 * and tab-side client.
 *
 * Kept in a separate module so both sides import the same types without
 * pulling worker globals into the main bundle or vice-versa.
 */

import type { StreamEvent } from "@/types"

// ─── Connection state ──────────────────────────────────────────────────────

/**
 * Everything the UI needs to describe the connection, including *when* the
 * next reconnect is due.
 *
 * The schedule is part of the wire contract rather than something each tab
 * infers, because the reconnect is driven from one place — the SharedWorker,
 * shared by every open tab — and a countdown a tab guessed at would drift
 * from the attempt it claims to be counting down to.
 *
 * Timestamps are epoch milliseconds from the same `Date.now()` clock on both
 * sides: a SharedWorker and its tabs are the same machine, same origin.
 */
export interface ConnectionState {
  connected: boolean
  /** Reconnect attempts since the stream last dropped; 0 while connected. */
  attempt: number
  /** When the pending attempt is due; `null` while one is already in flight. */
  nextAttemptAt: number | null
  /** When the stream was last open; `null` if it never has been. */
  lastConnectedAt: number | null
}

/** The state of a stream that has not connected yet. */
export const DISCONNECTED: ConnectionState = {
  connected: false,
  attempt: 0,
  nextAttemptAt: null,
  lastConnectedAt: null,
}

// ─── Tab → Worker ──────────────────────────────────────────────────────────

export type TabMessage =
  | { type: "subscribe"; url: string }
  | { type: "clear" }
  /** Pre-empts the scheduled retry — the "retry now" affordance. */
  | { type: "reconnect" }

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
  | { type: "init"; events: StreamEvent[]; state: ConnectionState }
  | { type: "events"; events: StreamEvent[] }
  | { type: "status"; state: ConnectionState }
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
