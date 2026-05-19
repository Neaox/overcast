import { createFileRoute } from "@tanstack/react-router"
import { StsPage } from "@/features/sts/components/sts-page"

export const Route = createFileRoute("/sts/")({
  head: () => ({ meta: [{ title: "STS — Overcast" }] }),
  component: StsPage,
})
