/**
 * CloudFront query/mutation definitions.
 *
 * Key factory:
 *   cloudfrontKeys.all()                        -> [endpoint, region, "cloudfront"]
 *   cloudfrontKeys.distributions()              -> [..., "distributions"]
 *   cloudfrontKeys.distribution(id)             -> [..., "distribution", id]
 *   cloudfrontKeys.invalidations(distId)        -> [..., "invalidations", distId]
 *   cloudfrontKeys.oacs()                       -> [..., "oacs"]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { cloudfront } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const cloudfrontKeys = {
  all: () => [...endpointStore.getKeys(), "cloudfront"] as const,
  distributions: () => [...cloudfrontKeys.all(), "distributions"] as const,
  distribution: () => [...cloudfrontKeys.all(), "distribution"] as const,
  distributionDetail: (id: string) => [...cloudfrontKeys.distribution(), id] as const,
  invalidations: () => [...cloudfrontKeys.all(), "invalidations"] as const,
  invalidationList: (distId: string) => [...cloudfrontKeys.invalidations(), distId] as const,
  oacs: () => [...cloudfrontKeys.all(), "oacs"] as const,
  realtimeLogConfigs: () => [...cloudfrontKeys.all(), "realtimeLogConfigs"] as const,
  keyGroups: () => [...cloudfrontKeys.all(), "keyGroups"] as const,
  fleConfigs: () => [...cloudfrontKeys.all(), "fleConfigs"] as const,
  fleProfiles: () => [...cloudfrontKeys.all(), "fleProfiles"] as const,
  continuousDeploymentPolicies: () =>
    [...cloudfrontKeys.all(), "continuousDeploymentPolicies"] as const,
  monitoringSubscription: (distId: string) =>
    [...cloudfrontKeys.all(), "monitoringSubscription", distId] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function cloudfrontDistributionsQueryOptions() {
  return queryOptions({
    queryKey: cloudfrontKeys.distributions(),
    queryFn: () => cloudfront.listDistributions(),
  })
}

export function cloudfrontDistributionQueryOptions(id: string) {
  return queryOptions({
    queryKey: cloudfrontKeys.distributionDetail(id),
    queryFn: () => cloudfront.getDistribution(id),
    enabled: !!id,
  })
}

export function cloudfrontInvalidationsQueryOptions(distId: string) {
  return queryOptions({
    queryKey: cloudfrontKeys.invalidationList(distId),
    queryFn: () => cloudfront.listInvalidations(distId),
    enabled: !!distId,
  })
}

export function cloudfrontOACsQueryOptions() {
  return queryOptions({
    queryKey: cloudfrontKeys.oacs(),
    queryFn: () => cloudfront.listOriginAccessControls(),
  })
}

export function cloudfrontRealtimeLogConfigsQueryOptions() {
  return queryOptions({
    queryKey: cloudfrontKeys.realtimeLogConfigs(),
    queryFn: () => cloudfront.listRealtimeLogConfigs(),
  })
}

export function cloudfrontKeyGroupsQueryOptions() {
  return queryOptions({
    queryKey: cloudfrontKeys.keyGroups(),
    queryFn: () => cloudfront.listKeyGroups(),
  })
}

export function cloudfrontFLEConfigsQueryOptions() {
  return queryOptions({
    queryKey: cloudfrontKeys.fleConfigs(),
    queryFn: () => cloudfront.listFLEConfigs(),
  })
}

export function cloudfrontFLEProfilesQueryOptions() {
  return queryOptions({
    queryKey: cloudfrontKeys.fleProfiles(),
    queryFn: () => cloudfront.listFLEProfiles(),
  })
}

export function cloudfrontContinuousDeploymentPoliciesQueryOptions() {
  return queryOptions({
    queryKey: cloudfrontKeys.continuousDeploymentPolicies(),
    queryFn: () => cloudfront.listContinuousDeploymentPolicies(),
  })
}

export function cloudfrontMonitoringSubscriptionQueryOptions(distId: string) {
  return queryOptions({
    queryKey: cloudfrontKeys.monitoringSubscription(distId),
    queryFn: () => cloudfront.getMonitoringSubscription(distId),
    enabled: !!distId,
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function createDistributionMutationOptions() {
  return mutationOptions({
    mutationKey: [...cloudfrontKeys.distributions(), "create"] as const,
    mutationFn: (opts: {
      comment: string
      enabled: boolean
      originDomainName: string
      originId: string
      defaultRootObject?: string
    }) => cloudfront.createDistribution(opts),
  })
}

export function deleteDistributionMutationOptions() {
  return mutationOptions({
    mutationKey: [...cloudfrontKeys.distributions(), "delete"] as const,
    mutationFn: ({ id, etag }: { id: string; etag: string }) =>
      cloudfront.deleteDistribution(id, etag),
  })
}

export function updateDistributionMutationOptions() {
  return mutationOptions({
    mutationKey: [...cloudfrontKeys.distributions(), "update"] as const,
    mutationFn: ({
      id,
      etag,
      updates,
    }: {
      id: string
      etag: string
      updates: { comment?: string; enabled?: boolean; defaultRootObject?: string }
    }) => cloudfront.updateDistribution(id, etag, updates),
  })
}

export function createInvalidationMutationOptions(distId: string) {
  return mutationOptions({
    mutationKey: [...cloudfrontKeys.invalidations(), distId, "create"] as const,
    mutationFn: (paths: string[]) => cloudfront.createInvalidation(distId, paths),
  })
}
