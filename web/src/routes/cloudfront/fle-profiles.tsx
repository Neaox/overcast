import { createFileRoute } from "@tanstack/react-router"
import { FLEProfileList } from "@/features/cloudfront/components/fle-profile-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type FLEProfilesSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/cloudfront/fle-profiles")({
  head: () => ({ meta: [{ title: "Field-Level Encryption Profiles — CloudFront — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): FLEProfilesSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function FLEProfilesRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <FLEProfileList sort={sort} onSortChange={setSort} />
  },
})
