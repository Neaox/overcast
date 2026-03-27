/**
 * useEventStream — generic SSE hook that tails the emulator's /_events stream.
 *
 * Because EventSource does not support custom request headers, the emulator
 * endpoint and region are passed as query parameters ("ep" and "region").
 * The BFF events route accepts both headers and query params.
 *
 * The stream is page-load scoped:
 *   - No history is replayed — only events received since mount are kept.
 *   - The last MAX_EVENTS events are kept in memory; older ones are discarded,
 *     so the page can be left open indefinitely without leaking memory.
 *
 * Generic — works for any source (s3, sqs, dynamodb, …). Callers filter by
 * passing source strings; omit to receive all events.
 *
 * Usage:
 *   const { events, connected, clear } = useEventStream({ source: "s3" })
 *   const { events, connected, clear } = useEventStream({ source: ["s3", "sqs"] })
 *   const { events, connected, clear } = useEventStream() // all sources
 */
import { useState, useEffect, useRef, useCallback } from "react"
import { useEndpoint } from "@/hooks/use-endpoint"
import type { StreamEvent } from "@/services/api"

export type { StreamEvent }

const MAX_EVENTS = 5_000

export interface UseEventStreamOptions {
  /** Filter to one or more sources. Omit to receive all events. */
  source?: string | string[]
  /** Set to false to pause the connection without unmounting. */
  enabled?: boolean
}

export interface UseEventStreamResult {
  events: StreamEvent[]
  connected: boolean
  clear: () => void
}

export function useEventStream(opts: UseEventStreamOptions = {}): UseEventStreamResult {
  const { endpoint } = useEndpoint()
  const [events, setEvents] = useState<StreamEvent[]>([])
  const [connected, setConnected] = useState(false)
  // Track the EventSource instance so we can close it in cleanup.
  const esRef = useRef<EventSource | null>(null)

  const clear = useCallback(() => setEvents([]), [])

  useEffect(() => {
    if (opts.enabled === false) return

    // Build the URL. EventSource uses GET with no custom headers, so the
    // emulator endpoint is passed as query params for the BFF to pick up.
    const params = new URLSearchParams()
    params.set("ep", endpoint.baseUrl)
    params.set("region", endpoint.region)

    const sources = Array.isArray(opts.source)
      ? opts.source
      : opts.source != null
        ? [opts.source]
        : []
    for (const s of sources) params.append("source", s)

    const url = `/api/events?${params.toString()}`
    const es = new EventSource(url)
    esRef.current = es

    es.onopen = () => setConnected(true)
    es.onerror = () => setConnected(false)

    es.onmessage = (e: MessageEvent<string>) => {
      // SSE comment frames (": connected") have no data – ignore.
      if (!e.data) return
      try {
        const evt = JSON.parse(e.data) as StreamEvent
        setEvents((prev) => {
          const next = [...prev, evt]
          // Cap to MAX_EVENTS to keep memory bounded regardless of uptime.
          return next.length > MAX_EVENTS ? next.slice(next.length - MAX_EVENTS) : next
        })
      } catch {
        // Malformed frame — ignore silently.
      }
    }

    return () => {
      es.close()
      esRef.current = null
      setConnected(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [endpoint.baseUrl, endpoint.region, JSON.stringify(sources_key(opts.source)), opts.enabled])

  return { events, connected, clear }
}

/** Stable serialisation of source option for use as a dependency value. */
function sources_key(source: string | string[] | undefined): string {
  if (!source) return ""
  return Array.isArray(source) ? [...source].sort().join(",") : source
}
