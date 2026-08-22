import { createFileRoute } from "@tanstack/react-router"
import { KmsPage } from "@/features/kms/components/kms-page"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"

type KmsIndexSearch = {
  /** Free-text filter, deep-linkable — see `useFilterSearchParam`. */
  q?: string
}

export const Route = createFileRoute("/kms/")({
  head: () => ({ meta: [{ title: "KMS — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): KmsIndexSearch => ({
    q: typeof search.q === "string" ? search.q : undefined,
  }),
  component: function KmsIndexRoute() {
    const [filter, setFilter] = useFilterSearchParam(Route.useSearch(), Route.useNavigate())
    return <KmsPage filter={filter} onFilterChange={setFilter} />
  },
})
