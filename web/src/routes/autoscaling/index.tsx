import { createFileRoute } from "@tanstack/react-router"
import { AutoScalingGroupList } from "@/features/autoscaling/components/group-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type AutoscalingIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/autoscaling/")({
  head: () => ({ meta: [{ title: "Auto Scaling Groups — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): AutoscalingIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function AutoscalingIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <AutoScalingGroupList sort={sort} onSortChange={setSort} />
  },
})
