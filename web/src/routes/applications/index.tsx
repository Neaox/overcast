import { createFileRoute } from "@tanstack/react-router"
import { ApplicationList } from "@/features/applications/components/application-list"

export const Route = createFileRoute("/applications/")({
  head: () => ({ meta: [{ title: "Applications — Overcast" }] }),
  component: ApplicationList,
})
