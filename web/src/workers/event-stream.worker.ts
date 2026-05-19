/**
 * event-stream.worker — SharedWorker that owns the single EventSource
 * connection to the emulator's SSE endpoint.
 *
 * Responsibilities:
 *  1. Maintain one EventSource with automatic reconnect (exponential backoff).
 *  2. Cache the most recent MAX_EVENTS events so newly-connected tabs get
 *     immediate history without waiting for fresh events.
 *  3. Broadcast every new event and status change to all connected tabs.
 *
 * Tabs communicate via MessagePort (SharedWorker.port):
 *  - Tab sends { type: "subscribe", url } to start/change the SSE URL.
 *  - Tab sends { type: "clear" } to wipe the event cache.
 *  - Worker replies with { type: "init", events, connected } on subscribe.
 *  - Worker broadcasts { type: "event", event } for each new SSE frame.
 *  - Worker broadcasts { type: "status", connected } on connection changes.
 *  - Worker broadcasts { type: "cleared" } after the cache is wiped.
 */
/// <reference lib="webworker" />

declare let self: SharedWorkerGlobalScope

import type { StreamEvent } from "@/types"
import type { TabMessage, WorkerMessage } from "./event-stream.protocol"

const MAX_EVENTS = 1_000

// ─── State ─────────────────────────────────────────────────────────────────

let events: StreamEvent[] = []
let connected = false
let currentUrl: string | null = null
let eventSource: EventSource | null = null
let retryCount = 0
let retryHandle: ReturnType<typeof setTimeout> | null = null

const ports = new Set<MessagePort>()

// ─── Broadcasting ──────────────────────────────────────────────────────────

function broadcast(msg: WorkerMessage): void {
  for (const port of ports) {
    port.postMessage(msg)
  }
}

// ─── EventSource lifecycle ─────────────────────────────────────────────────

function openConnection(url: string): void {
  // Tear down any existing connection + pending retry.
  eventSource?.close()
  if (retryHandle !== null) {
    clearTimeout(retryHandle)
    retryHandle = null
  }

  currentUrl = url
  retryCount = 0

  function attempt(): void {
    const es = new EventSource(url)
    eventSource = es

    es.onopen = () => {
      if (retryCount > 0) {
        console.info(`[event-stream] reconnected after ${retryCount} retries`)
      } else {
        console.info("[event-stream] connected")
      }
      retryCount = 0
      connected = true
      broadcast({ type: "status", connected: true })
    }

    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) {
        // Permanent close — retry with backoff.
        connected = false
        broadcast({ type: "status", connected: false })
        const delay = Math.min(1_000 * 2 ** retryCount, 5_000)
        retryCount++
        console.warn(
          `[event-stream] connection closed, retrying in ${delay}ms (attempt ${retryCount})`,
        )
        retryHandle = setTimeout(attempt, delay)
      } else {
        // Transient error (readyState === CONNECTING) — browser retries
        // automatically, but surface "disconnected" to the UI.
        if (connected) {
          console.warn("[event-stream] connection lost, browser is reconnecting")
          connected = false
          broadcast({ type: "status", connected: false })
        }
      }
    }

    es.onmessage = (e: MessageEvent<string>) => {
      if (!e.data) return
      try {
        const evt = JSON.parse(e.data) as StreamEvent

        // Heartbeats are not cached (they are ephemeral) but are still
        // forwarded so the UI can optionally display them.
        if (evt.type === "heartbeat") {
          broadcast({ type: "event", event: evt })
          return
        }

        // Append, sort, and cap the cache.
        events.push(evt)
        events.sort((a, b) => (a.time < b.time ? -1 : a.time > b.time ? 1 : 0))
        if (events.length > MAX_EVENTS) {
          events = events.slice(events.length - MAX_EVENTS)
        }

        broadcast({ type: "event", event: evt })
      } catch {
        console.error("[event-stream] malformed SSE frame", e.data)
      }
    }
  }

  attempt()
}

// ─── Port management ───────────────────────────────────────────────────────

self.addEventListener("connect", (e: MessageEvent) => {
  const port: MessagePort = e.ports[0]
  ports.add(port)

  port.addEventListener("message", (msg: MessageEvent<TabMessage>) => {
    switch (msg.data.type) {
      case "subscribe":
        // Reconnect only when the URL actually changes.
        if (msg.data.url !== currentUrl) {
          openConnection(msg.data.url)
        }
        // Always send the current snapshot to the newly-subscribing tab.
        port.postMessage({
          type: "init",
          events: [...events],
          connected,
        } satisfies WorkerMessage)
        break

      case "clear":
        events = []
        broadcast({ type: "cleared" })
        break
    }
  })

  port.start()
})
