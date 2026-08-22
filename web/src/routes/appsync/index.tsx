import { createFileRoute } from "@tanstack/react-router"
import { AppSyncPage } from "@/features/appsync/components/appsync-page"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"

type AppSyncIndexSearch = {
  /** Free-text filter, deep-linkable — see `useFilterSearchParam`. */
  q?: string
}

export const Route = createFileRoute("/appsync/")({
  head: () => ({ meta: [{ title: "AppSync — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): AppSyncIndexSearch => ({
    q: typeof search.q === "string" ? search.q : undefined,
  }),
  component: function AppSyncIndexRoute() {
    const [filter, setFilter] = useFilterSearchParam(Route.useSearch(), Route.useNavigate())
    return <AppSyncPage filter={filter} onFilterChange={setFilter} />
  },
})
