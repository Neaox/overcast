import { createFileRoute } from "@tanstack/react-router"
import { ApiGatewayList } from "@/features/apigateway/components/api-list"

export const Route = createFileRoute("/apigateway/")({
  head: () => ({ meta: [{ title: "API Gateway — Overcast" }] }),
  component: ApiGatewayList,
})
