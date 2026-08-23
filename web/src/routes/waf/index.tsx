import { createFileRoute } from "@tanstack/react-router"
import { WebACLList } from "@/features/waf/components/web-acl-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type WafIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/waf/")({
  head: () => ({ meta: [{ title: "WAF Web ACLs — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): WafIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function WafIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <WebACLList sort={sort} onSortChange={setSort} />
  },
})
