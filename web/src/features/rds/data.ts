/**
 * RDS query/mutation definitions.
 *
 * Key factory:
 *   rdsKeys.all()                     -> [...endpoint, "rds"]
 *   rdsKeys.instances()               -> [...endpoint, "rds", "instances"]
 *   rdsKeys.instanceDetail(id)        -> [...endpoint, "rds", "instance", id]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { rds } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const rdsKeys = {
  all: () => [...endpointStore.getKeys(), "rds"] as const,
  instances: () => [...rdsKeys.all(), "instances"] as const,
  instanceDetail: (id: string) => [...rdsKeys.all(), "instance", id] as const,
  instanceLogs: (id: string) => [...rdsKeys.all(), "instance", id, "logs"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function rdsInstancesQueryOptions() {
  return queryOptions({
    queryKey: rdsKeys.instances(),
    queryFn: () => rds.listInstances(),
    staleTime: 5_000,
  })
}

export function rdsInstanceDetailQueryOptions(id: string) {
  return queryOptions({
    queryKey: rdsKeys.instanceDetail(id),
    queryFn: () => rds.describeInstance(id),
    staleTime: 5_000,
  })
}

export function rdsInstanceLogsQueryOptions(id: string) {
  return queryOptions({
    queryKey: rdsKeys.instanceLogs(id),
    queryFn: () => rds.getInstanceLogs(id),
    staleTime: 10_000,
    refetchInterval: 10_000,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createInstanceMutationOptions() {
  return mutationOptions({
    mutationKey: [...rdsKeys.instances(), "create"] as const,
    mutationFn: (opts: {
      DBInstanceIdentifier: string
      Engine: string
      EngineVersion?: string
      MasterUsername: string
      MasterUserPassword: string
      DBInstanceClass: string
      AllocatedStorage: number
    }) => rds.createInstance(opts),
  })
}

export function deleteInstanceMutationOptions() {
  return mutationOptions({
    mutationKey: [...rdsKeys.instances(), "delete"] as const,
    mutationFn: (identifier: string) => rds.deleteInstance(identifier),
  })
}

export function startInstanceMutationOptions() {
  return mutationOptions({
    mutationKey: [...rdsKeys.instances(), "start"] as const,
    mutationFn: (identifier: string) => rds.startInstance(identifier),
  })
}

export function stopInstanceMutationOptions() {
  return mutationOptions({
    mutationKey: [...rdsKeys.instances(), "stop"] as const,
    mutationFn: (identifier: string) => rds.stopInstance(identifier),
  })
}
