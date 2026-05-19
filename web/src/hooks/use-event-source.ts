import { useEffect, useRef } from "react"
import { usePageUnloading } from "@/hooks/use-page-unloading"

// ─── HMR safety net ───────────────────────────────────────────────────────
// Track the active EventSource at module scope so we can close it when Vite
// hot-replaces this module. Without this, Firefox's 6-connection HTTP/1.1
// limit can be exhausted by leaked SSE connections that React's useEffect
// cleanup didn't close (e.g. when HMR replaces a parent module without
// triggering a full unmount cycle).
let activeES: EventSource | null = null

if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    activeES?.close()
    activeES = null
  })
}

export interface UseEventSourceOptions {
  /** URL to open. Pass `null` to disable (no connection is opened). */
  url: string | null
  /** Fired when the connection is established. */
  onOpen?: () => void
  /**
   * Fired on a transient error (`readyState` is `CONNECTING`) — the browser
   * will automatically retry. Not called during page unload.
   */
  onError?: (event: Event) => void
  /**
   * Fired when the connection is permanently closed (`readyState` is `CLOSED`)
   * — e.g. the server returned a non-200 status or wrong content-type.
   * Also called during page unload with `isPageUnloading = true` so callers
   * can do cleanup; use the flag to suppress any UI state changes.
   */
  onClose?: (event: Event, isPageUnloading: boolean) => void
  onMessage?: (event: MessageEvent<string>) => void
}

/**
 * Opens a single EventSource for `url` and wires up the provided handlers.
 * Tears down and reopens whenever `url` changes. Suppresses the `onError`
 * callback during page unload so callers don't flash a disconnected state
 * on reload or tab close.
 *
 * When the connection is permanently closed (non-2xx or wrong content-type),
 * the hook automatically retries with exponential backoff (2s→4s→8s…→30s max)
 * so that the UI reconnects once the emulator comes back up.
 */
export function useEventSource({
  url,
  onOpen,
  onError,
  onClose,
  onMessage,
}: UseEventSourceOptions): void {
  const isPageUnloading = usePageUnloading()

  // Stable refs so handler identity changes don't cause reconnects.
  const onOpenRef = useRef(onOpen)
  const onErrorRef = useRef(onError)
  const onCloseRef = useRef(onClose)
  const onMessageRef = useRef(onMessage)

  onOpenRef.current = onOpen
  onErrorRef.current = onError
  onCloseRef.current = onClose
  onMessageRef.current = onMessage

  useEffect(() => {
    if (!url) return

    let es: EventSource | null = null
    let retryCount = 0
    let retryHandle: ReturnType<typeof setTimeout> | null = null
    let torn = false

    function connect() {
      if (torn) return
      es = new EventSource(url!)
      activeES = es

      es.onopen = () => {
        retryCount = 0
        onOpenRef.current?.()
      }

      es.onerror = (event) => {
        if (es!.readyState === EventSource.CLOSED) {
          if (isPageUnloading.current) {
            onCloseRef.current?.(event, true)
            return
          }
          onCloseRef.current?.(event, false)
          // Exponential backoff: 1s, 2s, 4s, … capped at 5s (local dev only).
          const delay = Math.min(1_000 * 2 ** retryCount, 5_000)
          retryCount++
          retryHandle = setTimeout(connect, delay)
        } else if (!isPageUnloading.current) {
          onErrorRef.current?.(event)
        }
      }

      es.onmessage = (e: MessageEvent<string>) => onMessageRef.current?.(e)
    }

    connect()

    return () => {
      torn = true
      if (retryHandle !== null) clearTimeout(retryHandle)
      es?.close()
      if (activeES === es) activeES = null
    }
  }, [url, isPageUnloading])
}
