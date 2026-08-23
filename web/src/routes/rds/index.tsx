import { createFileRoute } from "@tanstack/react-router"
import { InstanceList } from "@/features/rds/components/instance-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type RdsIndexSearch = {
  /** Table sort — `instance` ascending, `-instance` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/rds/")({
  head: () => ({ meta: [{ title: "RDS Instances — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): RdsIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function RdsIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <InstanceList sort={sort} onSortChange={setSort} />
  },
})
