import { createFileRoute } from "@tanstack/react-router"
import { RealtimeLogConfigList } from "@/features/cloudfront/components/realtime-log-config-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type RealtimeLogConfigsSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/cloudfront/realtime-log-configs")({
  head: () => ({ meta: [{ title: "Realtime Log Configs — CloudFront — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): RealtimeLogConfigsSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function RealtimeLogConfigsRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <RealtimeLogConfigList sort={sort} onSortChange={setSort} />
  },
})
