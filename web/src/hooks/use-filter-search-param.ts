import { useCallback } from "react"
import { useDebouncedTextParam } from "./use-debounced-text-param"

/**
 * Two-way binds a list page's free-text filter box to the route's `q` search
 * param, so a filtered view is shareable, survives a reload, and Back/Forward
 * restores it — the same contract the Request Traces filters use (see
 * `useDebouncedTextParam`).
 *
 * `search`/`navigate` are the calling route's own `Route.useSearch()` /
 * `Route.useNavigate()` — generic here only in the `{ q?: string }` shape
 * they must carry, so each route's own search type still flows through
 * type-checked. `replace: true` on every commit is deliberate: a session of
 * typing into the filter box must not bury the page the user arrived from
 * under a dozen history entries (see `useDebouncedTextParam`'s own docblock
 * for why the debounce additionally collapses a burst of keystrokes into
 * one commit).
 *
 * ```tsx
 * // routes/appsync/index.tsx
 * export const Route = createFileRoute("/appsync/")({
 *   validateSearch: (search: Record<string, unknown>): { q?: string } => ({
 *     q: typeof search.q === "string" ? search.q : undefined,
 *   }),
 *   component: function AppSyncRoute() {
 *     const [filter, setFilter] = useFilterSearchParam(Route.useSearch(), Route.useNavigate())
 *     return <AppSyncPage filter={filter} onFilterChange={setFilter} />
 *   },
 * })
 * ```
 *
 * @param search current search params from the route (must include `q`)
 * @param navigate the route's typed `navigate`, used only for its `search` patch + `replace`
 * @param delayMs debounce window before a keystroke commits to the URL
 * @returns `[value, setValue]` for the filter input, exactly as `useDebouncedTextParam` returns
 */
export function useFilterSearchParam<TSearch extends { q?: string }>(
  search: TSearch,
  navigate: (opts: { search: (prev: TSearch) => TSearch; replace: true }) => unknown,
  delayMs = 300,
): [string, (next: string) => void] {
  const commit = useCallback(
    (next: string) => {
      void navigate({ search: (prev) => ({ ...prev, q: next || undefined }), replace: true })
    },
    [navigate],
  )
  return useDebouncedTextParam(search.q ?? "", commit, delayMs)
}
