import { createFileRoute } from "@tanstack/react-router"
import { IAMPage } from "@/features/iam/components/iam-page"
import { IAM_TABS, type IamTab } from "@/features/iam/data"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"

type IamSearch = {
  /** Selected tab, deep-linkable — defaults to "users" when absent/unrecognised. */
  tab?: IamTab
  /** Free-text filter for whichever tab is active, deep-linkable — see `useFilterSearchParam`. */
  q?: string
}

function isIamTab(value: unknown): value is IamTab {
  return typeof value === "string" && (IAM_TABS as readonly string[]).includes(value)
}

export const Route = createFileRoute("/iam")({
  head: () => ({ meta: [{ title: "IAM — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): IamSearch => ({
    tab: isIamTab(search.tab) ? search.tab : undefined,
    q: typeof search.q === "string" ? search.q : undefined,
  }),
  component: function IamRoute() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    const [filter, setFilter] = useFilterSearchParam(search, navigate)

    return (
      <IAMPage
        tab={search.tab ?? "users"}
        onTabChange={(tab) => {
          // Switching tabs already remounts the destination tab with a blank
          // filter (see `TabPanel`, which unmounts every panel but the
          // selected one) — the URL param is reset the same way rather than
          // carrying one tab's filter text into another tab's box.
          void navigate({ search: (prev) => ({ ...prev, tab, q: undefined }), replace: true })
        }}
        filter={filter}
        onFilterChange={setFilter}
      />
    )
  },
})
