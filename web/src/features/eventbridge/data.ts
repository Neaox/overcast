import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { eventbridge } from "@/services/api/eventbridge"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const ebKeys = {
  all: () => [...endpointStore.getKeys(), "eventbridge"] as const,
  buses: () => [...ebKeys.all(), "buses"] as const,
  rules: () => [...ebKeys.all(), "rules"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function ebBusesQueryOptions() {
  return queryOptions({
    queryKey: ebKeys.buses(),
    queryFn: () => eventbridge.listBuses(),
  })
}

export function ebRulesQueryOptions(eventBusName?: string) {
  return queryOptions({
    queryKey: [...ebKeys.rules(), eventBusName ?? "default"] as const,
    queryFn: () => eventbridge.listRules(eventBusName),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createBusMutationOptions() {
  return mutationOptions({
    mutationKey: [...ebKeys.buses(), "create"] as const,
    mutationFn: (name: string) => eventbridge.createBus(name),
  })
}

export function deleteBusMutationOptions() {
  return mutationOptions({
    mutationKey: [...ebKeys.buses(), "delete"] as const,
    mutationFn: (name: string) => eventbridge.deleteBus(name),
  })
}

export function createRuleMutationOptions() {
  return mutationOptions({
    mutationKey: [...ebKeys.rules(), "create"] as const,
    mutationFn: (name: string) => eventbridge.createRule(name),
  })
}

export function deleteRuleMutationOptions() {
  return mutationOptions({
    mutationKey: [...ebKeys.rules(), "delete"] as const,
    mutationFn: (name: string) => eventbridge.deleteRule(name),
  })
}
