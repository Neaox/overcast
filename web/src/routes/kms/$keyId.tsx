import { createFileRoute } from "@tanstack/react-router"
import { KmsKeyDetail } from "@/features/kms/components/kms-key-detail"

export const Route = createFileRoute("/kms/$keyId")({
  head: ({ params }) => ({
    meta: [{ title: `${params.keyId} — KMS — Overcast` }],
  }),
  component: function KmsKeyDetailRoute() {
    const { keyId } = Route.useParams()
    return <KmsKeyDetail keyId={keyId} />
  },
})
