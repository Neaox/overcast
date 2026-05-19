/**
 * endpoint-store — module-level singleton for the active emulator endpoint.
 *
 * Components subscribe via useSyncExternalStore (see use-endpoint.tsx).
 * React Query cache is reset automatically on endpoint change via the
 * subscription wired in main.tsx.
 *
 * Key factory functions (e.g. s3Keys.buckets()) call getKeys() at call time,
 * so every render always sees the current endpoint in the query key.
 */

import { DEFAULT_ENDPOINT, endpointResolver } from "./discovery"
import type { EmulatorEndpoint } from "./discovery"

type Listener = (prev: EmulatorEndpoint, next: EmulatorEndpoint) => void

let current: EmulatorEndpoint = endpointResolver.get()
const listeners = new Set<Listener>()

export const endpointStore = {
  get: (): EmulatorEndpoint => current,

  set(next: EmulatorEndpoint): void {
    // Always persist — even if the values match the in-memory default — so
    // that isConfigured() returns true on the next page load.
    endpointResolver.set(next)
    if (
      current.baseUrl === next.baseUrl &&
      current.region === next.region &&
      current.label === next.label
    )
      return
    const prev = current
    current = next
    listeners.forEach((l) => l(prev, next))
  },

  /** Clears persisted endpoint so isConfigured() returns false on next page load. */
  reset(): void {
    const prev = current
    current = DEFAULT_ENDPOINT
    endpointResolver.clear()
    listeners.forEach((l) => l(prev, DEFAULT_ENDPOINT))
  },

  subscribe(listener: Listener): () => void {
    listeners.add(listener)
    return () => {
      listeners.delete(listener)
    }
  },

  /** Returns [baseUrl, region] — use as the first two segments of every endpoint-scoped query key. */
  getKeys(): readonly [baseUrl: string, region: string] {
    return [current.baseUrl, current.region]
  },
}
