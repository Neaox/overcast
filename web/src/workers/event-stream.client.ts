/**
 * event-stream.client — tab-side singleton that communicates with the
 * SharedWorker (or falls back to a direct EventSource when SharedWorker
 * is unavailable).
 *
 * The public API is intentionally minimal:
 *
 *   client.subscribe(url, listener)  → unsubscribe fn
 *   client.clear()
 *
 * React hooks wrap this so components never interact with it directly.
 */

import type { StreamEvent } from "@/types"
import type { TabMessage, WorkerMessage } from "./event-stream.protocol"

// ─── Listener type ─────────────────────────────────────────────────────────

export type EventStreamListener = (msg: WorkerMessage) => void

// ─── Singleton state ───────────────────────────────────────────────────────

let worker: SharedWorker | null = null
let port: MessagePort | null = null
let currentUrl: string | null = null
const listeners = new Set<EventStreamListener>()

// Fallback state (when SharedWorker is unavailable).
let fallbackES: EventSource | null = null
let fallbackRetryCount = 0
let fallbackRetryHandle: ReturnType<typeof setTimeout> | null = null
let fallbackEvents: StreamEvent[] = []
let fallbackConnected = false

const MAX_EVENTS = 1_000

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
  fallbackES?.close()
  if (fallbackRetryHandle !== null) {
    clearTimeout(fallbackRetryHandle)
    fallbackRetryHandle = null
  }
  fallbackRetryCount = 0

  function attempt(): void {
    const es = new EventSource(url)
    fallbackES = es

    es.onopen = () => {
      if (fallbackRetryCount > 0) {
        console.info(`[event-stream] reconnected after ${fallbackRetryCount} retries`)
      } else {
        console.info("[event-stream] connected (fallback)")
      }
      fallbackRetryCount = 0
      fallbackConnected = true
      dispatch({ type: "status", connected: true })
    }

    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) {
        fallbackConnected = false
        dispatch({ type: "status", connected: false })
        const delay = Math.min(1_000 * 2 ** fallbackRetryCount, 5_000)
        fallbackRetryCount++
        console.warn(
          `[event-stream] connection closed, retrying in ${delay}ms (attempt ${fallbackRetryCount})`,
        )
        fallbackRetryHandle = setTimeout(attempt, delay)
      } else if (fallbackConnected) {
        console.warn("[event-stream] connection lost, browser is reconnecting")
        fallbackConnected = false
        dispatch({ type: "status", connected: false })
      }
    }

    es.onmessage = (e: MessageEvent<string>) => {
      if (!e.data) return
      try {
        const evt = JSON.parse(e.data) as StreamEvent

        // Heartbeats are not cached but forwarded so the UI can optionally display them.
        if (evt.type === "heartbeat") {
          dispatch({ type: "event", event: evt })
          return
        }

        fallbackEvents.push(evt)
        fallbackEvents.sort((a, b) => (a.time < b.time ? -1 : a.time > b.time ? 1 : 0))
        if (fallbackEvents.length > MAX_EVENTS) {
          fallbackEvents = fallbackEvents.slice(fallbackEvents.length - MAX_EVENTS)
        }
        dispatch({ type: "event", event: evt })
      } catch {
        console.error("[event-stream] malformed SSE frame", e.data)
      }
    }
  }

  // Send init with whatever we have cached.
  dispatch({ type: "init", events: [...fallbackEvents], connected: fallbackConnected })
  attempt()
}

// ─── Public API ────────────────────────────────────────────────────────────

const supportsSharedWorker = typeof SharedWorker !== "undefined"

/**
 * Subscribe to the event stream at `url`. Returns an unsubscribe function.
 *
 * The listener receives WorkerMessage objects:
 *  - `init`    — cached events + current connection status (on first subscribe)
 *  - `event`   — a single new event
 *  - `status`  — connection status changed
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
    if (urlChanged || !fallbackES) {
      openFallback(url)
    } else {
      // Same URL, just send the cached snapshot to the new listener.
      listener({ type: "init", events: [...fallbackEvents], connected: fallbackConnected })
    }
  }

  return () => {
    listeners.delete(listener)
  }
}

/** Clear the event cache across all tabs. */
export function clear(): void {
  if (supportsSharedWorker) {
    sendToWorker({ type: "clear" })
  } else {
    fallbackEvents = []
    dispatch({ type: "cleared" })
  }
}
