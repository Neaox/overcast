import { createFileRoute } from "@tanstack/react-router"
import { UsagePlansPage } from "@/features/apigateway/components/usage-plans-page"

type UsagePlansSearch = {
  apiId?: string
  planId?: string
}

export const Route = createFileRoute("/apigateway/usage-plans")({
  head: () => ({ meta: [{ title: "Usage Plans — API Gateway — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): UsagePlansSearch => ({
    apiId: typeof search.apiId === "string" ? search.apiId : undefined,
    planId: typeof search.planId === "string" ? search.planId : undefined,
  }),
  component: function UsagePlansPageRoute() {
    const { apiId, planId } = Route.useSearch()
    return <UsagePlansPage apiIdFilter={apiId} initialExpandedPlanId={planId} />
  },
})
