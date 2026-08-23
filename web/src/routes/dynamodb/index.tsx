import { createFileRoute } from "@tanstack/react-router"
import { TableList } from "@/features/dynamodb/components/table-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type DynamodbIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/dynamodb/")({
  head: () => ({ meta: [{ title: "DynamoDB Tables — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): DynamodbIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function DynamodbIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <TableList sort={sort} onSortChange={setSort} />
  },
})
