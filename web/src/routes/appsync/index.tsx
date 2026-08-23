import { createFileRoute } from "@tanstack/react-router"
import { AppSyncPage } from "@/features/appsync/components/appsync-page"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type AppSyncIndexSearch = {
  /** Free-text filter, deep-linkable — see `useFilterSearchParam`. */
  q?: string
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/appsync/")({
  head: () => ({ meta: [{ title: "AppSync — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): AppSyncIndexSearch => ({
    q: typeof search.q === "string" ? search.q : undefined,
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function AppSyncIndexRoute() {
    const [filter, setFilter] = useFilterSearchParam(Route.useSearch(), Route.useNavigate())
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return (
      <AppSyncPage filter={filter} onFilterChange={setFilter} sort={sort} onSortChange={setSort} />
    )
  },
})
