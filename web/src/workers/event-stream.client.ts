/**
 * event-stream.client — tab-side singleton that communicates with the
 * SharedWorker (or falls back to a direct EventSource when SharedWorker
 * is unavailable).
 *
 * The public API is intentionally minimal:
 *
 *   client.subscribe(url, listener)  → unsubscribe fn
 *   client.reconnect()
 *   client.clear()
 *
 * React hooks wrap this so components never interact with it directly.
 */

import type { TabMessage, WorkerMessage, ConnectionState } from "./event-stream.protocol"
import { DISCONNECTED } from "./event-stream.protocol"
import { ReconnectingStream } from "./event-stream.connection"
import { EventBatcher } from "./event-buffer"

// ─── Listener type ─────────────────────────────────────────────────────────

export type EventStreamListener = (msg: WorkerMessage) => void

// ─── Singleton state ───────────────────────────────────────────────────────

let worker: SharedWorker | null = null
let port: MessagePort | null = null
let currentUrl: string | null = null
const listeners = new Set<EventStreamListener>()

// Fallback state (when SharedWorker is unavailable).
let fallback: ReconnectingStream | null = null
let fallbackState: ConnectionState = DISCONNECTED

const fallbackBuffer = new EventBatcher((events) => dispatch({ type: "events", events }))

// ─── Shared dispatch ───────────────────────────────────────────────────────

function dispatch(msg: WorkerMessage): void {
  for (const listener of listeners) {
    listener(msg)
  }
}

// ─── SharedWorker path ─────────────────────────────────────────────────────

function ensureWorker(): MessagePort {
  if (!worker) {
    worker = new SharedWorker(new URL("./event-stream.worker.ts", import.meta.url), {
      type: "module",
      name: "event-stream",
    })
    port = worker.port
    port.addEventListener("message", (e: MessageEvent<WorkerMessage>) => {
      dispatch(e.data)
    })
    port.start()
  }
  return port!
}

function sendToWorker(msg: TabMessage): void {
  ensureWorker().postMessage(msg)
}

// ─── Direct EventSource fallback ───────────────────────────────────────────

function openFallback(url: string): void {
  fallback?.close()
  fallbackState = DISCONNECTED

  // Send init with whatever we have cached, before the connection reports.
  dispatch({ type: "init", events: fallbackBuffer.snapshot(), state: fallbackState })

  fallback = new ReconnectingStream({
    url,
    onEvent: (event) => fallbackBuffer.push(event),
    onState: (state) => {
      fallbackState = state
      dispatch({ type: "status", state })
    },
  })
}

// ─── Public API ────────────────────────────────────────────────────────────

const supportsSharedWorker = typeof SharedWorker !== "undefined"

/**
 * Subscribe to the event stream at `url`. Returns an unsubscribe function.
 *
 * The listener receives WorkerMessage objects:
 *  - `init`    — cached events + current connection state (on first subscribe)
 *  - `events`  — a batch of new events
 *  - `status`  — the connection state changed
 *  - `cleared` — the event cache was wiped
 *
 * Calling subscribe again with a different `url` will reconnect the stream.
 * Multiple listeners can be active simultaneously (one per hook instance).
 */
export function subscribe(url: string, listener: EventStreamListener): () => void {
  listeners.add(listener)

  const urlChanged = url !== currentUrl
  currentUrl = url

  if (supportsSharedWorker) {
    // Always send subscribe so the worker replies with init for this port.
    // The worker only reconnects when the URL actually changes.
    sendToWorker({ type: "subscribe", url })
  } else {
    if (urlChanged || !fallback) {
      console.info("[event-stream] no SharedWorker — using a direct EventSource")
      openFallback(url)
    } else {
      // Same URL, just send the cached snapshot to the new listener.
      listener({ type: "init", events: fallbackBuffer.snapshot(), state: fallbackState })
    }
  }

  return () => {
    listeners.delete(listener)
  }
}

/**
 * Brings the scheduled reconnect forward to now — the "retry now" affordance
 * on the reconnecting toast. A no-op while an attempt is already in flight.
 */
export function reconnect(): void {
  if (supportsSharedWorker) {
    sendToWorker({ type: "reconnect" })
  } else {
    fallback?.retryNow()
  }
}

/** Clear the event cache across all tabs. */
export function clear(): void {
  if (supportsSharedWorker) {
    sendToWorker({ type: "clear" })
  } else {
    fallbackBuffer.clear()
    dispatch({ type: "cleared" })
  }
}
