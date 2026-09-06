import { createFileRoute } from "@tanstack/react-router"
import { FunctionList } from "@/features/lambda/components/function-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type LambdaIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/lambda/")({
  head: () => ({ meta: [{ title: "Lambda Functions — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): LambdaIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function LambdaIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <FunctionList sort={sort} onSortChange={setSort} />
  },
})
