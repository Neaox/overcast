import { createFileRoute } from "@tanstack/react-router"
import { SesPage } from "@/features/ses/ses-page"

export const Route = createFileRoute("/ses")({
  head: () => ({ meta: [{ title: "SES — Overcast" }] }),
  component: SesPage,
})
