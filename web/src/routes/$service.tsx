import { createFileRoute, notFound } from "@tanstack/react-router"
import { PlaceholderPage } from "@/features/placeholder/placeholder-page"
import { CATALOG_BY_ID } from "@/lib/unsupported-services"

export const Route = createFileRoute("/$service")({
  loader: ({ params }) => {
    const entry = CATALOG_BY_ID[params.service]
    // TanStack Router expects throwing the notFound sentinel here.
    // eslint-disable-next-line @typescript-eslint/only-throw-error
    if (!entry) throw notFound()
    return entry
  },
  component: ServicePlaceholderPage,
})

function ServicePlaceholderPage() {
  const entry = Route.useLoaderData()
  return (
    <PlaceholderPage
      serviceName={entry.label}
      description={entry.description}
      docsUrl={entry.awsDocsUrl}
      tier={entry.tier}
      goalTier={entry.goalTier}
      reason={entry.reason}
    />
  )
}
