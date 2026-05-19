import { createFileRoute } from "@tanstack/react-router"
import { Dashboard } from "@/features/dashboard/dashboard"

export const Route = createFileRoute("/")({
  head: () => ({ meta: [{ title: "Dashboard — Overcast" }] }),
  component: Dashboard,
})
