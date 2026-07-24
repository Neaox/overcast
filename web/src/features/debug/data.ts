import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query"
import { debugState } from "@/services/api/misc"

export const debugKeys = {
  all: ["debug"] as const,
  state: () => [...debugKeys.all, "state"] as const,
  namespace: (namespace: string) => [...debugKeys.state(), namespace] as const,
  value: (namespace: string, key: string) => [...debugKeys.namespace(namespace), "value", key] as const,
}

/**
 * Page size requested from the server per fetch. Kept well under the
 * server's default (`debugStateDefaultPageLimit` = 500 in
 * internal/router/debug.go) and explicit here — rather than omitted to rely
 * on the server default — so the client/server contract stays visible in
 * one place if either side's default ever drifts.
 */
const DEBUG_NAMESPACE_PAGE_SIZE = 500

export function debugStateQueryOptions() {
  return queryOptions({
    queryKey: debugKeys.state(),
    queryFn: () => debugState.list(),
  })
}

/**
 * Incremental paging over GET /_debug/state/{namespace} (storage-plan.md
 * item 3.13, frontend half). Each page is fetched only when the UI asks for
 * it (see debug-page.tsx's scroll-triggered fetchNextPage) — this
 * deliberately does NOT merge every page eagerly the way the old
 * `debugState.namespace` shim did.
 */
export function debugNamespaceInfiniteQueryOptions(namespace: string) {
  return infiniteQueryOptions({
    queryKey: debugKeys.namespace(namespace),
    queryFn: ({ pageParam }) =>
      debugState.namespacePage(namespace, pageParam || undefined, DEBUG_NAMESPACE_PAGE_SIZE),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.nextKey || undefined,
    enabled: namespace !== "",
  })
}

/**
 * Single-key lazy fetch, used as a fallback when a requested key (e.g. from
 * a deep link) hasn't appeared in any loaded page of the namespace yet.
 * `enabled` is caller-driven — see debug-page.tsx's `usingValueFallback`.
 */
export function debugValueQueryOptions(namespace: string, key: string, enabled: boolean) {
  return queryOptions({
    queryKey: debugKeys.value(namespace, key),
    queryFn: () => debugState.value(namespace, key),
    enabled: enabled && namespace !== "" && key !== "",
  })
}
