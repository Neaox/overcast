/**
 * ECS query/mutation definitions.
 *
 * Key factory:
 *   ecsKeys.all()                    -> [...endpoint, "ecs"]
 *   ecsKeys.clusters()               -> [...endpoint, "ecs", "clusters"]
 *   ecsKeys.clusterDetail(name)      -> [...endpoint, "ecs", "cluster", name]
 *   ecsKeys.taskDefinitions()        -> [...endpoint, "ecs", "task-definitions"]
 *   ecsKeys.tasks(cluster)           -> [...endpoint, "ecs", "tasks", cluster]
 *   ecsKeys.services(cluster)        -> [...endpoint, "ecs", "services", cluster]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { ecs } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const ecsKeys = {
  all: () => [...endpointStore.getKeys(), "ecs"] as const,
  clusters: () => [...ecsKeys.all(), "clusters"] as const,
  clusterDetail: (name: string) => [...ecsKeys.all(), "cluster", name] as const,
  taskDefinitions: () => [...ecsKeys.all(), "task-definitions"] as const,
  taskDefinitionFamilies: () => [...ecsKeys.all(), "task-definition-families"] as const,
  tasks: (cluster: string) => [...ecsKeys.all(), "tasks", cluster] as const,
  services: (cluster: string) => [...ecsKeys.all(), "services", cluster] as const,
  containerInstances: (cluster: string) =>
    [...ecsKeys.all(), "container-instances", cluster] as const,
  tags: (arn: string) => [...ecsKeys.all(), "tags", arn] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function ecsClustersQueryOptions() {
  return queryOptions({
    queryKey: ecsKeys.clusters(),
    queryFn: () => ecs.listClusters(),
    staleTime: 30_000,
  })
}

export function ecsClusterDetailQueryOptions(name: string) {
  return queryOptions({
    queryKey: ecsKeys.clusterDetail(name),
    queryFn: () => ecs.describeCluster(name),
    staleTime: 10_000,
  })
}

export function ecsTaskDefinitionsQueryOptions() {
  return queryOptions({
    queryKey: ecsKeys.taskDefinitions(),
    queryFn: () => ecs.listTaskDefinitions(),
    staleTime: 30_000,
  })
}

export function ecsTasksQueryOptions(cluster: string) {
  return queryOptions({
    queryKey: ecsKeys.tasks(cluster),
    queryFn: () => ecs.listTasks(cluster),
    staleTime: 5_000,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createClusterMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecsKeys.clusters(), "create"] as const,
    mutationFn: (name: string) => ecs.createCluster(name),
  })
}

export function deleteClusterMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecsKeys.clusters(), "delete"] as const,
    mutationFn: (name: string) => ecs.deleteCluster(name),
  })
}

export function registerTaskDefinitionMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecsKeys.taskDefinitions(), "register"] as const,
    mutationFn: (json: string) => ecs.registerTaskDefinition(json),
  })
}

export function deregisterTaskDefinitionMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecsKeys.taskDefinitions(), "deregister"] as const,
    mutationFn: (arn: string) => ecs.deregisterTaskDefinition(arn),
  })
}

export function runTaskMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecsKeys.all(), "run-task"] as const,
    mutationFn: (opts: {
      cluster: string
      taskDefinition: string
      count?: number
      launchType?: string
    }) => ecs.runTask(opts),
  })
}

export function stopTaskMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecsKeys.all(), "stop-task"] as const,
    mutationFn: (opts: { cluster: string; task: string; reason?: string }) =>
      ecs.stopTask(opts.cluster, opts.task, opts.reason),
  })
}

// ─── Service query/mutation definitions ────────────────────────────────────

export function ecsServicesQueryOptions(cluster: string) {
  return queryOptions({
    queryKey: ecsKeys.services(cluster),
    queryFn: () => ecs.listServices(cluster),
    staleTime: 10_000,
  })
}

export function createServiceMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecsKeys.all(), "create-service"] as const,
    mutationFn: (params: {
      cluster: string
      serviceName: string
      taskDefinition: string
      desiredCount: number
    }) => ecs.createService(params),
  })
}

export function updateServiceMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecsKeys.all(), "update-service"] as const,
    mutationFn: (params: {
      cluster: string
      service: string
      desiredCount?: number
      taskDefinition?: string
    }) => ecs.updateService(params),
  })
}

export function deleteServiceMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecsKeys.all(), "delete-service"] as const,
    mutationFn: (params: { cluster: string; service: string }) => ecs.deleteService(params),
  })
}

export function ecsTaskDefinitionFamiliesQueryOptions() {
  return queryOptions({
    queryKey: ecsKeys.taskDefinitionFamilies(),
    queryFn: () => ecs.listTaskDefinitionFamilies(),
    staleTime: 30_000,
  })
}

export function ecsContainerInstancesQueryOptions(cluster: string) {
  return queryOptions({
    queryKey: ecsKeys.containerInstances(cluster),
    queryFn: () => ecs.listContainerInstances(cluster),
    staleTime: 15_000,
  })
}

export function ecsTagsQueryOptions(resourceArn: string) {
  return queryOptions({
    queryKey: ecsKeys.tags(resourceArn),
    queryFn: () => ecs.listTagsForResource(resourceArn),
    staleTime: 30_000,
    enabled: !!resourceArn,
  })
}
