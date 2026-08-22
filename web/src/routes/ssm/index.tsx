import { createFileRoute } from "@tanstack/react-router"
import { SsmPage } from "@/features/ssm/components/ssm-page"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"

type SsmIndexSearch = {
  /** Free-text filter, deep-linkable — see `useFilterSearchParam`. */
  q?: string
}

export const Route = createFileRoute("/ssm/")({
  head: () => ({ meta: [{ title: "SSM Parameter Store — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): SsmIndexSearch => ({
    q: typeof search.q === "string" ? search.q : undefined,
  }),
  component: function SsmIndexRoute() {
    const [filter, setFilter] = useFilterSearchParam(Route.useSearch(), Route.useNavigate())
    return <SsmPage filter={filter} onFilterChange={setFilter} />
  },
})
