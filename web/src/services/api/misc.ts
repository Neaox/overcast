import { apiFetch, API_BASE, endpointHeaders, endpointResolver } from "./base"
import type { MetricsSnapshot, HealthResponse, TopologyResponse, CapturedMessage } from "@/types"

export const metrics = {
  get: () => apiFetch<MetricsSnapshot>("/metrics"),
}

export const health = {
  check: () => apiFetch<HealthResponse>("/health"),
}

export const topology = {
  get: (region?: string) =>
    apiFetch<TopologyResponse>(
      region ? `/topology?region=${encodeURIComponent(region)}` : "/topology",
    ),
}

export const inbox = {
  list: (limit?: number) =>
    apiFetch<CapturedMessage[]>(`/inbox/messages${limit ? `?limit=${limit}` : ""}`),
  get: (id: string) => apiFetch<CapturedMessage>(`/inbox/messages/${encodeURIComponent(id)}`),
  clear: () => apiFetch<void>("/inbox/messages", { method: "DELETE" }),
  delete: (id: string) =>
    apiFetch<void>(`/inbox/messages/${encodeURIComponent(id)}`, { method: "DELETE" }),
}

export type DebugStateSummary = Record<string, string[]>
export type DebugNamespaceValues = Record<string, string>

/** Paginated response shape of GET /_debug/state/{namespace}. */
export type DebugNamespacePage = {
  values: DebugNamespaceValues
  /** Exclusive cursor for the next page; absent/empty on the last page. */
  nextKey?: string
}

export const debugState = {
  list: () => apiFetch<DebugStateSummary>("/debug/state"),
  /**
   * Fetches a single page of a namespace's raw state (storage-plan.md item
   * 3.13, frontend half). `after` is the exclusive cursor from a previous
   * page's `nextKey` (omit/empty for the first page). Callers page
   * incrementally via `useInfiniteQuery` (see `debugNamespaceInfiniteQueryOptions`
   * in `web/src/features/debug/data.ts`) rather than merging every page
   * eagerly — the whole point of the paginated contract is that a caller
   * only fetches as many pages as it actually renders.
   */
  namespacePage: (namespace: string, after?: string, limit?: number): Promise<DebugNamespacePage> => {
    const params = new URLSearchParams()
    if (after) params.set("after", after)
    if (limit) params.set("limit", String(limit))
    const query = params.toString()
    return apiFetch<DebugNamespacePage>(
      `/debug/state/${encodeURIComponent(namespace)}${query ? `?${query}` : ""}`,
    )
  },
  /**
   * Fetches a single key's raw value via `GET /_debug/state/{namespace}?key=`,
   * bypassing pagination entirely. Used as a lazy fallback when a deep-linked
   * key hasn't appeared in any loaded page yet (see `debug-page.tsx`).
   *
   * The endpoint returns the raw stored value as the response body — it is
   * not JSON-enveloped like other debug endpoints (it may not even be JSON:
   * plain strings are returned as `text/plain`) — so this bypasses `apiFetch`
   * and reads the body as text directly. Returns `null` on a 404 (key not
   * found) rather than throwing, since "not found" is an expected,
   * handleable outcome here, not an error condition. (`null`, not
   * `undefined`: this feeds a TanStack Query `queryFn`, which forbids
   * resolving `undefined` — see @tanstack/query/no-void-query-fn.)
   */
  value: async (namespace: string, key: string): Promise<string | null> => {
    const endpoint = endpointResolver.get()
    const res = await fetch(
      `${API_BASE}/debug/state/${encodeURIComponent(namespace)}?key=${encodeURIComponent(key)}`,
      { headers: endpointHeaders(endpoint) },
    )
    if (res.status === 404) return null
    if (!res.ok) {
      const body = (await res.json().catch(() => ({ error: res.statusText }))) as {
        error?: string
        message?: string
      }
      throw new Error(body.message ?? body.error ?? `HTTP ${res.status}`)
    }
    return res.text()
  },
}
