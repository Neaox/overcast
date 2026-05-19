/**
 * use-topology — React Query hook that fetches the topology graph.
 *
 * The topology is driven primarily by SSE lifecycle events (resource created /
 * deleted / reconfigured) and SQS message state changes, all of which
 * invalidate this query immediately via use-event-animations. The
 * refetchInterval here is a safety-net only — it catches any events that
 * arrived before the SSE connection was established or were missed due to a
 * brief disconnect.
 *
 * Pass `region` to filter server-side; omit for all regions.
 */

import { useQuery, queryOptions } from "@tanstack/react-query"
import { topology } from "@/services/api"

/** Prefix key used for invalidation — matches all topology queries regardless of region. */
export const topologyKey = ["topology"] as const

export function useTopology(region?: string) {
  return useQuery(
    queryOptions({
      queryKey: region ? ["topology", region] : topologyKey,
      queryFn: () => topology.get(region),
      refetchInterval: 300_000,
      staleTime: 0,
    }),
  )
}
