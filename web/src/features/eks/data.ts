import { mutationOptions, queryOptions } from "@tanstack/react-query"
import { eks } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

export const eksKeys = {
  all: () => [...endpointStore.getKeys(), "eks"] as const,
  clusters: () => [...eksKeys.all(), "clusters"] as const,
}

export function eksClustersQueryOptions() {
  return queryOptions({
    queryKey: eksKeys.clusters(),
    queryFn: () => eks.listClusters(),
    staleTime: 5_000,
  })
}

export function createEksClusterMutationOptions() {
  return mutationOptions({
    mutationKey: [...eksKeys.clusters(), "create"] as const,
    mutationFn: (name: string) => eks.createCluster(name),
  })
}
