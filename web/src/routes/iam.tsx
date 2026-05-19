import { createFileRoute } from "@tanstack/react-router"
import { IAMPage } from "@/features/iam/components/iam-page"

export const Route = createFileRoute("/iam")({
  head: () => ({ meta: [{ title: "IAM — Overcast" }] }),
  component: IAMPage,
})
