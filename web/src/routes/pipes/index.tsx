import { createFileRoute } from "@tanstack/react-router"
import { PipeList } from "@/features/pipes/components/pipe-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type PipesIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/pipes/")({
  head: () => ({ meta: [{ title: "Pipes — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): PipesIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function PipesIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <PipeList sort={sort} onSortChange={setSort} />
  },
})
