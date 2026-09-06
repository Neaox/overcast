import { createFileRoute } from "@tanstack/react-router"
import { ClusterList } from "@/features/msk/components/cluster-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type MskIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/msk/")({
  head: () => ({ meta: [{ title: "MSK Clusters — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): MskIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function MskIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <ClusterList sort={sort} onSortChange={setSort} />
  },
})
