import { createFileRoute } from "@tanstack/react-router"
import { ApiKeysPage } from "@/features/apigateway/components/api-keys-page"

export const Route = createFileRoute("/apigateway/api-keys")({
  head: () => ({ meta: [{ title: "API Keys — API Gateway — Overcast" }] }),
  component: ApiKeysPage,
})
