import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { settings } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

export const settingsKeys = {
  all: ["settings"] as const,
  https: () => [...endpointStore.getKeys(), ...settingsKeys.all, "https"] as const,
}

/**
 * GET /api/settings/https — the daemon's view of the HTTPS setup. The
 * trust-store check shells out to platform tooling on the daemon side, so
 * this is fetched on demand (settings page open), not polled.
 */
export function httpsStatusQueryOptions() {
  return queryOptions({
    queryKey: settingsKeys.https(),
    queryFn: () => settings.httpsStatus(),
    staleTime: 5_000,
  })
}

/**
 * POST /api/settings/https/enable — mint certificates and (natively) install
 * the CA into the system trust store. Blocks on the OS approval prompt.
 */
export function enableHttpsMutationOptions() {
  return mutationOptions({
    mutationKey: [...settingsKeys.all, "https", "enable"] as const,
    mutationFn: () => settings.enableHttps(),
  })
}
