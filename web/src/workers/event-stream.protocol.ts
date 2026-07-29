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
