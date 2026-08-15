/**
 * Environment preflight query definitions.
 *
 * Key factory:
 *   preflightKeys.all()          -> [baseUrl, region, "preflight"]
 *   preflightKeys.region(kind)   -> [baseUrl, region, "preflight", "region", kind]
 */

import { queryOptions } from "@tanstack/react-query"
import { preflight } from "@/services/api"
import type { PreflightRegionKind } from "@/services/api/preflight"
import { endpointStore } from "@/services/endpoint-store"

// ─── Key factory ───────────────────────────────────────────────────────────

export const preflightKeys = {
  all: () => [...endpointStore.getKeys(), "preflight"] as const,
  region: (kind: PreflightRegionKind) => [...preflightKeys.all(), "region", kind] as const,
}

// ─── Query definitions ─────────────────────────────────────────────────────

/**
 * Whether an empty list page is empty because the resources are in another
 * region.
 *
 * The key carries the selected region (via `endpointStore.getKeys()`), which
 * it must: the answer is a comparison against that region, so a cached result
 * from before a region switch would be a sentence about somewhere else.
 *
 * Only mount the component that runs this once a page has genuinely rendered
 * nothing. The check costs one namespace read on the server, which is cheap
 * but not free, and a page that showed rows has no symptom to explain.
 */
export function preflightRegionQueryOptions(kind: PreflightRegionKind) {
  return queryOptions({
    queryKey: preflightKeys.region(kind),
    queryFn: () => preflight.region(kind),
  })
}
