import { matchesQuery, registerContributor } from "@/lib/search"
import type { SearchContributor, SearchContext, SearchResult } from "@/lib/search"
import type { EmulatorEndpoint } from "@/services/discovery"

/**
 * Factory for creating and registering a search contributor with a consistent pattern.
 *
 * Most contributors follow the same structure: fetch or read cached data,
 * filter by query, and map to SearchResult. This factory eliminates the boilerplate.
 */
export interface ContributorConfig<T> {
  /** Unique contributor ID (e.g. "s3", "sqs") */
  id: string
  /** React Query cache key. Receives the endpoint for key construction. */
  cacheKey: (endpoint: EmulatorEndpoint) => readonly unknown[]
  /** Fetches the full list when no cache is available. Should never throw. */
  fetchAll: () => Promise<T[]>
  /** Fields to match against the search query. */
  matchFields: (item: T) => (string | undefined)[]
  /** Maps the item to a SearchResult. */
  toResult: (item: T) => SearchResult
}

export function createSearchContributor<T>(config: ContributorConfig<T>): void {
  const contributor: SearchContributor = {
    id: config.id,
    async search(query: string, ctx: SearchContext): Promise<SearchResult[]> {
      const cached = ctx.queryClient.getQueryData<T[]>(config.cacheKey(ctx.endpoint))
      const items = cached ?? (await config.fetchAll().catch(() => []))
      return items
        .filter((item) => matchesQuery(query, ...config.matchFields(item)))
        .map(config.toResult)
    },
  }
  registerContributor(contributor)
}
