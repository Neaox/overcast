import { createFileRoute } from "@tanstack/react-router"
import { SecretDetail } from "@/features/secretsmanager/components/secret-detail"

export const Route = createFileRoute("/secretsmanager/$secretName")({
  head: ({ params }) => ({
    meta: [{ title: `${params.secretName} — Secrets Manager — Overcast` }],
  }),
  component: function SecretDetailRoute() {
    const { secretName } = Route.useParams()
    return <SecretDetail secretName={secretName} />
  },
})
