import { createFileRoute } from "@tanstack/react-router"
import { DistributionList } from "@/features/cloudfront/components/distribution-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type CloudFrontIndexSearch = {
  /** Table sort — `id` ascending, `-id` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/cloudfront/")({
  head: () => ({ meta: [{ title: "CloudFront Distributions — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): CloudFrontIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function CloudFrontIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <DistributionList sort={sort} onSortChange={setSort} />
  },
})
