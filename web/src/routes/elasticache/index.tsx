import { createFileRoute } from "@tanstack/react-router"
import { ClusterList } from "@/features/elasticache/components/cluster-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type ElasticacheIndexSearch = {
  /** Table sort — `cluster` ascending, `-cluster` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/elasticache/")({
  head: () => ({ meta: [{ title: "ElastiCache Clusters — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): ElasticacheIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function ElasticacheIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <ClusterList sort={sort} onSortChange={setSort} />
  },
})
