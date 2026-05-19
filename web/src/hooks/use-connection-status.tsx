import React, { createContext, useContext, useEffect } from "react"
import { useQuery, onlineManager } from "@tanstack/react-query"
import { eventStreamStatusQueryOptions } from "@/hooks/use-event-stream"

interface ConnectionStatusContextValue {
  /** `true` = connected, `false` = disconnected, `null` = still connecting */
  isOnline: boolean | null
}

const ConnectionStatusContext = createContext<ConnectionStatusContextValue>({ isOnline: null })

/**
 * Reads the EventSource connection status from the query cache.
 * The singleton subscription (useEventStreamSubscription) pushes
 * connected/disconnected state; this provider syncs it to React
 * context and TanStack Query's onlineManager.
 */
export function ConnectionStatusProvider({ children }: { children: React.ReactNode }) {
  const { data: status } = useQuery(eventStreamStatusQueryOptions())
  const isOnline = status?.connected ?? null

  useEffect(() => {
    // Only tell TanStack Query's onlineManager once we have a definitive state.
    if (isOnline !== null) onlineManager.setOnline(isOnline)
  }, [isOnline])

  return (
    <ConnectionStatusContext.Provider value={{ isOnline }}>
      {children}
    </ConnectionStatusContext.Provider>
  )
}

/** Returns the current backend connection status. */
// eslint-disable-next-line react-refresh/only-export-components
export function useConnectionStatus(): ConnectionStatusContextValue {
  return useContext(ConnectionStatusContext)
}
