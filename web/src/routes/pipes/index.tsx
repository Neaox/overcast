import { createFileRoute } from "@tanstack/react-router"
import { PipeList } from "@/features/pipes/components/pipe-list"

export const Route = createFileRoute("/pipes/")({
  head: () => ({ meta: [{ title: "Pipes — Overcast" }] }),
  component: PipeList,
})
