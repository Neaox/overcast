import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { eventbridge } from "@/services/api/eventbridge"
import { endpointStore } from "@/services/endpoint-store"

// ─── Tabs ───────────────────────────────────────────────────────────────────

/**
 * The EventBridge tabs, both resource lists. Lives here rather than in
 * `eventbridge-page.tsx` because that file's exports must stay component-only
 * for Fast Refresh; the route file needs this to validate the `tab` search
 * param.
 */
export const EVENTBRIDGE_TABS = ["buses", "rules"] as const
export type EventBridgeTab = (typeof EVENTBRIDGE_TABS)[number]

// ─── Key factory ───────────────────────────────────────────────────────────

export const ebKeys = {
  all: () => [...endpointStore.getKeys(), "eventbridge"] as const,
  buses: () => [...ebKeys.all(), "buses"] as const,
  rules: () => [...ebKeys.all(), "rules"] as const,
  targets: () => [...ebKeys.all(), "rule-targets"] as const,
  deliveries: () => [...ebKeys.all(), "deliveries"] as const,
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

/** Each rule's targets on a bus, with the resolved target type per target. */
export function ebRuleTargetsQueryOptions(eventBusName: string) {
  return queryOptions({
    queryKey: [...ebKeys.targets(), eventBusName] as const,
    queryFn: () => eventbridge.listRuleTargets(eventBusName),
  })
}

/**
 * Recent target delivery outcomes on a bus. Polled so the bus view reflects
 * deliveries triggered elsewhere without a manual refresh; the emulator keeps
 * these in memory only, so there is nothing expensive behind the poll.
 */
export function ebDeliveriesQueryOptions(eventBusName: string) {
  return queryOptions({
    queryKey: [...ebKeys.deliveries(), eventBusName] as const,
    queryFn: () => eventbridge.listDeliveries(eventBusName),
    refetchInterval: 5000,
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
