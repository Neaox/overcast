import { createFileRoute } from "@tanstack/react-router"
import { QueueList } from "@/features/sqs/components/queue-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type SqsIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/sqs/")({
  head: () => ({ meta: [{ title: "SQS Queues — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): SqsIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function SqsIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <QueueList sort={sort} onSortChange={setSort} />
  },
})
