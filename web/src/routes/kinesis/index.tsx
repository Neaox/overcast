import { createFileRoute } from "@tanstack/react-router"
import { StreamList } from "@/features/kinesis/components/stream-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type KinesisIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/kinesis/")({
  head: () => ({ meta: [{ title: "Kinesis Data Streams — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): KinesisIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function KinesisIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <StreamList sort={sort} onSortChange={setSort} />
  },
})
