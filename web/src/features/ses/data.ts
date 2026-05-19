/**
 * SES query/mutation definitions.
 *
 * Key factory:
 *   sesKeys.all()            -> [baseUrl, region, "ses"]
 *   sesKeys.identities()     -> [...all(), "identities"]
 */

import { queryOptions, mutationOptions } from "@tanstack/react-query"
import { ses } from "@/services/api"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const sesKeys = {
  all: () => [...endpointStore.getKeys(), "ses"] as const,
  identities: () => [...sesKeys.all(), "identities"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function sesIdentitiesQueryOptions() {
  return queryOptions({
    queryKey: sesKeys.identities(),
    queryFn: () => ses.listIdentities(),
  })
}

// ─── Mutation definitions ──────────────────────────────────────────────────

export function deleteIdentityMutationOptions() {
  return mutationOptions({
    mutationKey: [...sesKeys.identities(), "delete"] as const,
    mutationFn: (identity: string) => ses.deleteIdentity(identity),
  })
}

export function verifyIdentityMutationOptions() {
  return mutationOptions({
    mutationKey: [...sesKeys.identities(), "verify"] as const,
    mutationFn: (identity: string) => ses.verifyIdentity(identity),
  })
}
