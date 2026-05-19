import { createFileRoute } from "@tanstack/react-router"
import { AppSyncPage } from "@/features/appsync/components/appsync-page"

export const Route = createFileRoute("/appsync/")({
  head: () => ({ meta: [{ title: "AppSync — Overcast" }] }),
  component: AppSyncPage,
})
