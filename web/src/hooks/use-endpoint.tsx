import React, { createContext, useCallback, useContext, useState } from "react"
import { endpointResolver, isConfigured, DEFAULT_ENDPOINT } from "@/services/discovery"
import type { EmulatorEndpoint } from "@/services/discovery"

interface EndpointContextValue {
  endpoint: EmulatorEndpoint
  setEndpoint: (e: EmulatorEndpoint) => void
  reset: () => void
}

const EndpointContext = createContext<EndpointContextValue | null>(null)

export function EndpointProvider({ children }: { children: React.ReactNode }) {
  const [endpoint, setEndpointState] = useState<EmulatorEndpoint>(() => endpointResolver.get())

  const setEndpoint = useCallback((e: EmulatorEndpoint) => {
    endpointResolver.set(e)
    setEndpointState(e)
  }, [])

  const reset = useCallback(() => {
    endpointResolver.clear()
    setEndpointState(DEFAULT_ENDPOINT)
  }, [])

  return (
    <EndpointContext.Provider value={{ endpoint, setEndpoint, reset }}>
      {children}
    </EndpointContext.Provider>
  )
}

export function useEndpoint(): EndpointContextValue {
  const ctx = useContext(EndpointContext)
  if (!ctx) throw new Error("useEndpoint must be used within EndpointProvider")
  return ctx
}

export { isConfigured }
