import { useState } from "react"
import { queryOptions, useQuery } from "@tanstack/react-query"
import { isConfigured, useEndpoint } from "@/hooks/use-endpoint"
import type { EmulatorEndpoint } from "@/services/discovery"
import { health } from "@/services/api"
import { ConnectionDialog } from "./connection-dialog"
import { ConnectingScreen } from "./connecting-screen"

/**
 * Three states, not two:
 *
 * | state                    | cause                                | renders            |
 * | ------------------------ | ------------------------------------ | ------------------ |
 * | unconfigured             | no endpoint persisted                | `ConnectionDialog` |
 * | configured, unreachable  | endpoint set, emulator has not replied | `ConnectingScreen` |
 * | configured, reachable    | liveness probe has succeeded once    | the app shell      |
 *
 * Configuration and liveness used to be conflated, so a stored endpoint that
 * pointed at nothing rendered the whole shell with every query failing on its
 * own. Liveness is now a real, honoured probe rather than a swallowed one.
 */

/** Cold boot resolves itself once the emulator finishes starting, so poll. */
const PROBE_INTERVAL_MS = 2000

/**
 * Shares the `["health"]` cache entry with the dashboard and the command
 * palette, so the first page to mount already has an answer.
 *
 * Retries are the poll, not TanStack Query's backoff: a failed probe must show
 * the cold-boot screen immediately rather than sit in a retry window.
 */
function livenessProbeQueryOptions(reached: boolean) {
  return queryOptions({
    queryKey: ["health"],
    queryFn: () => health.check(),
    staleTime: 30_000,
    retry: false,
    refetchInterval: reached ? false : PROBE_INTERVAL_MS,
  })
}

function endpointHost(endpoint: EmulatorEndpoint): string {
  try {
    return new URL(endpoint.baseUrl).host
  } catch {
    return endpoint.baseUrl
  }
}

/** Renders `children` only once the configured emulator has answered. */
export function ConnectionGate({ children }: { children: React.ReactNode }) {
  const [configured, setConfigured] = useState(isConfigured)

  // Split so the probe is never mounted for a user who has no host to probe.
  if (!configured) return <ConnectionDialog onConnected={() => setConfigured(true)} />
  return <LivenessGate>{children}</LivenessGate>
}

function LivenessGate({ children }: { children: React.ReactNode }) {
  const endpoint = useEndpoint()
  const [reached, setReached] = useState(false)
  const { isSuccess, refetch } = useQuery(livenessProbeQueryOptions(reached))

  // Latched: a connection that has worked once hands over to the shell for
  // good. A later drop is the OfflineBanner's job — yanking the app back to a
  // cold-boot screen would throw away every page the user has open.
  if (isSuccess && !reached) setReached(true)

  if (!reached) {
    return <ConnectingScreen host={endpointHost(endpoint)} onRetry={() => void refetch()} />
  }
  return <>{children}</>
}
