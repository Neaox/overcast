import { useSyncExternalStore } from "react"
import { endpointStore } from "@/services/endpoint-store"
import type { EmulatorEndpoint } from "@/services/discovery"

export { isConfigured } from "@/services/discovery"

export function useEndpoint(): EmulatorEndpoint {
  return useSyncExternalStore(
    (cb) => endpointStore.subscribe(cb),
    () => endpointStore.get(),
  )
}
