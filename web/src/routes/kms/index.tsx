import { createFileRoute } from "@tanstack/react-router"
import { KmsPage } from "@/features/kms/components/kms-page"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type KmsIndexSearch = {
  /** Free-text filter, deep-linkable — see `useFilterSearchParam`. */
  q?: string
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/kms/")({
  head: () => ({ meta: [{ title: "KMS — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): KmsIndexSearch => ({
    q: typeof search.q === "string" ? search.q : undefined,
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function KmsIndexRoute() {
    const [filter, setFilter] = useFilterSearchParam(Route.useSearch(), Route.useNavigate())
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <KmsPage filter={filter} onFilterChange={setFilter} sort={sort} onSortChange={setSort} />
  },
})
