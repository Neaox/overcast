import { createFileRoute } from "@tanstack/react-router"
import { FLEConfigList } from "@/features/cloudfront/components/fle-config-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type FLEConfigsSearch = {
  /** Table sort — `id` ascending, `-id` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/cloudfront/fle-configs")({
  head: () => ({ meta: [{ title: "Field-Level Encryption Configs — CloudFront — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): FLEConfigsSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function FLEConfigsRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <FLEConfigList sort={sort} onSortChange={setSort} />
  },
})
