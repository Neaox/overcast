import { createFileRoute } from "@tanstack/react-router"
import { RepositoryList } from "@/features/ecr/components/repository-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type EcrIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/ecr/")({
  head: () => ({ meta: [{ title: "ECR Repositories — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): EcrIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function EcrIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <RepositoryList sort={sort} onSortChange={setSort} />
  },
})
