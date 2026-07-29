import React, { createContext, useContext, useEffect, useMemo } from "react"
import { useQuery, onlineManager } from "@tanstack/react-query"
import {
  eventStreamStatusQueryOptions,
  reconnectEventStream,
  type EventStreamStatus,
} from "@/hooks/use-event-stream"

interface ConnectionStatusContextValue {
  /** `true` = connected, `false` = *dropped*, `null` = still connecting */
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
  const isOnline = connectionOf(status)

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

/**
 * Narrows the worker's `connected` to what the UI means by offline: a
 * connection that *dropped*.
 *
 * The worker's boolean answers a different question. Its stream starts at
 * `DISCONNECTED`, and both transports broadcast that state before the SSE
 * handshake has had a chance — so `connected: false` is the first definitive
 * answer of every session, and of every endpoint switch. Passed straight
 * through it made the app announce "Reconnected" on load for a connection
 * that had never been lost, flash the reconnecting toast before the first
 * frame arrived, and tell TanStack Query's `onlineManager` it was offline
 * while the opening queries were in flight.
 *
 * So a stream that has never been open reports as `null` — "still
 * connecting", which is what `null` already meant. There is nothing for the
 * shell to say about a first connection: the liveness probe and the stream
 * share a server and a proxy, so a stream that has never opened means the
 * connection gate's cold-boot screen is what the user is looking at.
 *
 * `lastConnectedAt` is therefore the whole test, not `connected` alone — it
 * is the only field that distinguishes "not yet" from "not any more".
 */
function connectionOf(status: EventStreamStatus | undefined): boolean | null {
  if (!status) return null
  // A live stream always carries the timestamp of its own open, so the two
  // conditions only disagree if the worker contradicts itself.
  if (status.connected !== true && status.lastConnectedAt === null) return null
  return status.connected
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
