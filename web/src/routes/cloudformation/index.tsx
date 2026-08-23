import { createFileRoute } from "@tanstack/react-router"
import { StackList } from "@/features/cloudformation/components/stack-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type CloudFormationIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/cloudformation/")({
  head: () => ({ meta: [{ title: "CloudFormation Stacks — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): CloudFormationIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function CloudFormationIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <StackList sort={sort} onSortChange={setSort} />
  },
})
