import { useSyncExternalStore } from "react"
import { endpointStore } from "@/services/endpoint-store"
import { isConfigured, type EmulatorEndpoint } from "@/services/discovery"

export { isConfigured }

const subscribe = (cb: () => void) => endpointStore.subscribe(cb)

export function useEndpoint(): EmulatorEndpoint {
  return useSyncExternalStore(subscribe, () => endpointStore.get())
}

/**
 * Host of an endpoint, e.g. `localhost:4566` — how the UI names the emulator
 * whenever it is talking about the connection rather than about a resource.
 * Falls back to the raw value, which is what a user typed if it will not parse.
 */
export function endpointHost(endpoint: EmulatorEndpoint): string {
  try {
    return new URL(endpoint.baseUrl).host
  } catch {
    return endpoint.baseUrl
  }
}

/** {@link endpointHost} of the configured endpoint, tracked live. */
export function useEndpointHost(): string {
  return endpointHost(useEndpoint())
}

/**
 * Whether an endpoint is configured, tracked live.
 *
 * `isConfigured()` reads persisted state, and the settings control clears it at
 * runtime via `endpointStore.reset()`. Snapshotting it into `useState` left the
 * connection dialog unreachable until a manual reload, so "change connection"
 * appeared to do nothing.
 */
export function useIsConfigured(): boolean {
  return useSyncExternalStore(subscribe, isConfigured)
}
