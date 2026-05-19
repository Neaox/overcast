/**
 * MSK query/mutation definitions.
 *
 * Key factory:
 *   mskKeys.all()       -> [...endpoint, "msk"]
 *   mskKeys.clusters()  -> [...endpoint, "msk", "clusters"]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { msk } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const mskKeys = {
  all: () => [...endpointStore.getKeys(), "msk"] as const,
  clusters: () => [...mskKeys.all(), "clusters"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function mskClustersQueryOptions() {
  return queryOptions({
    queryKey: mskKeys.clusters(),
    queryFn: () => msk.listClusters(),
    staleTime: 5_000,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createMSKClusterMutationOptions() {
  return mutationOptions({
    mutationKey: [...mskKeys.clusters(), "create"] as const,
    mutationFn: (opts: {
      clusterName: string
      kafkaVersion: string
      numberOfBrokerNodes: number
    }) => msk.createCluster(opts),
  })
}

export function deleteMSKClusterMutationOptions() {
  return mutationOptions({
    mutationKey: [...mskKeys.clusters(), "delete"] as const,
    mutationFn: (clusterArn: string) => msk.deleteCluster(clusterArn),
  })
}
