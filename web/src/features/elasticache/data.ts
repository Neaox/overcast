/**
 * ElastiCache query/mutation definitions.
 *
 * Key factory:
 *   elasticacheKeys.all()          -> [...endpoint, "elasticache"]
 *   elasticacheKeys.clusters()     -> [...endpoint, "elasticache", "clusters"]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { elasticache } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const elasticacheKeys = {
  all: () => [...endpointStore.getKeys(), "elasticache"] as const,
  clusters: () => [...elasticacheKeys.all(), "clusters"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function elasticacheClustersQueryOptions() {
  return queryOptions({
    queryKey: elasticacheKeys.clusters(),
    queryFn: () => elasticache.listClusters(),
    staleTime: 5_000,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createClusterMutationOptions() {
  return mutationOptions({
    mutationKey: [...elasticacheKeys.clusters(), "create"] as const,
    mutationFn: (opts: {
      CacheClusterId: string
      Engine: string
      CacheNodeType: string
      NumCacheNodes: number
    }) => elasticache.createCluster(opts),
  })
}

export function deleteClusterMutationOptions() {
  return mutationOptions({
    mutationKey: [...elasticacheKeys.clusters(), "delete"] as const,
    mutationFn: (id: string) => elasticache.deleteCluster(id),
  })
}
