import { createFileRoute } from "@tanstack/react-router"
import { StepFunctionsPage } from "@/features/stepfunctions/components/stepfunctions-page"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type StepFunctionsIndexSearch = {
  /** Free-text filter, deep-linkable — see `useFilterSearchParam`. */
  q?: string
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/stepfunctions/")({
  head: () => ({ meta: [{ title: "Step Functions — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): StepFunctionsIndexSearch => ({
    q: typeof search.q === "string" ? search.q : undefined,
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function StepFunctionsIndexRoute() {
    const [filter, setFilter] = useFilterSearchParam(Route.useSearch(), Route.useNavigate())
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return (
      <StepFunctionsPage
        filter={filter}
        onFilterChange={setFilter}
        sort={sort}
        onSortChange={setSort}
      />
    )
  },
})
