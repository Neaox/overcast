import { createFileRoute } from "@tanstack/react-router"
import { TopicList } from "@/features/sns/components/topic-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type SnsIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/sns/")({
  head: () => ({ meta: [{ title: "SNS Topics — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): SnsIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function SnsIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <TopicList sort={sort} onSortChange={setSort} />
  },
})
