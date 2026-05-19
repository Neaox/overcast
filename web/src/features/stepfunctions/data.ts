import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { stepfunctions } from "@/services/api/stepfunctions"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const sfnKeys = {
  all: () => [...endpointStore.getKeys(), "stepfunctions"] as const,
  stateMachines: () => [...sfnKeys.all(), "stateMachines"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function sfnStateMachinesQueryOptions() {
  return queryOptions({
    queryKey: sfnKeys.stateMachines(),
    queryFn: () => stepfunctions.listStateMachines(),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createStateMachineMutationOptions() {
  return mutationOptions({
    mutationKey: [...sfnKeys.stateMachines(), "create"] as const,
    mutationFn: (name: string) => stepfunctions.createStateMachine(name),
  })
}

export function deleteStateMachineMutationOptions() {
  return mutationOptions({
    mutationKey: [...sfnKeys.stateMachines(), "delete"] as const,
    mutationFn: (arn: string) => stepfunctions.deleteStateMachine(arn),
  })
}
