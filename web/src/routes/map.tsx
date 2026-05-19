import { createFileRoute } from "@tanstack/react-router"
import { MapPage } from "@/features/map/map-page"

type MapSearch = {
  focusRegion?: string
}

export const Route = createFileRoute("/map")({
  head: () => ({ meta: [{ title: "Resource Map — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): MapSearch => ({
    focusRegion: typeof search.focusRegion === "string" ? search.focusRegion : undefined,
  }),
  component: function MapPageWrapper() {
    const { focusRegion } = Route.useSearch()
    return <MapPage focusRegion={focusRegion} />
  },
})
