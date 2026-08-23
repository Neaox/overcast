import { createFileRoute } from "@tanstack/react-router"
import { BucketList } from "@/features/s3/components/bucket-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type S3IndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/s3/")({
  head: () => ({ meta: [{ title: "S3 Buckets — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): S3IndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function S3IndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <BucketList sort={sort} onSortChange={setSort} />
  },
})
