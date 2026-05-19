/**
 * Shared HTTP helpers for the API client modules.
 *
 * All per-service API modules import from here.
 */

import { endpointResolver } from "../discovery"
import type { EmulatorEndpoint } from "../discovery"

export const API_BASE = "/api"

export function endpointHeaders(endpoint: EmulatorEndpoint): Record<string, string> {
  return {
    "x-overcast-endpoint": endpoint.baseUrl,
    "x-overcast-region": endpoint.region,
  }
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const endpoint = endpointResolver.get()
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...endpointHeaders(endpoint),
      ...init?.headers,
    },
  })

  if (!res.ok) {
    const body = (await res.json().catch(() => ({ error: res.statusText }))) as {
      error?: string
      message?: string
    }
    throw new Error(body.message ?? body.error ?? `HTTP ${res.status}`)
  }

  // 204 / empty body
  const text = await res.text()
  return text ? (JSON.parse(text) as T) : ({} as T)
}

export { endpointResolver }
