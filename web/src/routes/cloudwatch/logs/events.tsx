import { createFileRoute } from "@tanstack/react-router"
import { LogEventsViewer } from "@/features/cloudwatch/logs/components/log-events-viewer"

type EventsSearch = {
  groupName: string | undefined
  /** Timestamp of an event to open the view centred on — a jump target. */
  anchorTs?: number
  /** Signature disambiguating the event within its millisecond. */
  anchorSig?: string
}

export const Route = createFileRoute("/cloudwatch/logs/events")({
  validateSearch: (search: Record<string, unknown>): EventsSearch => ({
    groupName: typeof search.groupName === "string" ? search.groupName : undefined,
    ...(typeof search.anchorTs === "number" ? { anchorTs: search.anchorTs } : {}),
    ...(typeof search.anchorSig === "string" ? { anchorSig: search.anchorSig } : {}),
  }),
  head: () => ({
    meta: [{ title: "CloudWatch Logs — Overcast" }],
  }),
  component: function LogGroupEventsPage() {
    const { groupName, anchorTs, anchorSig } = Route.useSearch()
    if (!groupName) return null
    return (
      <LogEventsViewer
        groupName={groupName}
        anchor={anchorTs != null ? { timestamp: anchorTs, signature: anchorSig } : undefined}
      />
    )
  },
})
