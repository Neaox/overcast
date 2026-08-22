import { createFileRoute } from "@tanstack/react-router"
import { EventBridgePage } from "@/features/eventbridge/components/eventbridge-page"
import { EVENTBRIDGE_TABS, type EventBridgeTab } from "@/features/eventbridge/data"
import { useFilterSearchParam } from "@/hooks/use-filter-search-param"

type EventBridgeSearch = {
  /** Selected tab, deep-linkable — defaults to "buses" when absent/unrecognised. */
  tab?: EventBridgeTab
  /** Free-text filter for whichever tab is active, deep-linkable — see `useFilterSearchParam`. */
  q?: string
}

function isEventBridgeTab(value: unknown): value is EventBridgeTab {
  return typeof value === "string" && (EVENTBRIDGE_TABS as readonly string[]).includes(value)
}

export const Route = createFileRoute("/eventbridge/")({
  head: () => ({ meta: [{ title: "EventBridge — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): EventBridgeSearch => ({
    tab: isEventBridgeTab(search.tab) ? search.tab : undefined,
    q: typeof search.q === "string" ? search.q : undefined,
  }),
  component: function EventBridgeIndexRoute() {
    const search = Route.useSearch()
    const navigate = Route.useNavigate()
    const [filter, setFilter] = useFilterSearchParam(search, navigate)

    return (
      <EventBridgePage
        tab={search.tab ?? "buses"}
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
