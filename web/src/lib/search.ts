/**
 * Global search infrastructure.
 *
 * Usage:
 *   Register a contributor once per service:
 *     registerContributor(myContributor)
 *
 *   Run a search across all registered contributors:
 *     const grouped = await runSearch(query, ctx)
 *
 * Results are grouped by serviceKey (e.g. "/s3") so the UI can render
 * a section header per service.
 */

import type { QueryClient } from "@tanstack/react-query"
import type { EmulatorEndpoint } from "@/services/discovery"

// ─── Result ────────────────────────────────────────────────────────────────

export interface SearchResult {
  /** Globally unique ID within a search run, e.g. "s3:my-bucket". */
  id: string
  /** Primary display text — the resource name. */
  label: string
  /** Secondary display text — ARN, URL, or other identifier. */
  sublabel?: string
  /** Human-readable service name shown in the group header, e.g. "S3". */
  service: string
  /** Key matching a ServiceDefinition, e.g. "/s3". Used for grouping. */
  serviceKey: string
  /** Resource type label shown as a badge, e.g. "Bucket", "Queue". */
  type: string
  /** Relative href the result navigates to when selected. */
  href: string
}

// ─── Contributor contract ──────────────────────────────────────────────────

export interface SearchContext {
  /**
   * The TanStack QueryClient. Contributors should try
   * `queryClient.getQueryData(key)` first (instant, no network) and fall back
   * to an `api.*` call only when no cache is available.
   */
  queryClient: QueryClient
  /** Currently configured emulator endpoint — needed for cache key lookups. */
  endpoint: EmulatorEndpoint
}

export interface SearchContributor {
  /**
   * Unique identifier — typically the service route key, e.g. "s3".
   * Used only for deduplication; not shown in the UI.
   */
  id: string
  /**
   * Return search results matching `query`.
   * - Called only when `query` is non-empty.
   * - Should never throw — return [] on error.
   * - Should resolve quickly; prefer cache over fresh fetches.
   */
  search(query: string, ctx: SearchContext): Promise<SearchResult[]>
}

// ─── Registry ─────────────────────────────────────────────────────────────

const _contributors: SearchContributor[] = []

export function registerContributor(c: SearchContributor): void {
  if (!_contributors.find((x) => x.id === c.id)) {
    _contributors.push(c)
  }
}

// ─── Runner ────────────────────────────────────────────────────────────────

/**
 * Run all registered contributors concurrently and return results grouped
 * by `serviceKey`. The insertion order of groups matches contributor
 * registration order.
 */
export async function runSearch(
  query: string,
  ctx: SearchContext,
): Promise<Map<string, SearchResult[]>> {
  const settled = await Promise.allSettled(_contributors.map((c) => c.search(query, ctx)))

  const grouped = new Map<string, SearchResult[]>()

  for (const outcome of settled) {
    if (outcome.status !== "fulfilled") continue
    for (const item of outcome.value) {
      if (!grouped.has(item.serviceKey)) grouped.set(item.serviceKey, [])
      grouped.get(item.serviceKey)!.push(item)
    }
  }

  return grouped
}

// ─── Helpers (shared across contributors) ─────────────────────────────────

/** Case-insensitive substring match against one or more string fields. */
export function matchesQuery(query: string, ...fields: (string | undefined)[]): boolean {
  const q = query.toLowerCase()
  return fields.some((f) => f?.toLowerCase().includes(q))
}
