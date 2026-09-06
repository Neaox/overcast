import { createFileRoute } from "@tanstack/react-router"
import { ClusterList } from "@/features/ecs/components/cluster-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type EcsIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/ecs/")({
  head: () => ({ meta: [{ title: "ECS Clusters — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): EcsIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function EcsIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <ClusterList sort={sort} onSortChange={setSort} />
  },
})
