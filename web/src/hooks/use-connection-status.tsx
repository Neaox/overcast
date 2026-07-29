import React, { createContext, useContext, useEffect, useMemo } from "react"
import { useQuery, onlineManager } from "@tanstack/react-query"
import {
  eventStreamStatusQueryOptions,
  reconnectEventStream,
  type EventStreamStatus,
} from "@/hooks/use-event-stream"

interface ConnectionStatusContextValue {
  /** `true` = connected, `false` = disconnected, `null` = still connecting */
  isOnline: boolean | null
  /** Reconnect attempts since the stream dropped; 0 while connected. */
  attempt: number
  /**
   * When the pending reconnect is due (epoch ms), or `null` while an attempt
   * is already in flight. Scheduled by the worker, so every tab counts down
   * to the same moment.
   */
  nextAttemptAt: number | null
  /** When the stream was last open (epoch ms) — how old the cached view is. */
  lastConnectedAt: number | null
  /** Brings the scheduled attempt forward to now. */
  retryNow: () => void
}

const ConnectionStatusContext = createContext<ConnectionStatusContextValue>({
  isOnline: null,
  attempt: 0,
  nextAttemptAt: null,
  lastConnectedAt: null,
  retryNow: reconnectEventStream,
})

/**
 * Reads the SSE connection state from the query cache.
 * The singleton subscription (useEventStreamSubscription) pushes the state the
 * worker reports — connected, which attempt is pending and when it is due;
 * this provider syncs it to React context and TanStack Query's onlineManager.
 */
export function ConnectionStatusProvider({ children }: { children: React.ReactNode }) {
  const { data: status } = useQuery(eventStreamStatusQueryOptions())
  const isOnline = status?.connected ?? null

  useEffect(() => {
    // Only tell TanStack Query's onlineManager once we have a definitive state.
    if (isOnline !== null) onlineManager.setOnline(isOnline)
  }, [isOnline])

  const value = useMemo<ConnectionStatusContextValue>(
    () => ({ ...retryDetail(status), isOnline, retryNow: reconnectEventStream }),
    [status, isOnline],
  )

  return (
    <ConnectionStatusContext.Provider value={value}>{children}</ConnectionStatusContext.Provider>
  )
}

/** The schedule half of the state, defaulted for the pre-answer render. */
function retryDetail(status: EventStreamStatus | undefined) {
  return {
    attempt: status?.attempt ?? 0,
    nextAttemptAt: status?.nextAttemptAt ?? null,
    lastConnectedAt: status?.lastConnectedAt ?? null,
  }
}

/** Returns the current backend connection status. */
// eslint-disable-next-line react-refresh/only-export-components
export function useConnectionStatus(): ConnectionStatusContextValue {
  return useContext(ConnectionStatusContext)
}
