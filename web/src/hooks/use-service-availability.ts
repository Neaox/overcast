/**
 * Service availability — which services this emulator run actually has on.
 *
 * `OVERCAST_SERVICES` narrows the enabled set and `/_health` reports the
 * result. Every surface that greys, disables, or defaults on availability
 * reads it from here so they cannot disagree, and they all share the
 * `["health"]` cache entry the connection gate has already populated.
 */

import { useCallback, useMemo } from "react"
import { queryOptions, useQuery } from "@tanstack/react-query"
import { ALL_SERVICES } from "@/lib/nav-services"
import { SERVICES, type ServiceEntry } from "@/lib/service-registry"
import { health } from "@/services/api"
import type { HealthResponse } from "@/types"

/** Shown wherever a disabled service is still rendered. */
export const DISABLED_HINT = "Disabled in this emulator run."

/** Emulator service name (the name reported in /_health) by route path. */
const NAME_BY_ROUTE = new Map<string, string>(
  Object.entries(SERVICES as Record<string, ServiceEntry>).flatMap(([name, entry]) =>
    entry.to ? [[entry.to, name] as const] : [],
  ),
)

/** Pinnable nav services — the candidate set for the default pins. */
const PINNABLE = ALL_SERVICES.filter((service) => service.favouritable !== false)

export const healthQueryOptions = queryOptions({
  queryKey: ["health"],
  queryFn: () => health.check(),
  staleTime: 30_000,
  retry: 2,
})

/**
 * Availability is the emulator's enabled/disabled list and nothing else.
 * Emulation tier is deliberately ignored: a live service whose tier entry is
 * missing reports "stub", and greying on that would hide working services.
 *
 * No health payload (loading, or an error) means the emulator's view is
 * unknown — everything is treated as enabled rather than greyed on a guess.
 */
export function isServiceEnabled(data: HealthResponse | undefined, route: string): boolean {
  if (!data) return true
  const name = NAME_BY_ROUTE.get(route)
  return name === undefined || data.services.includes(name)
}

/**
 * The enabled nav services, in registry order — the sidebar's default pins
 * when the user has pinned nothing themselves.
 *
 * Empty unless this run enables only *some* of them: a full run would pin the
 * whole catalogue, which says nothing and is not what anyone means by pinning.
 * That doubles as the `OVERCAST_SERVICES` signal — the enabled set is only ever
 * a strict subset because someone narrowed it.
 */
export function defaultPinnedServices(data: HealthResponse | undefined): string[] {
  if (!data) return []
  const enabled = PINNABLE.filter((service) => isServiceEnabled(data, service.to))
  return enabled.length === PINNABLE.length ? [] : enabled.map((service) => service.key)
}

export interface ServiceAvailability {
  /** Whether the emulator has the service owning `route` switched on. */
  isEnabled: (route: string) => boolean
  /** Service keys to pin when the user has pinned nothing. May be empty. */
  defaultPins: string[]
}

export function useServiceAvailability(): ServiceAvailability {
  const { data } = useQuery(healthQueryOptions)
  return {
    isEnabled: useCallback((route: string) => isServiceEnabled(data, route), [data]),
    defaultPins: useMemo(() => defaultPinnedServices(data), [data]),
  }
}
