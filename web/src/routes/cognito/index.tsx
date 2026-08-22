import { createFileRoute } from "@tanstack/react-router"
import { CognitoPage } from "@/features/cognito/components/cognito-page"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"

type CognitoIndexSearch = {
  /** Free-text filter, deep-linkable — see `useFilterSearchParam`. */
  q?: string
}

export const Route = createFileRoute("/cognito/")({
  head: () => ({ meta: [{ title: "Cognito — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): CognitoIndexSearch => ({
    q: typeof search.q === "string" ? search.q : undefined,
  }),
  component: function CognitoIndexRoute() {
    const [filter, setFilter] = useFilterSearchParam(Route.useSearch(), Route.useNavigate())
    return <CognitoPage filter={filter} onFilterChange={setFilter} />
  },
})
