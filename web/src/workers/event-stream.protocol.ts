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

export type WorkerMessage =
  | { type: "init"; events: StreamEvent[]; connected: boolean }
  | { type: "event"; event: StreamEvent }
  | { type: "status"; connected: boolean }
  | { type: "cleared" }
