import { createFileRoute } from "@tanstack/react-router"
import { KeyGroupList } from "@/features/cloudfront/components/key-group-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type KeyGroupsSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/cloudfront/key-groups")({
  head: () => ({ meta: [{ title: "Key Groups — CloudFront — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): KeyGroupsSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function KeyGroupsRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <KeyGroupList sort={sort} onSortChange={setSort} />
  },
})
