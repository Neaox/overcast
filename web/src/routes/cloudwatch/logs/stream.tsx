import { createFileRoute } from "@tanstack/react-router"
import { LogEventsViewer } from "@/features/cloudwatch/logs/components/log-events-viewer"

type StreamSearch = {
  groupName: string | undefined
  streamName: string | undefined
  /** Timestamp of an event to open the view centred on, from a search result. */
  anchorTs?: number
  /** Signature disambiguating the event within its millisecond. */
  anchorSig?: string
}

export const Route = createFileRoute("/cloudwatch/logs/stream")({
  validateSearch: (search: Record<string, unknown>): StreamSearch => ({
    groupName: typeof search.groupName === "string" ? search.groupName : undefined,
    streamName: typeof search.streamName === "string" ? search.streamName : undefined,
    ...(typeof search.anchorTs === "number" ? { anchorTs: search.anchorTs } : {}),
    // The signature is base36, so one made only of digits round-trips through
    // the router's search parser as a number — String() it back rather than
    // dropping it and silently degrading to timestamp-only matching.
    ...(typeof search.anchorSig === "string" || typeof search.anchorSig === "number"
      ? { anchorSig: String(search.anchorSig) }
      : {}),
  }),
  component: function LogEventsPage() {
    const { groupName, streamName, anchorTs, anchorSig } = Route.useSearch()
    if (!groupName || !streamName) return null
    return (
      <LogEventsViewer
        groupName={groupName}
        streamName={streamName}
        anchor={anchorTs != null ? { timestamp: anchorTs, signature: anchorSig } : undefined}
      />
    )
  },
})
