import { createFileRoute } from "@tanstack/react-router"
import { EventBridgePage } from "@/features/eventbridge/components/eventbridge-page"

export const Route = createFileRoute("/eventbridge/")({
  head: () => ({ meta: [{ title: "EventBridge — Overcast" }] }),
  component: EventBridgePage,
})
