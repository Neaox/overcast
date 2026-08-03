import { createFileRoute } from "@tanstack/react-router"
import { PipeDetail } from "@/features/pipes/components/pipe-detail"

export const Route = createFileRoute("/pipes/$pipeName")({
  head: ({ params }) => ({
    meta: [{ title: `${params.pipeName} — Pipes — Overcast` }],
  }),
  component: function PipeDetailRoute() {
    const { pipeName } = Route.useParams()
    return <PipeDetail pipeName={pipeName} />
  },
})
