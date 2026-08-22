import { createFileRoute } from "@tanstack/react-router"
import { StepFunctionsPage } from "@/features/stepfunctions/components/stepfunctions-page"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"

type StepFunctionsIndexSearch = {
  /** Free-text filter, deep-linkable — see `useFilterSearchParam`. */
  q?: string
}

export const Route = createFileRoute("/stepfunctions/")({
  head: () => ({ meta: [{ title: "Step Functions — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): StepFunctionsIndexSearch => ({
    q: typeof search.q === "string" ? search.q : undefined,
  }),
  component: function StepFunctionsIndexRoute() {
    const [filter, setFilter] = useFilterSearchParam(Route.useSearch(), Route.useNavigate())
    return <StepFunctionsPage filter={filter} onFilterChange={setFilter} />
  },
})
