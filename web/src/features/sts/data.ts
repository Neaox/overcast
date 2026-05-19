/**
 * STS query definitions.
 */

import { queryOptions } from "@tanstack/react-query"
import { sts } from "@/services/api/sts"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const stsKeys = {
  all: () => [...endpointStore.getKeys(), "sts"] as const,
  callerIdentity: () => [...stsKeys.all(), "callerIdentity"] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

export function stsCallerIdentityQueryOptions() {
  return queryOptions({
    queryKey: stsKeys.callerIdentity(),
    queryFn: () => sts.getCallerIdentity(),
  })
}
