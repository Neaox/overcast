import { createFileRoute } from "@tanstack/react-router"
import { LayerDetail } from "@/features/lambda/components/layer-detail"

export const Route = createFileRoute("/lambda/layers/$layerName")({
  head: ({ params }) => ({ meta: [{ title: `${params.layerName} — Lambda Layers — Overcast` }] }),
  component: LayerDetail,
})
