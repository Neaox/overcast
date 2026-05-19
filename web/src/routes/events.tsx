import { createFileRoute } from "@tanstack/react-router"
import { EventsPage } from "@/features/events/events-page"

export const Route = createFileRoute("/events")({
  head: () => ({ meta: [{ title: "Events — Overcast" }] }),
  component: EventsPage,
})
