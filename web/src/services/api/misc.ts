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

export const debugState = {
  list: () => apiFetch<DebugStateSummary>("/debug/state"),
  namespace: (namespace: string) =>
    apiFetch<DebugNamespaceValues>(`/debug/state/${encodeURIComponent(namespace)}`),
}
