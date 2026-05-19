import { mutationOptions, queryOptions } from "@tanstack/react-query"
import { ecr } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

export const ecrKeys = {
  all: () => [...endpointStore.getKeys(), "ecr"] as const,
  repositories: () => [...ecrKeys.all(), "repositories"] as const,
  repository: () => [...ecrKeys.all(), "repository"] as const,
  repositoryDetail: (name: string) => [...ecrKeys.repository(), name] as const,
}

export function ecrRepositoriesQueryOptions() {
  return queryOptions({
    queryKey: ecrKeys.repositories(),
    queryFn: () => ecr.listRepositories(),
  })
}

export function ecrRepositoryQueryOptions(name: string) {
  return queryOptions({
    queryKey: ecrKeys.repositoryDetail(name),
    queryFn: () => ecr.getRepository(name),
    enabled: !!name,
  })
}

export function createRepositoryMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecrKeys.repositories(), "create"] as const,
    mutationFn: (name: string) => ecr.createRepository(name),
  })
}

export function deleteRepositoryMutationOptions() {
  return mutationOptions({
    mutationKey: [...ecrKeys.repositories(), "delete"] as const,
    mutationFn: (name: string) => ecr.deleteRepository(name),
  })
}
