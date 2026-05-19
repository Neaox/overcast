/**
 * SSM Parameter Store query/mutation definitions.
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { ssm } from "@/services/api/ssm"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const ssmKeys = {
  all: () => [...endpointStore.getKeys(), "ssm"] as const,
  parameters: () => [...ssmKeys.all(), "parameters"] as const,
  parameter: (name: string) => [...ssmKeys.all(), "parameter", name] as const,
  history: (name: string) => [...ssmKeys.all(), "history", name] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function ssmParametersQueryOptions() {
  return queryOptions({
    queryKey: ssmKeys.parameters(),
    queryFn: () => ssm.listParameters(),
  })
}

export function ssmParameterDetailQueryOptions(name: string) {
  return queryOptions({
    queryKey: ssmKeys.parameter(name),
    queryFn: () => ssm.getParameter(name),
    enabled: !!name,
  })
}

export function ssmParameterHistoryQueryOptions(name: string) {
  return queryOptions({
    queryKey: ssmKeys.history(name),
    queryFn: () => ssm.getParameterHistory(name),
    enabled: !!name,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function putParameterMutationOptions() {
  return mutationOptions({
    mutationKey: [...ssmKeys.parameters(), "put"] as const,
    mutationFn: ({ name, value, type }: { name: string; value: string; type?: string }) =>
      ssm.putParameter(name, value, type),
  })
}

export function deleteParameterMutationOptions() {
  return mutationOptions({
    mutationKey: [...ssmKeys.parameters(), "delete"] as const,
    mutationFn: (name: string) => ssm.deleteParameter(name),
  })
}
