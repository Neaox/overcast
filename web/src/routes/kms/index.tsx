import { createFileRoute } from "@tanstack/react-router"
import { KmsPage } from "@/features/kms/components/kms-page"

export const Route = createFileRoute("/kms/")({
  head: () => ({ meta: [{ title: "KMS — Overcast" }] }),
  component: KmsPage,
})
