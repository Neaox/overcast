import { useSyncExternalStore } from "react"
import { endpointStore } from "@/services/endpoint-store"
import { isConfigured, type EmulatorEndpoint } from "@/services/discovery"

export { isConfigured }

const subscribe = (cb: () => void) => endpointStore.subscribe(cb)

export function useEndpoint(): EmulatorEndpoint {
  return useSyncExternalStore(subscribe, () => endpointStore.get())
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
