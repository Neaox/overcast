import { createFileRoute } from "@tanstack/react-router"
import { LayerList } from "@/features/lambda/components/layer-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type LambdaLayersIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/lambda/layers/")({
  head: () => ({ meta: [{ title: "Lambda Layers — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): LambdaLayersIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function LambdaLayersIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <LayerList sort={sort} onSortChange={setSort} />
  },
})
