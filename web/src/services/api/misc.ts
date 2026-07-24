import { apiFetch } from "./base"
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

/**
 * Safety bound on cursor-following, not an expected limit — at the server's
 * default page size this covers namespaces far larger than the debug UI can
 * usefully render, and it guards against a pathological cursor loop.
 */
const MAX_DEBUG_NAMESPACE_PAGES = 200

export const debugState = {
  list: () => apiFetch<DebugStateSummary>("/debug/state"),
  /**
   * Fetches every page of a namespace and merges them into one map, so
   * consumers keep the pre-pagination "whole namespace" view. The 3.13
   * frontend work (virtualized rows, lazy values) will replace this with
   * true incremental paging.
   */
  namespace: async (namespace: string): Promise<DebugNamespaceValues> => {
    const merged: DebugNamespaceValues = {}
    let after = ""
    for (let page = 0; page < MAX_DEBUG_NAMESPACE_PAGES; page++) {
      const query = after ? `?after=${encodeURIComponent(after)}` : ""
      const res = await apiFetch<DebugNamespacePage>(
        `/debug/state/${encodeURIComponent(namespace)}${query}`,
      )
      Object.assign(merged, res.values)
      if (!res.nextKey) break
      after = res.nextKey
    }
    return merged
  },
}
