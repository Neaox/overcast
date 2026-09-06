import { createFileRoute } from "@tanstack/react-router"
import { ApplicationList } from "@/features/applications/components/application-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type ApplicationsIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/applications/")({
  head: () => ({ meta: [{ title: "Applications — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): ApplicationsIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function ApplicationsIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <ApplicationList sort={sort} onSortChange={setSort} />
  },
})
