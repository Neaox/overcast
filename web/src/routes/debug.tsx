import { createFileRoute } from "@tanstack/react-router"
import { DebugPage } from "@/features/debug/debug-page"

type DebugSearch = {
  service?: string
  namespace?: string
  key?: string
}

export const Route = createFileRoute("/debug")({
  head: () => ({ meta: [{ title: "Raw State Debugger — Overcast" }] }),
  validateSearch: (search: Record<string, unknown>): DebugSearch => ({
    service: typeof search.service === "string" ? search.service : undefined,
    namespace: typeof search.namespace === "string" ? search.namespace : undefined,
    key: typeof search.key === "string" ? search.key : undefined,
  }),
  component: function DebugPageWrapper() {
    const { service, namespace, key } = Route.useSearch()
    return (
      <DebugPage
        key={`${service ?? ""}:${namespace ?? ""}:${key ?? ""}`}
        initialService={service}
        initialNamespace={namespace}
        initialKey={key}
      />
    )
  },
})
