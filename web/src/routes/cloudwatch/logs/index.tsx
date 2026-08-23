import { createFileRoute } from "@tanstack/react-router"
import { LogGroupList } from "@/features/cloudwatch/logs/components/log-group-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type LogsIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/cloudwatch/logs/")({
  head: () => ({ meta: [{ title: "CloudWatch Logs — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): LogsIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function LogsIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <LogGroupList sort={sort} onSortChange={setSort} />
  },
})
