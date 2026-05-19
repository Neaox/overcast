import { createFileRoute } from "@tanstack/react-router"
import { LayerList } from "@/features/lambda/components/layer-list"

export const Route = createFileRoute("/lambda/layers/")({
  head: () => ({ meta: [{ title: "Lambda Layers — Overcast" }] }),
  component: LayerList,
})
