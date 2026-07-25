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
    const navigate = Route.useNavigate()
    return (
      <DebugPage
        // Remounts on namespace/service change so a URL edit, browser
        // back/forward, or fresh deep link always re-derives state cleanly.
        // Key changes deliberately do NOT remount — DebugPage syncs those
        // itself (see its `initialKey` effect) so clicking through many
        // keys stays lightweight.
        key={`${service ?? ""}:${namespace ?? ""}`}
        initialService={service}
        initialNamespace={namespace}
        initialKey={key}
        onNamespaceChange={(next) =>
          void navigate({
            search: {
              service: next.service || undefined,
              namespace: next.namespace || undefined,
              key: undefined,
            },
            replace: false,
          })
        }
        onKeyChange={(nextKey) =>
          void navigate({
            search: (prev) => ({ ...prev, key: nextKey || undefined }),
            replace: true,
          })
        }
      />
    )
  },
})
