import { useCallback, useMemo } from "react"
import type { ResourceTableSort } from "@/components/ui/resource-table"

/**
 * Two-way binds a list page's table sort to the route's `sort` search param,
 * so a sorted view is shareable, survives a reload, and Back/Forward restores
 * it — the same contract `useFilterSearchParam` gives the filter box's `q` and
 * the tabbed pages give `tab`.
 *
 * The param follows the JSON:API convention: the column id alone is ascending,
 * a leading `-` makes it descending — `?sort=name`, `?sort=-created`. That is
 * the spelling people already read in URLs, and it keeps the common case (sort
 * by this column) down to the column's own name. Clearing the sort removes the
 * param rather than writing an empty string, so an unsorted page has a clean
 * URL.
 *
 * `ResourceTable` sorts by a single column (`enableMultiSort: false`), so only
 * the first entry is read. The comma list JSON:API uses for multi-column sort
 * (`?sort=-created,name`) therefore parses to its highest-priority column
 * rather than failing, which is what a link written by a future multi-sort UI
 * should degrade to.
 *
 * Unlike the filter, this is **not** debounced: a header click is a decision,
 * not a burst of keystrokes. `replace: true` still applies for the same reason
 * it does there — cycling asc → desc → none must not bury the page the user
 * arrived from under three history entries.
 *
 * `search`/`navigate` are the calling route's own `Route.useSearch()` /
 * `Route.useNavigate()`, generic here only in the `{ sort?: string }` shape
 * they must carry:
 *
 * ```tsx
 * // routes/kms/index.tsx
 * export const Route = createFileRoute("/kms/")({
 *   validateSearch: (search: Record<string, unknown>): { q?: string; sort?: string } => ({
 *     q: typeof search.q === "string" ? search.q : undefined,
 *     sort: typeof search.sort === "string" ? search.sort : undefined,
 *   }),
 *   component: function KmsRoute() {
 *     const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
 *     return <KmsPage sort={sort} onSortChange={setSort} … />
 *   },
 * })
 * ```
 *
 * @param search current search params from the route (must include `sort`)
 * @param navigate the route's typed `navigate`, used only for its `search` patch + `replace`
 * @returns `[sort, setSort]`, ready to pass straight to `ResourceTable`'s `sort`/`onSortChange`
 */
export function useSortSearchParam<TSearch extends { sort?: string }>(
  search: TSearch,
  navigate: (opts: { search: (prev: TSearch) => TSearch; replace: true }) => unknown,
): [ResourceTableSort | undefined, (next: ResourceTableSort | undefined) => void] {
  const value = useMemo(() => parseSortParam(search.sort), [search.sort])

  const setValue = useCallback(
    (next: ResourceTableSort | undefined) => {
      const serialized = formatSortParam(next)
      void navigate({ search: (prev) => ({ ...prev, sort: serialized }), replace: true })
    },
    [navigate],
  )

  return [value, setValue]
}

/**
 * Reads `"name"` as ascending and `"-name"` as descending, taking the first
 * entry of a comma list. A missing param, an empty one, or a bare `-` is
 * treated as "not sorted" rather than throwing: the param is user-editable,
 * and a typo in a shared link should degrade to the default order, not a blank
 * page.
 */
export function parseSortParam(raw: string | undefined): ResourceTableSort | undefined {
  if (!raw) return undefined
  const first = raw.split(",")[0].trim()
  if (!first) return undefined
  const desc = first.startsWith("-")
  const id = desc ? first.slice(1) : first
  if (!id) return undefined
  return { id, desc }
}

/** Inverse of `parseSortParam`; `undefined` (not `""`) drops the param entirely. */
export function formatSortParam(sort: ResourceTableSort | undefined): string | undefined {
  if (!sort) return undefined
  return sort.desc ? `-${sort.id}` : sort.id
}
