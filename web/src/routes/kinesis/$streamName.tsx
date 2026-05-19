import { createFileRoute } from "@tanstack/react-router"
import { StreamDetail } from "@/features/kinesis/components/stream-detail"

export const Route = createFileRoute("/kinesis/$streamName")({
  head: ({ params }) => ({
    meta: [{ title: `${params.streamName} — Kinesis — Overcast` }],
  }),
  component: function StreamDetailRoute() {
    const { streamName } = Route.useParams()
    return <StreamDetail streamName={streamName} />
  },
})
