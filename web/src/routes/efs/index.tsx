import { createFileRoute } from "@tanstack/react-router"
import { FileSystemList } from "@/features/efs/components/file-system-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type EfsIndexSearch = {
  /** Table sort — `name` ascending, `-name` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/efs/")({
  head: () => ({ meta: [{ title: "EFS File Systems — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): EfsIndexSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function EfsIndexRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <FileSystemList sort={sort} onSortChange={setSort} />
  },
})
