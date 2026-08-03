/**
 * Auto Scaling query/mutation definitions.
 *
 * Key factory:
 *   autoscalingKeys.all()               -> [...endpoint, "autoscaling"]
 *   autoscalingKeys.groups()            -> [...endpoint, "autoscaling", "groups"]
 *   autoscalingKeys.policies()          -> [...endpoint, "autoscaling", "policies"]
 *   autoscalingKeys.hooks(group)        -> [...endpoint, "autoscaling", "hooks", group]
 *   autoscalingKeys.activities(group)   -> [...endpoint, "autoscaling", "activities", group]
 *
 * Groups refresh briskly: the reconciler moves instances through Pending →
 * InService in the background, so a stale list is the one thing this page must
 * not show.
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { autoscaling } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const autoscalingKeys = {
  all: () => [...endpointStore.getKeys(), "autoscaling"] as const,
  groups: () => [...autoscalingKeys.all(), "groups"] as const,
  policies: () => [...autoscalingKeys.all(), "policies"] as const,
  hooks: (group: string) => [...autoscalingKeys.all(), "hooks", group] as const,
  activities: (group: string) => [...autoscalingKeys.all(), "activities", group] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function autoscalingGroupsQueryOptions() {
  return queryOptions({
    queryKey: autoscalingKeys.groups(),
    queryFn: () => autoscaling.listGroups(),
    staleTime: 2_000,
    refetchInterval: 5_000,
  })
}

export function autoscalingPoliciesQueryOptions() {
  return queryOptions({
    queryKey: autoscalingKeys.policies(),
    queryFn: () => autoscaling.listPolicies(),
    staleTime: 5_000,
  })
}

export function autoscalingHooksQueryOptions(group: string) {
  return queryOptions({
    queryKey: autoscalingKeys.hooks(group),
    queryFn: () => autoscaling.listLifecycleHooks(group),
    staleTime: 5_000,
    enabled: group !== "",
  })
}

export function autoscalingActivitiesQueryOptions(group: string) {
  return queryOptions({
    queryKey: autoscalingKeys.activities(group),
    queryFn: () => autoscaling.listActivities(group),
    staleTime: 2_000,
    refetchInterval: 5_000,
    enabled: group !== "",
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function setDesiredCapacityMutationOptions() {
  return mutationOptions({
    mutationKey: [...autoscalingKeys.groups(), "set-desired-capacity"] as const,
    mutationFn: ({ group, desiredCapacity }: { group: string; desiredCapacity: number }) =>
      autoscaling.setDesiredCapacity(group, desiredCapacity),
  })
}
