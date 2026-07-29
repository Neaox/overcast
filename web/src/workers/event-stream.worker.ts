/**
 * event-stream.worker — SharedWorker that owns the single EventSource
 * connection to the emulator's SSE endpoint.
 *
 * Responsibilities:
 *  1. Maintain one connection, reconnecting on the schedule owned by
 *     ReconnectingStream (see event-stream.connection.ts).
 *  2. Cache recent events (see event-buffer.ts for capacity and eviction) so
 *     newly-connected tabs get immediate history without waiting for fresh
 *     events — including after a reload, when the worker outlives the page.
 *  3. Broadcast new events, in batches, and connection state to all tabs.
 *
 * The connection is opened as soon as any tab subscribes — which the app
 * shell does on load — so events are captured in the background whether or
 * not the Events page has ever been opened.
 *
 * Tabs communicate via MessagePort (SharedWorker.port):
 *  - Tab sends { type: "subscribe", url } to start/change the SSE URL.
 *  - Tab sends { type: "clear" } to wipe the event cache.
 *  - Tab sends { type: "reconnect" } to pre-empt the scheduled retry.
 *  - Worker replies with { type: "init", events, state } on subscribe.
 *  - Worker broadcasts { type: "events", events } for each batch of frames.
 *  - Worker broadcasts { type: "status", state } on connection changes.
 *  - Worker broadcasts { type: "cleared" } after the cache is wiped.
 */
/// <reference lib="webworker" />

declare let self: SharedWorkerGlobalScope

import type { TabMessage, WorkerMessage, ConnectionState } from "./event-stream.protocol"
import { DISCONNECTED } from "./event-stream.protocol"
import { ReconnectingStream } from "./event-stream.connection"
import { EventBatcher } from "./event-buffer"

// ─── State ─────────────────────────────────────────────────────────────────

let currentUrl: string | null = null
let stream: ReconnectingStream | null = null
let state: ConnectionState = DISCONNECTED

const ports = new Set<MessagePort>()

const buffer = new EventBatcher((events) => broadcast({ type: "events", events }))

// ─── Broadcasting ──────────────────────────────────────────────────────────

function broadcast(msg: WorkerMessage): void {
  for (const port of ports) {
    port.postMessage(msg)
  }
}

// ─── Connection lifecycle ──────────────────────────────────────────────────

function openConnection(url: string): void {
  stream?.close()
  currentUrl = url
  state = DISCONNECTED

  stream = new ReconnectingStream({
    url,
    onEvent: (event) => buffer.push(event),
    onState: (next) => {
      state = next
      broadcast({ type: "status", state: next })
    },
  })
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
        // Anything already queued for the next flush is deliberately not in
        // it — the tab receives those in the batch instead, so nothing is
        // delivered twice or missed at the subscription boundary.
        port.postMessage({
          type: "init",
          events: buffer.snapshot(),
          state,
        } satisfies WorkerMessage)
        break

      case "reconnect":
        stream?.retryNow()
        break

      case "clear":
        buffer.clear()
        broadcast({ type: "cleared" })
        break
    }
  })

  port.start()
})
