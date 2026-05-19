import { createFileRoute } from "@tanstack/react-router"
import { EventBusDetail } from "@/features/eventbridge/components/event-bus-detail"

export const Route = createFileRoute("/eventbridge/$busName")({
  head: ({ params }) => ({
    meta: [{ title: `${params.busName} — EventBridge — Overcast` }],
  }),
  component: function EventBusDetailRoute() {
    const { busName } = Route.useParams()
    return <EventBusDetail busName={busName} />
  },
})
